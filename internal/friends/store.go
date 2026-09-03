package friends

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/hjordan6/trivial/internal/db"
)

// UpsertInvite returns the caller's reusable invite, creating it on first use.
//
// token is supplied rather than drawn here so this package stays pure SQL and a
// test can pin the value -- the same division accounts.RequestCode makes by
// returning its code instead of sending it. On a conflict the supplied token is
// discarded and the stored one returned, which is exactly what makes a link
// already shared with somebody keep working.
//
// The no-op-shaped DO UPDATE is what gets a RETURNING row whether the insert
// landed or conflicted, the same trick accounts.upsertUser uses.
func UpsertInvite(ctx context.Context, q db.DBTX, userID int64, nickname, token string, now time.Time) (Invite, error) {
	nickname = truncateRunes(strings.TrimSpace(nickname))
	if nickname == "" {
		// Caught here rather than left to the NOT NULL and the length CHECK, so
		// a direct caller gets a sentence instead of a constraint violation.
		// The HTTP layer always supplies a fallback, so this is unreachable
		// from a request.
		return Invite{}, ErrEmptyNickname
	}

	var inv Invite
	err := q.QueryRow(ctx,
		`INSERT INTO friend_invites (token, user_id, nickname, created_at)
		 VALUES ($1, $2, $3, $4)
		 ON CONFLICT (user_id) DO UPDATE SET nickname = EXCLUDED.nickname
		 RETURNING token, user_id, nickname`,
		token, userID, nickname, now).Scan(&inv.Token, &inv.UserID, &inv.Nickname)
	if err != nil {
		return Invite{}, fmt.Errorf("upsert friend invite: %w", err)
	}
	return inv, nil
}

// InviteByToken resolves a link to its sender.
//
// It returns no email, and there is no variant of it that does: the token is a
// bearer credential that will end up forwarded into group chats, and a route
// that echoed the sender's address would turn every such link into a way to
// read it off the server.
func InviteByToken(ctx context.Context, q db.DBTX, token string) (Invite, error) {
	var inv Invite
	err := q.QueryRow(ctx,
		`SELECT token, user_id, nickname FROM friend_invites WHERE token = $1`,
		token).Scan(&inv.Token, &inv.UserID, &inv.Nickname)
	if errors.Is(err, pgx.ErrNoRows) {
		return Invite{}, ErrNoInvite
	}
	if err != nil {
		return Invite{}, fmt.Errorf("look up friend invite: %w", err)
	}
	return inv, nil
}

// Accept records a mutual friendship between the invite's sender and userID.
//
// One statement, in three parts. The lookup, the insert, and the report of what
// the insert did all have to agree, and splitting them would open a window
// where a concurrent accept makes the reported outcome wrong.
//
// least()/greatest() is what satisfies the CHECK (user_low < user_high), so the
// primary key rejects a duplicate in either direction, and ON CONFLICT DO
// NOTHING turns that rejection into the already_friends outcome rather than an
// error. Together they mean a double-tap costs nothing and needs no prior
// existence check.
//
// This is a single data-modifying CTE read by the outer SELECT, not two of them
// touching one row -- that second shape is the one Postgres documents as
// unpredictable, and accounts.VerifyCode avoids it for the same reason.
func Accept(ctx context.Context, q db.DBTX, token string, userID int64, now time.Time) (Invite, Outcome, error) {
	var inv Invite
	var inserted bool
	err := q.QueryRow(ctx,
		`WITH inv AS (
		     SELECT token, user_id, nickname FROM friend_invites WHERE token = $1
		 ), ins AS (
		     INSERT INTO friendships (user_low, user_high, created_at)
		     SELECT least(inv.user_id, $2), greatest(inv.user_id, $2), $3
		       FROM inv
		      WHERE inv.user_id <> $2
		     ON CONFLICT DO NOTHING
		     RETURNING 1
		 )
		 SELECT inv.token, inv.user_id, inv.nickname, EXISTS (SELECT 1 FROM ins)
		   FROM inv`,
		token, userID, now).Scan(&inv.Token, &inv.UserID, &inv.Nickname, &inserted)
	if errors.Is(err, pgx.ErrNoRows) {
		return Invite{}, "", ErrNoInvite
	}
	if err != nil {
		return Invite{}, "", fmt.Errorf("accept friend invite: %w", err)
	}
	// The WHERE in the CTE already declined to insert this one; reporting it as
	// its own error is what lets the page say "this is your own link" instead of
	// claiming a friendship that was never made.
	if inv.UserID == userID {
		return inv, "", ErrSelfInvite
	}
	if inserted {
		return inv, OutcomeAdded, nil
	}
	return inv, OutcomeAlreadyFriends, nil
}

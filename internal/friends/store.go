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

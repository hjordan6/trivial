package accounts

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/hjordan6/trivial/internal/db"
)

// RequestCode records a new sign-in code for email and returns it for delivery.
//
// It deliberately does not send mail: the Sender lives in the HTTP layer so this
// package stays pure SQL and needs no provider to test. The cost is that a failed
// send leaves a live code nobody received, which is harmless -- the player just
// asks for another, and that supersedes it.
//
// It creates no user row. A user only exists once someone has proved they can
// read the address, which is also why this function cannot leak whether an
// address is already known: it never looks.
//
// requestIP may be empty, in which case the per-IP limit does not apply.
func RequestCode(ctx context.Context, q db.DBTX, cfg Config, email, requestIP string, now time.Time) (string, error) {
	cfg = cfg.withDefaults()
	email, err := NormalizeEmail(email)
	if err != nil {
		return "", err
	}

	if err := checkRequestLimits(ctx, q, cfg, email, requestIP, now); err != nil {
		return "", err
	}

	code, err := newCode()
	if err != nil {
		return "", err
	}

	// Invalidating the previous live codes is what keeps the attempt budget
	// non-cumulative: only ever one code per address is checkable, so guesses
	// cannot be spread across several of them.
	if _, err := q.Exec(ctx,
		`UPDATE login_tokens SET consumed_at=$2 WHERE email=$1 AND consumed_at IS NULL`,
		email, now); err != nil {
		return "", fmt.Errorf("supersede live login codes: %w", err)
	}

	if _, err := q.Exec(ctx,
		`INSERT INTO login_tokens (email, code_hash, request_ip, created_at, expires_at)
		 VALUES ($1, $2, $3::inet, $4, $5)`,
		email, hashCode(cfg.Key, email, code), nullableIP(requestIP), now, now.Add(cfg.CodeTTL)); err != nil {
		return "", fmt.Errorf("record login code: %w", err)
	}

	// Opportunistic, best-effort, and never fatal: this keeps the table bounded
	// without anything having to schedule a job, the same way play.Finish sweeps
	// unanswered questions rather than relying on one.
	_ = SweepExpired(ctx, q, now.Add(-cfg.Retention))
	return code, nil
}

// checkRequestLimits counts all four rate windows in one round trip. Every limit
// is a count over login_tokens, which is why rate limiting needs no table and no
// garbage collection of its own.
func checkRequestLimits(ctx context.Context, q db.DBTX, cfg Config, email, requestIP string, now time.Time) error {
	fastSince := now.Add(-cfg.FastWindow)
	daySince := now.Add(-24 * time.Hour)
	hourSince := now.Add(-time.Hour)

	var perEmailFast, perEmailDay, failsPerHour, perIP int
	err := q.QueryRow(ctx,
		`SELECT count(*) FILTER (WHERE email = $1 AND created_at > $3),
		        count(*) FILTER (WHERE email = $1 AND created_at > $4),
		        coalesce(sum(attempts) FILTER (WHERE email = $1 AND created_at > $5), 0),
		        count(*) FILTER (WHERE $2::inet IS NOT NULL AND request_ip = $2::inet AND created_at > $5)
		   FROM login_tokens
		  WHERE created_at > least($3, $4, $5)`,
		email, nullableIP(requestIP), fastSince, daySince, hourSince,
	).Scan(&perEmailFast, &perEmailDay, &failsPerHour, &perIP)
	if err != nil {
		return fmt.Errorf("check login code limits: %w", err)
	}

	switch {
	case perEmailFast >= cfg.MaxPerEmailFast:
		return fmt.Errorf("%w: %d codes for this address within %s", ErrTooManyRequests, perEmailFast, cfg.FastWindow)
	case perEmailDay >= cfg.MaxPerEmailDay:
		return fmt.Errorf("%w: %d codes for this address within 24h", ErrTooManyRequests, perEmailDay)
	case failsPerHour >= cfg.MaxFailsPerHour:
		// Without this, an attacker refreshes their guess budget forever by
		// simply asking for another code.
		return fmt.Errorf("%w: %d failed guesses for this address within an hour", ErrTooManyRequests, failsPerHour)
	case requestIP != "" && perIP >= cfg.MaxPerIP:
		return fmt.Errorf("%w: %d codes within an hour", ErrTooManyRequestsFromIP, perIP)
	}
	return nil
}

// VerifyCode consumes the newest live code for email and returns the user,
// creating one if this address has never signed in before.
func VerifyCode(ctx context.Context, q db.DBTX, cfg Config, email, code string, now time.Time) (User, error) {
	cfg = cfg.withDefaults()
	email, err := NormalizeEmail(email)
	if err != nil {
		return User{}, err
	}
	// Format first, so a code that cannot possibly match never costs a database
	// round trip or one of the five attempts.
	code, err = parseCode(code)
	if err != nil {
		return User{}, err
	}

	// One statement, because db.DBTX offers no transaction of its own: it has to
	// pick the row, compare, and record the outcome atomically or two concurrent
	// guesses could each see attempts=4 and both spend it.
	//
	// Deliberately a single UPDATE with CASE arms rather than two data-modifying
	// CTEs. Two CTEs updating the same row in one statement is precisely the
	// pattern Postgres documents as unpredictable.
	//
	// The hash comparison happens in SQL and is therefore not constant time.
	// That is fine: a timing oracle here leaks a prefix of an HMAC output, and
	// there is no way back from that to the code. The comparison that does need
	// constant time is the cookie signature, and that one uses hmac.Equal.
	var ok bool
	var expiresAt time.Time
	var attempts int
	err = q.QueryRow(ctx,
		`UPDATE login_tokens t
		    SET consumed_at = CASE WHEN t.code_hash = $2 AND t.expires_at > $3 AND t.attempts < $4
		                           THEN $3 ELSE NULL END,
		        attempts    = CASE WHEN t.code_hash = $2 AND t.expires_at > $3 AND t.attempts < $4
		                           THEN t.attempts ELSE t.attempts + 1 END
		  WHERE t.id = (SELECT id FROM login_tokens
		                 WHERE email = $1 AND consumed_at IS NULL
		                 ORDER BY created_at DESC, id DESC
		                 LIMIT 1)
		RETURNING consumed_at IS NOT NULL, expires_at, attempts`,
		email, hashCode(cfg.Key, email, code), now, cfg.MaxAttempts,
	).Scan(&ok, &expiresAt, &attempts)

	switch {
	case errors.Is(err, pgx.ErrNoRows):
		// No live code at all. Reported as a plain miss so a caller cannot use
		// this endpoint to discover whether a code is outstanding for an address
		// they do not control.
		return User{}, ErrCodeIncorrect
	case err != nil:
		return User{}, fmt.Errorf("verify login code: %w", err)
	case ok:
		return upsertUser(ctx, q, email, now)
	case attempts >= cfg.MaxAttempts:
		return User{}, ErrTooManyCodeAttempts
	case !expiresAt.After(now):
		return User{}, ErrCodeExpired
	default:
		return User{}, ErrCodeIncorrect
	}
}

// upsertUser returns the account for email, creating it on first sign-in.
//
// The no-op DO UPDATE is the same trick play.Start uses to get a RETURNING row
// whether the insert landed or conflicted.
func upsertUser(ctx context.Context, q db.DBTX, email string, now time.Time) (User, error) {
	var u User
	err := q.QueryRow(ctx,
		`INSERT INTO users (email, created_at) VALUES ($1, $2)
		 ON CONFLICT (email) DO UPDATE SET email = EXCLUDED.email
		 RETURNING id, email, created_at`,
		email, now).Scan(&u.ID, &u.Email, &u.CreatedAt)
	if err != nil {
		return User{}, fmt.Errorf("upsert user: %w", err)
	}
	return u, nil
}

// Attach claims a browser for a user. Idempotent.
//
// Nothing is moved or deleted: the player's runs stay the player's, and the user
// simply gains another browser. Whether a browser already claimed by someone
// else may be re-pointed is a policy question, and it lives in the HTTP layer.
func Attach(ctx context.Context, q db.DBTX, playerID string, userID int64) error {
	if _, err := q.Exec(ctx, `UPDATE players SET user_id=$2 WHERE id=$1`, playerID, userID); err != nil {
		return fmt.Errorf("attach player to user: %w", err)
	}
	return nil
}

// ForPlayer returns the user a player belongs to, or nil for an anonymous one.
func ForPlayer(ctx context.Context, q db.DBTX, playerID string) (*User, error) {
	var u User
	err := q.QueryRow(ctx,
		`SELECT u.id, u.email, u.created_at
		   FROM players p JOIN users u ON u.id = p.user_id
		  WHERE p.id = $1`, playerID).Scan(&u.ID, &u.Email, &u.CreatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("look up player's user: %w", err)
	}
	return &u, nil
}

// PlayerIDs lists every browser belonging to a user.
func PlayerIDs(ctx context.Context, q db.DBTX, userID int64) ([]string, error) {
	rows, err := q.Query(ctx, `SELECT id FROM players WHERE user_id=$1 ORDER BY created_at, id`, userID)
	if err != nil {
		return nil, fmt.Errorf("list a user's players: %w", err)
	}
	defer rows.Close()

	var ids []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		ids = append(ids, id)
	}
	return ids, rows.Err()
}

// SweepExpired deletes login codes created before the cutoff.
//
// The cutoff must be older than the longest rate window, or sweeping would
// refund an attacker's budget.
func SweepExpired(ctx context.Context, q db.DBTX, before time.Time) error {
	if _, err := q.Exec(ctx, `DELETE FROM login_tokens WHERE created_at < $1`, before); err != nil {
		return fmt.Errorf("sweep expired login codes: %w", err)
	}
	return nil
}

// nullableIP turns an empty string into a SQL NULL, so the inet comparisons in
// the limit query fall away rather than matching every row with no IP.
func nullableIP(ip string) *string {
	if ip == "" {
		return nil
	}
	return &ip
}

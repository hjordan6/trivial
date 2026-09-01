package accounts_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/hjordan6/trivial/internal/accounts"
	"github.com/hjordan6/trivial/internal/testsupport"
)

var now = time.Date(2026, 8, 23, 18, 0, 0, 0, time.UTC)

func config() accounts.Config {
	return accounts.Config{Key: []byte("a test signing key")}
}

func setup(t *testing.T) (context.Context, pgx.Tx) {
	t.Helper()
	return context.Background(), testsupport.Tx(t, testsupport.MustPool(t))
}

// player inserts a bare player row, standing in for a browser.
func player(t *testing.T, ctx context.Context, tx pgx.Tx) string {
	t.Helper()
	var id string
	if err := tx.QueryRow(ctx, `INSERT INTO players(created_at,last_seen_at) VALUES($1,$1) RETURNING id`, now).Scan(&id); err != nil {
		t.Fatal(err)
	}
	return id
}

func TestRequestThenVerifyCreatesAUser(t *testing.T) {
	ctx, tx := setup(t)

	code, err := accounts.RequestCode(ctx, tx, config(), "player@example.com", "203.0.113.5", now)
	if err != nil {
		t.Fatalf("RequestCode() error = %v", err)
	}
	user, err := accounts.VerifyCode(ctx, tx, config(), "player@example.com", code, now.Add(time.Minute))
	if err != nil {
		t.Fatalf("VerifyCode() error = %v", err)
	}
	if user.ID == 0 {
		t.Error("user id = 0, want a real id")
	}
	if user.Email != "player@example.com" {
		t.Errorf("email = %q, want player@example.com", user.Email)
	}
}

// Requesting a code must not create a user: a mistyped address should never
// become an account, and request-code must not be able to tell a caller whether
// an address is known.
func TestRequestCodeCreatesNoUser(t *testing.T) {
	ctx, tx := setup(t)

	if _, err := accounts.RequestCode(ctx, tx, config(), "typo@example.com", "", now); err != nil {
		t.Fatal(err)
	}
	var count int
	if err := tx.QueryRow(ctx, `SELECT count(*) FROM users`).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 0 {
		t.Errorf("users rows = %d, want 0", count)
	}
}

func TestRequestCodeRejectsABadAddress(t *testing.T) {
	ctx, tx := setup(t)
	if _, err := accounts.RequestCode(ctx, tx, config(), "not-an-email", "", now); !errors.Is(err, accounts.ErrInvalidEmail) {
		t.Fatalf("error = %v, want ErrInvalidEmail", err)
	}
}

// The address is normalized on both sides, so the case a player types on their
// phone cannot strand them from the account they made on a laptop.
func TestVerifyIsCaseInsensitiveAndReusesTheUser(t *testing.T) {
	ctx, tx := setup(t)

	code, err := accounts.RequestCode(ctx, tx, config(), "Player@Example.COM", "", now)
	if err != nil {
		t.Fatal(err)
	}
	first, err := accounts.VerifyCode(ctx, tx, config(), "  player@example.com ", code, now)
	if err != nil {
		t.Fatalf("VerifyCode() error = %v", err)
	}

	code2, err := accounts.RequestCode(ctx, tx, config(), "PLAYER@EXAMPLE.COM", "", now.Add(time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	second, err := accounts.VerifyCode(ctx, tx, config(), "player@example.com", code2, now.Add(time.Hour))
	if err != nil {
		t.Fatalf("VerifyCode() error = %v", err)
	}
	if first.ID != second.ID {
		t.Errorf("user ids %d and %d differ; the same address made two accounts", first.ID, second.ID)
	}
	var count int
	if err := tx.QueryRow(ctx, `SELECT count(*) FROM users`).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 1 {
		t.Errorf("users rows = %d, want 1", count)
	}
}

func TestVerifyIsSingleUse(t *testing.T) {
	ctx, tx := setup(t)

	code, err := accounts.RequestCode(ctx, tx, config(), "player@example.com", "", now)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := accounts.VerifyCode(ctx, tx, config(), "player@example.com", code, now); err != nil {
		t.Fatal(err)
	}
	_, err = accounts.VerifyCode(ctx, tx, config(), "player@example.com", code, now)
	if !errors.Is(err, accounts.ErrCodeIncorrect) {
		t.Fatalf("replaying a consumed code: error = %v, want ErrCodeIncorrect", err)
	}
}

func TestVerifyRejectsAnExpiredCode(t *testing.T) {
	ctx, tx := setup(t)
	cfg := config()
	cfg.CodeTTL = 10 * time.Minute

	code, err := accounts.RequestCode(ctx, tx, cfg, "player@example.com", "", now)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := accounts.VerifyCode(ctx, tx, cfg, "player@example.com", code, now.Add(11*time.Minute)); !errors.Is(err, accounts.ErrCodeExpired) {
		t.Fatalf("error = %v, want ErrCodeExpired", err)
	}
}

func TestVerifyWithNoLiveCode(t *testing.T) {
	ctx, tx := setup(t)
	if _, err := accounts.VerifyCode(ctx, tx, config(), "stranger@example.com", "048221", now); !errors.Is(err, accounts.ErrCodeIncorrect) {
		t.Fatalf("error = %v, want ErrCodeIncorrect", err)
	}
}

func TestVerifyRejectsAMalformedCodeWithoutSpendingAnAttempt(t *testing.T) {
	ctx, tx := setup(t)

	if _, err := accounts.RequestCode(ctx, tx, config(), "player@example.com", "", now); err != nil {
		t.Fatal(err)
	}
	if _, err := accounts.VerifyCode(ctx, tx, config(), "player@example.com", "abc", now); !errors.Is(err, accounts.ErrInvalidCode) {
		t.Fatalf("error = %v, want ErrInvalidCode", err)
	}
	var attempts int
	if err := tx.QueryRow(ctx, `SELECT attempts FROM login_tokens WHERE email=$1`, "player@example.com").Scan(&attempts); err != nil {
		t.Fatal(err)
	}
	if attempts != 0 {
		t.Errorf("attempts = %d, want 0: a code that cannot possibly match should not cost a guess", attempts)
	}
}

// TestVerifyLocksAfterMaxAttempts is the load-bearing security test: six digits
// is only safe because the budget runs out, and it has to run out even for
// someone who then produces the right code.
func TestVerifyLocksAfterMaxAttempts(t *testing.T) {
	ctx, tx := setup(t)
	cfg := config()
	cfg.MaxAttempts = 3

	code, err := accounts.RequestCode(ctx, tx, cfg, "player@example.com", "", now)
	if err != nil {
		t.Fatal(err)
	}
	wrong := "000000"
	if wrong == code {
		wrong = "111111"
	}

	for i := 1; i <= cfg.MaxAttempts; i++ {
		_, err := accounts.VerifyCode(ctx, tx, cfg, "player@example.com", wrong, now)
		want := accounts.ErrCodeIncorrect
		if i == cfg.MaxAttempts {
			// The budget is spent by this guess, so the last wrong answer
			// reports the lockout rather than a plain miss.
			want = accounts.ErrTooManyCodeAttempts
		}
		if !errors.Is(err, want) {
			t.Fatalf("attempt %d: error = %v, want %v", i, err, want)
		}
	}
	if _, err := accounts.VerifyCode(ctx, tx, cfg, "player@example.com", code, now); !errors.Is(err, accounts.ErrTooManyCodeAttempts) {
		t.Fatalf("the correct code after the budget was spent: error = %v, want ErrTooManyCodeAttempts", err)
	}
}

// Requesting a second code invalidates the first. This is what keeps the
// attempt budget non-cumulative, and it is why the failure message has to
// mention expiry.
func TestNewerCodeSupersedesTheOlder(t *testing.T) {
	ctx, tx := setup(t)

	first, err := accounts.RequestCode(ctx, tx, config(), "player@example.com", "", now)
	if err != nil {
		t.Fatal(err)
	}
	second, err := accounts.RequestCode(ctx, tx, config(), "player@example.com", "", now.Add(time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	if first == second {
		t.Skip("the two draws collided; nothing to assert")
	}
	if _, err := accounts.VerifyCode(ctx, tx, config(), "player@example.com", first, now.Add(2*time.Minute)); !errors.Is(err, accounts.ErrCodeIncorrect) {
		t.Fatalf("the superseded code: error = %v, want ErrCodeIncorrect", err)
	}
	if _, err := accounts.VerifyCode(ctx, tx, config(), "player@example.com", second, now.Add(2*time.Minute)); err != nil {
		t.Fatalf("the newest code: error = %v, want success", err)
	}
}

func TestRequestCodeRateLimits(t *testing.T) {
	tests := []struct {
		name string
		// tune narrows one limit to 2 and leaves the others wide.
		tune func(*accounts.Config)
		// gap is how far apart the requests are spaced.
		gap time.Duration
		// ip is passed to every request.
		ip string
		// sameEmail requests all use one address when true.
		sameEmail bool
	}{
		{
			name:      "per email in the fast window",
			tune:      func(c *accounts.Config) { c.MaxPerEmailFast = 2; c.FastWindow = time.Hour },
			gap:       time.Minute,
			sameEmail: true,
		},
		{
			name:      "per email per day",
			tune:      func(c *accounts.Config) { c.MaxPerEmailDay = 2; c.MaxPerEmailFast = 99 },
			gap:       2 * time.Hour,
			sameEmail: true,
		},
		{
			// A different address each time, so only the IP limit can bite.
			name: "per ip",
			tune: func(c *accounts.Config) { c.MaxPerIP = 2 },
			gap:  time.Minute,
			ip:   "203.0.113.5",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx, tx := setup(t)
			cfg := config()
			cfg.MaxPerEmailFast, cfg.MaxPerEmailDay, cfg.MaxPerIP = 99, 99, 99
			tt.tune(&cfg)

			at := now
			for i := 0; i < 2; i++ {
				email := "player@example.com"
				if !tt.sameEmail {
					email = string(rune('a'+i)) + "-player@example.com"
				}
				if _, err := accounts.RequestCode(ctx, tx, cfg, email, tt.ip, at); err != nil {
					t.Fatalf("request %d: error = %v, want success", i+1, err)
				}
				at = at.Add(tt.gap)
			}
			email := "player@example.com"
			if !tt.sameEmail {
				email = "z-player@example.com"
			}
			if _, err := accounts.RequestCode(ctx, tx, cfg, email, tt.ip, at); !errors.Is(err, accounts.ErrTooManyRequests) {
				t.Fatalf("the third request: error = %v, want ErrTooManyRequests", err)
			}
		})
	}
}

// Once an address has been ground down with failed guesses, requesting a fresh
// code has to stop too, or the attempt budget resets forever.
func TestRequestCodeStopsAfterTooManyFailedGuesses(t *testing.T) {
	ctx, tx := setup(t)
	cfg := config()
	cfg.MaxAttempts = 5
	cfg.MaxFailsPerHour = 2

	code, err := accounts.RequestCode(ctx, tx, cfg, "player@example.com", "", now)
	if err != nil {
		t.Fatal(err)
	}
	wrong := "000000"
	if wrong == code {
		wrong = "111111"
	}
	for i := 0; i < 2; i++ {
		if _, err := accounts.VerifyCode(ctx, tx, cfg, "player@example.com", wrong, now); !errors.Is(err, accounts.ErrCodeIncorrect) {
			t.Fatalf("guess %d: error = %v", i+1, err)
		}
	}
	if _, err := accounts.RequestCode(ctx, tx, cfg, "player@example.com", "", now.Add(time.Minute)); !errors.Is(err, accounts.ErrTooManyRequests) {
		t.Fatalf("error = %v, want ErrTooManyRequests", err)
	}
}

func TestAttachAndForPlayer(t *testing.T) {
	ctx, tx := setup(t)
	p := player(t, ctx, tx)

	if got, err := accounts.ForPlayer(ctx, tx, p); err != nil || got != nil {
		t.Fatalf("ForPlayer() on an anonymous player = (%v, %v), want (nil, nil)", got, err)
	}

	code, err := accounts.RequestCode(ctx, tx, config(), "player@example.com", "", now)
	if err != nil {
		t.Fatal(err)
	}
	user, err := accounts.VerifyCode(ctx, tx, config(), "player@example.com", code, now)
	if err != nil {
		t.Fatal(err)
	}
	if err := accounts.Attach(ctx, tx, p, user.ID); err != nil {
		t.Fatalf("Attach() error = %v", err)
	}
	// Attaching twice is how a re-sign-in on the same browser behaves.
	if err := accounts.Attach(ctx, tx, p, user.ID); err != nil {
		t.Fatalf("Attach() second call error = %v", err)
	}

	got, err := accounts.ForPlayer(ctx, tx, p)
	if err != nil {
		t.Fatal(err)
	}
	if got == nil || got.ID != user.ID || got.Email != "player@example.com" {
		t.Fatalf("ForPlayer() = %+v, want the attached user", got)
	}

	ids, err := accounts.PlayerIDs(ctx, tx, user.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(ids) != 1 || ids[0] != p {
		t.Errorf("PlayerIDs() = %v, want [%s]", ids, p)
	}
}

// A user accumulates browsers. This is the whole point of attach-not-migrate.
func TestOneUserAccumulatesPlayers(t *testing.T) {
	ctx, tx := setup(t)
	laptop, phone := player(t, ctx, tx), player(t, ctx, tx)

	code, err := accounts.RequestCode(ctx, tx, config(), "player@example.com", "", now)
	if err != nil {
		t.Fatal(err)
	}
	user, err := accounts.VerifyCode(ctx, tx, config(), "player@example.com", code, now)
	if err != nil {
		t.Fatal(err)
	}
	for _, p := range []string{laptop, phone} {
		if err := accounts.Attach(ctx, tx, p, user.ID); err != nil {
			t.Fatal(err)
		}
	}
	ids, err := accounts.PlayerIDs(ctx, tx, user.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(ids) != 2 {
		t.Fatalf("PlayerIDs() = %v, want both browsers", ids)
	}
}

// ON DELETE SET NULL, not CASCADE. Runs belong to the player, so deleting an
// account must detach browsers and leave every run standing.
func TestDeletingAUserKeepsPlayersAndRuns(t *testing.T) {
	ctx, tx := setup(t)
	p := player(t, ctx, tx)

	if _, err := tx.Exec(ctx, `INSERT INTO daily_puzzles(puzzle_date) VALUES('2026-08-23')`); err != nil {
		t.Fatal(err)
	}
	if _, err := tx.Exec(ctx,
		`INSERT INTO runs(player_id,puzzle_date,started_at,expires_at,option_seed) VALUES($1,'2026-08-23',$2,$3,1)`,
		p, now, now.Add(time.Minute)); err != nil {
		t.Fatal(err)
	}

	code, err := accounts.RequestCode(ctx, tx, config(), "player@example.com", "", now)
	if err != nil {
		t.Fatal(err)
	}
	user, err := accounts.VerifyCode(ctx, tx, config(), "player@example.com", code, now)
	if err != nil {
		t.Fatal(err)
	}
	if err := accounts.Attach(ctx, tx, p, user.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := tx.Exec(ctx, `DELETE FROM users WHERE id=$1`, user.ID); err != nil {
		t.Fatalf("deleting the user: %v", err)
	}

	var userID *int64
	if err := tx.QueryRow(ctx, `SELECT user_id FROM players WHERE id=$1`, p).Scan(&userID); err != nil {
		t.Fatalf("the player row did not survive: %v", err)
	}
	if userID != nil {
		t.Errorf("user_id = %d, want NULL", *userID)
	}
	var runs int
	if err := tx.QueryRow(ctx, `SELECT count(*) FROM runs WHERE player_id=$1`, p).Scan(&runs); err != nil {
		t.Fatal(err)
	}
	if runs != 1 {
		t.Errorf("runs = %d, want 1: deleting an account must not delete history", runs)
	}
}

// The sweep must never reach back into a live rate window, or an attacker gets
// their budget refunded on a schedule.
func TestSweepExpiredKeepsTheRateLimitWindow(t *testing.T) {
	ctx, tx := setup(t)
	cfg := config()

	// Retention defaults to 7 days, comfortably longer than the longest rate
	// window, which is what makes the sweep safe to run opportunistically.
	if _, err := accounts.RequestCode(ctx, tx, cfg, "old@example.com", "", now.Add(-30*24*time.Hour)); err != nil {
		t.Fatal(err)
	}
	if _, err := accounts.RequestCode(ctx, tx, cfg, "recent@example.com", "", now.Add(-time.Hour)); err != nil {
		t.Fatal(err)
	}
	if err := accounts.SweepExpired(ctx, tx, now.Add(-7*24*time.Hour)); err != nil {
		t.Fatalf("SweepExpired() error = %v", err)
	}

	var emails []string
	rows, err := tx.Query(ctx, `SELECT email FROM login_tokens ORDER BY email`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	for rows.Next() {
		var e string
		if err := rows.Scan(&e); err != nil {
			t.Fatal(err)
		}
		emails = append(emails, e)
	}
	if len(emails) != 1 || emails[0] != "recent@example.com" {
		t.Errorf("remaining rows = %v, want only recent@example.com", emails)
	}
}

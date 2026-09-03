package friends_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/hjordan6/trivial/internal/friends"
	"github.com/hjordan6/trivial/internal/testsupport"
)

var now = time.Date(2026, 9, 2, 18, 0, 0, 0, time.UTC)

func setup(t *testing.T) (context.Context, pgx.Tx) {
	t.Helper()
	return context.Background(), testsupport.Tx(t, testsupport.MustPool(t))
}

// user inserts a bare user row and returns its id. Every test runs inside a
// transaction that is rolled back, so the addresses need not be unique across
// the suite -- only within one test.
func user(t *testing.T, ctx context.Context, tx pgx.Tx, email string) int64 {
	t.Helper()
	var id int64
	if err := tx.QueryRow(ctx,
		`INSERT INTO users(email, created_at) VALUES($1, $2) RETURNING id`,
		email, now).Scan(&id); err != nil {
		t.Fatal(err)
	}
	return id
}

func TestUpsertInviteCreatesALink(t *testing.T) {
	ctx, tx := setup(t)
	uid := user(t, ctx, tx, "sender@example.com")

	inv, err := friends.UpsertInvite(ctx, tx, uid, "sender", "tok-one", now)
	if err != nil {
		t.Fatalf("UpsertInvite() error = %v", err)
	}
	if inv.Token != "tok-one" {
		t.Errorf("token = %q, want tok-one", inv.Token)
	}
	if inv.UserID != uid {
		t.Errorf("user id = %d, want %d", inv.UserID, uid)
	}
	if inv.Nickname != "sender" {
		t.Errorf("nickname = %q, want sender", inv.Nickname)
	}
}

// The link is reusable and stable: pressing the button again renames the sender
// but must not mint a second token, or a link already sent to somebody would go
// dead.
func TestUpsertInviteKeepsTheFirstTokenAndTakesTheNewName(t *testing.T) {
	ctx, tx := setup(t)
	uid := user(t, ctx, tx, "sender@example.com")

	first, err := friends.UpsertInvite(ctx, tx, uid, "sender", "tok-one", now)
	if err != nil {
		t.Fatal(err)
	}
	second, err := friends.UpsertInvite(ctx, tx, uid, "Sam", "tok-two", now.Add(time.Hour))
	if err != nil {
		t.Fatalf("second UpsertInvite() error = %v", err)
	}
	if second.Token != first.Token {
		t.Errorf("token = %q, want the original %q", second.Token, first.Token)
	}
	if second.Nickname != "Sam" {
		t.Errorf("nickname = %q, want Sam", second.Nickname)
	}

	var count int
	if err := tx.QueryRow(ctx, `SELECT count(*) FROM friend_invites WHERE user_id=$1`, uid).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 1 {
		t.Errorf("invite rows = %d, want 1", count)
	}
}

func TestUpsertInviteRejectsABlankNickname(t *testing.T) {
	ctx, tx := setup(t)
	uid := user(t, ctx, tx, "sender@example.com")

	if _, err := friends.UpsertInvite(ctx, tx, uid, "   ", "tok-one", now); !errors.Is(err, friends.ErrEmptyNickname) {
		t.Errorf("error = %v, want ErrEmptyNickname", err)
	}
}

func TestInviteByTokenReadsItBack(t *testing.T) {
	ctx, tx := setup(t)
	uid := user(t, ctx, tx, "sender@example.com")
	if _, err := friends.UpsertInvite(ctx, tx, uid, "Sam", "tok-one", now); err != nil {
		t.Fatal(err)
	}

	inv, err := friends.InviteByToken(ctx, tx, "tok-one")
	if err != nil {
		t.Fatalf("InviteByToken() error = %v", err)
	}
	if inv.Nickname != "Sam" || inv.UserID != uid {
		t.Errorf("invite = %+v, want Sam and user %d", inv, uid)
	}
}

func TestInviteByTokenRejectsAnUnknownToken(t *testing.T) {
	ctx, tx := setup(t)

	if _, err := friends.InviteByToken(ctx, tx, "nope"); !errors.Is(err, friends.ErrNoInvite) {
		t.Errorf("error = %v, want ErrNoInvite", err)
	}
}

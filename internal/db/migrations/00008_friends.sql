-- +goose Up

-- A sender's reusable friend link.
--
-- UNIQUE (user_id) is what makes the link stable: minting is an upsert that
-- returns the token already on the row and only updates the nickname, so
-- pressing the button a second time yields the same URL and a link already
-- sent to somebody never goes dead.
--
-- The token is a bearer credential -- anyone holding it can befriend this user,
-- and it does not expire. It lives in its own column rather than being derived
-- from a signed cookie value precisely so it can be rotated with one UPDATE if
-- a revoke feature is ever wanted; deriving it would tie rotation to APP_SECRET
-- and sign every player out.
--
-- nickname is NOT NULL here, unlike share_tokens.nickname. A share can
-- legitimately be anonymous; an invite cannot, because the nickname is the only
-- thing the landing page has to say who is asking. The length CHECK matches
-- maxNicknameRunes in internal/httpapi.
CREATE TABLE friend_invites (
    token      text PRIMARY KEY,
    user_id    bigint NOT NULL UNIQUE REFERENCES users (id) ON DELETE CASCADE,
    nickname   text NOT NULL,
    created_at timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT friend_invites_nickname_length CHECK (length(nickname) BETWEEN 1 AND 40)
);

-- One row per friendship, not two.
--
-- CHECK (user_low < user_high) forces canonical order, and the primary key then
-- buys three properties outright: no duplicates, no direction-dependent
-- duplicates, and no way to record a half-friendship where A is B's friend but
-- not the reverse. Inserting with least()/greatest() and ON CONFLICT DO NOTHING
-- therefore makes accepting idempotent without a uniqueness check in Go.
--
-- The strict < also makes self-friendship unrepresentable. The HTTP layer
-- rejects it a step earlier so the player reads a sentence rather than a 500.
--
-- ON DELETE CASCADE, deliberately unlike players.user_id, which is SET NULL.
-- That column is SET NULL because runs belong to the player and must outlive
-- the account; a friendship has no meaning once either user is gone.
CREATE TABLE friendships (
    user_low   bigint NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    user_high  bigint NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    created_at timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (user_low, user_high),
    CONSTRAINT friendships_ordered CHECK (user_low < user_high)
);

-- The primary key orders on user_low and therefore serves only half the
-- lookups. Nothing reads this yet; the first thing that lists a person's
-- friends needs it, and it costs nothing on an empty table.
CREATE INDEX friendships_high_idx ON friendships (user_high);

-- +goose Down
DROP TABLE friendships;
DROP TABLE friend_invites;

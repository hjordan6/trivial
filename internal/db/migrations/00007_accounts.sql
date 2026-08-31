-- +goose Up

-- A user is a person; a player stays a browser. Signing in attaches a player
-- row to a user, so a user accumulates players over time and nothing is ever
-- merged or destroyed. That is what makes signing in on a second device the
-- same operation as the first, and what keeps every past run addressable.
--
-- email is plain text rather than citext: NormalizeEmail in internal/accounts
-- is the only writer, the CHECK below catches any path that forgets, and the
-- lower(email) index catches it even then. Same guarantee without needing an
-- extension, which this schema otherwise has none of -- gen_random_uuid() in
-- 00002 is built in, so nothing here has ever required contrib.
CREATE TABLE users (
    id         bigserial PRIMARY KEY,
    email      text NOT NULL,
    created_at timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT users_email_normalized CHECK (email = lower(btrim(email))),
    CONSTRAINT users_email_length CHECK (length(email) BETWEEN 6 AND 254)
);

CREATE UNIQUE INDEX users_email_key ON users (email);

-- Second line of defence. An unnormalized address that somehow reached the
-- table still cannot duplicate an existing account.
CREATE UNIQUE INDEX users_email_lower_key ON users (lower(email));

-- The column the original design described but 00002_play.sql never created.
--
-- ON DELETE SET NULL, never CASCADE: deleting a user must detach their
-- browsers, not delete the runs those browsers played. Runs belong to the
-- player, so a cascade here would destroy history that outlives the account.
ALTER TABLE players
    ADD COLUMN user_id bigint REFERENCES users (id) ON DELETE SET NULL;

CREATE INDEX players_user_id_idx ON players (user_id) WHERE user_id IS NOT NULL;

-- A six-digit sign-in code.
--
-- The identity is a surrogate id, not the hash: six digits collide in a 10^6
-- space, and one address legitimately has several rows over time, so the hash
-- cannot be a key.
--
-- code_hash is HMAC-SHA256(APP_SECRET, email || 0x00 || code), not a plain
-- digest. A plain digest of a six-digit code is 10^6 hash evaluations from
-- plaintext -- milliseconds -- so it would protect nothing in a database dump.
-- Binding the email into the MAC also stops a hash captured for one address
-- being replayed against another.
--
-- There is deliberately no foreign key to users: a code is requested before any
-- user exists, and most rows for an address that never finishes signing in will
-- never have one. A mistyped address therefore lives here until the retention
-- sweep and never becomes an account.
--
-- attempts is the brute-force budget, and it is the whole defence -- six digits
-- is only ~20 bits. Because only the newest unconsumed row for an address is
-- ever checkable, guesses cannot be spread across several live codes, so this
-- column bounds an attack without a separate lockout table.
--
-- request_ip is what the per-IP request limiter counts, which is why rate
-- limiting needs no storage of its own: every window is a count over this table.
CREATE TABLE login_tokens (
    id          bigserial PRIMARY KEY,
    email       text NOT NULL,
    code_hash   bytea NOT NULL,
    request_ip  inet,
    attempts    smallint NOT NULL DEFAULT 0 CHECK (attempts >= 0),
    created_at  timestamptz NOT NULL DEFAULT now(),
    expires_at  timestamptz NOT NULL,
    consumed_at timestamptz,
    CONSTRAINT login_tokens_email_normalized CHECK (email = lower(btrim(email))),
    CONSTRAINT login_tokens_expires_after_creation CHECK (expires_at > created_at)
);

-- Serves the newest-live-code lookup and both per-email rate windows.
CREATE INDEX login_tokens_email_created_idx ON login_tokens (email, created_at DESC);
CREATE INDEX login_tokens_ip_created_idx
    ON login_tokens (request_ip, created_at DESC) WHERE request_ip IS NOT NULL;
-- Serves the retention sweep.
CREATE INDEX login_tokens_created_at_idx ON login_tokens (created_at);

-- +goose Down
DROP TABLE login_tokens;
DROP INDEX players_user_id_idx;
ALTER TABLE players DROP COLUMN user_id;
DROP TABLE users;

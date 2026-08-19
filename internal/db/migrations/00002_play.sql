-- +goose Up
CREATE TYPE answer_stage AS ENUM ('free_text', 'multiple_choice');
CREATE TYPE answer_outcome AS ENUM ('star', 'circle', 'miss', 'expired');

CREATE TABLE players (
    id           uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    created_at   timestamptz NOT NULL DEFAULT now(),
    last_seen_at timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE runs (
    id                 uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    player_id          uuid NOT NULL REFERENCES players (id) ON DELETE CASCADE,
    puzzle_date        date NOT NULL REFERENCES daily_puzzles (puzzle_date) ON DELETE RESTRICT,
    started_at         timestamptz NOT NULL,
    expires_at         timestamptz NOT NULL,
    completed_at       timestamptz,
    option_seed        bigint NOT NULL,
    referred_by_run_id uuid REFERENCES runs (id) ON DELETE SET NULL,
    created_at         timestamptz NOT NULL DEFAULT now(),
    UNIQUE (player_id, puzzle_date)
);

CREATE INDEX runs_player_completed_idx ON runs (player_id, completed_at);

CREATE TABLE run_answers (
    run_id               uuid NOT NULL REFERENCES runs (id) ON DELETE CASCADE,
    question_id          bigint NOT NULL REFERENCES questions (id) ON DELETE RESTRICT,
    stage                answer_stage NOT NULL,
    free_text_submission text,
    chosen_option        text,
    outcome              answer_outcome,
    first_touched_at     timestamptz NOT NULL,
    resolved_at          timestamptz,
    PRIMARY KEY (run_id, question_id)
);

CREATE TABLE share_tokens (
    token      text PRIMARY KEY,
    run_id     uuid NOT NULL UNIQUE REFERENCES runs (id) ON DELETE CASCADE,
    nickname   text,
    view_count integer NOT NULL DEFAULT 0 CHECK (view_count >= 0),
    created_at timestamptz NOT NULL DEFAULT now()
);

-- +goose Down
DROP TABLE share_tokens;
DROP TABLE run_answers;
DROP TABLE runs;
DROP TABLE players;
DROP TYPE answer_outcome;
DROP TYPE answer_stage;

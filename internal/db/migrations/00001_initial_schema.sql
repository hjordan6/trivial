-- +goose Up
CREATE TYPE difficulty AS ENUM ('easy', 'medium', 'hard');
CREATE TYPE question_status AS ENUM ('draft', 'active', 'retired');

CREATE TABLE topics (
    id         bigserial PRIMARY KEY,
    slug       text NOT NULL UNIQUE,
    name       text NOT NULL,
    active     boolean NOT NULL DEFAULT true,
    created_at timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE questions (
    id               bigserial PRIMARY KEY,
    topic_id         bigint NOT NULL REFERENCES topics (id) ON DELETE RESTRICT,
    difficulty       difficulty NOT NULL,
    prompt           text NOT NULL,
    canonical_answer text NOT NULL,
    status           question_status NOT NULL DEFAULT 'draft',
    source           text,
    external_id      text,
    created_at       timestamptz NOT NULL DEFAULT now(),
    updated_at       timestamptz NOT NULL DEFAULT now()
);

CREATE UNIQUE INDEX questions_source_external_id_key
    ON questions (source, external_id)
    WHERE external_id IS NOT NULL;

CREATE INDEX questions_active_pool_idx
    ON questions (topic_id, difficulty)
    WHERE status = 'active';

CREATE TABLE question_aliases (
    id          bigserial PRIMARY KEY,
    question_id bigint NOT NULL REFERENCES questions (id) ON DELETE CASCADE,
    alias       text NOT NULL,
    normalized  text NOT NULL
);

CREATE UNIQUE INDEX question_aliases_normalized_key
    ON question_aliases (question_id, normalized);

CREATE TABLE question_distractors (
    id          bigserial PRIMARY KEY,
    question_id bigint NOT NULL REFERENCES questions (id) ON DELETE CASCADE,
    option_text text NOT NULL
);

CREATE UNIQUE INDEX question_distractors_option_key
    ON question_distractors (question_id, option_text);

CREATE TABLE daily_puzzles (
    puzzle_date        date PRIMARY KEY,
    time_limit_seconds integer NOT NULL DEFAULT 135 CHECK (time_limit_seconds > 0),
    created_at         timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE daily_puzzle_questions (
    puzzle_date    date NOT NULL REFERENCES daily_puzzles (puzzle_date) ON DELETE CASCADE,
    topic_id       bigint NOT NULL REFERENCES topics (id) ON DELETE RESTRICT,
    topic_position smallint NOT NULL CHECK (topic_position BETWEEN 0 AND 2),
    difficulty     difficulty NOT NULL,
    question_id    bigint NOT NULL REFERENCES questions (id) ON DELETE RESTRICT,
    PRIMARY KEY (puzzle_date, topic_id, difficulty)
);

CREATE UNIQUE INDEX daily_puzzle_questions_position_key
    ON daily_puzzle_questions (puzzle_date, topic_position, difficulty);

CREATE UNIQUE INDEX daily_puzzle_questions_once_per_day_key
    ON daily_puzzle_questions (question_id, puzzle_date);

CREATE INDEX daily_puzzle_questions_cooldown_idx
    ON daily_puzzle_questions (question_id, puzzle_date DESC);

-- +goose Down
DROP TABLE daily_puzzle_questions;
DROP TABLE daily_puzzles;
DROP TABLE question_distractors;
DROP TABLE question_aliases;
DROP TABLE questions;
DROP TABLE topics;
DROP TYPE question_status;
DROP TYPE difficulty;

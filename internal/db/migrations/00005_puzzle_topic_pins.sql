-- +goose Up

-- Operator-chosen topics for a date. This table records intent; the board in
-- daily_puzzle_questions stays the derived artifact. A date with no rows here
-- is fully automatic, which is why reverting to automatic is just a delete.
--
-- There is deliberately no foreign key to daily_puzzles: a date must be
-- pinnable before it has been generated, and daily_puzzles rows only exist
-- after generation. The cost is that pins for elapsed dates are never cascaded
-- away, which is harmless -- they are inert once the date is in the past.
CREATE TABLE puzzle_topic_pins (
    puzzle_date    date NOT NULL,
    topic_position smallint NOT NULL CHECK (topic_position BETWEEN 0 AND 2),
    topic_id       bigint NOT NULL REFERENCES topics (id) ON DELETE RESTRICT,
    created_at     timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (puzzle_date, topic_position)
);

-- Catch "same topic pinned to two slots" here, with a name attached, rather
-- than letting it surface later as a daily_puzzle_questions primary key
-- violation during generation.
CREATE UNIQUE INDEX puzzle_topic_pins_once_per_date_key
    ON puzzle_topic_pins (puzzle_date, topic_id);

-- +goose Down
DROP TABLE puzzle_topic_pins;

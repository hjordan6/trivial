-- +goose Up

-- Every typed answer the alias grader rejected and the judge then scored (see
-- internal/judge), accepted or not.
--
-- One row per question and normalized answer, which makes it three things:
--
--   * A cache. The same wrong answer to the same question is judged once, not
--     once per player who types it.
--   * An audit log. Accepted rows are answers the game added to
--     question_aliases on its own; listing them is how an operator reviews
--     what the judge has been letting in.
--   * An operator override. Deleting an accepted alias in the admin panel
--     leaves its row here, and the row stops the judge from being asked again
--     and re-adding it. Delete the row too to have it judged afresh.
--
-- The scores are kept rather than just the verdict so thresholds can be
-- re-tuned against real traffic later without calling the judge again.
CREATE TABLE judged_answers (
    question_id       bigint NOT NULL REFERENCES questions (id) ON DELETE CASCADE,
    normalized        text NOT NULL,
    -- As the first player typed it, so an accepted row reads naturally.
    submission        text NOT NULL,
    acceptable        real NOT NULL,
    names_other_thing real NOT NULL,
    too_vague         real NOT NULL,
    model             text NOT NULL,
    accepted          boolean NOT NULL,
    judged_at         timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (question_id, normalized)
);

-- +goose Down
DROP TABLE judged_answers;

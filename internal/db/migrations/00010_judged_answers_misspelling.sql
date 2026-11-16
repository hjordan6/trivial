-- +goose Up

-- The judge's fourth score: is the answer the correct one, misspelled? (See
-- internal/judge.) NULL on verdicts made before the question was asked, which
-- stay final like any other verdict -- none of the rejections among them was a
-- misspelling of the right answer.
ALTER TABLE judged_answers ADD COLUMN misspelling real;

-- +goose Down
ALTER TABLE judged_answers DROP COLUMN misspelling;

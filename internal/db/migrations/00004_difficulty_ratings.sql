-- +goose Up
ALTER TABLE questions
    ADD COLUMN difficulty_rating smallint;

UPDATE questions SET difficulty_rating = CASE difficulty
    WHEN 'easy' THEN 2
    WHEN 'medium' THEN 6
    WHEN 'hard' THEN 9
END;

ALTER TABLE questions
    ALTER COLUMN difficulty_rating SET NOT NULL,
    ADD CONSTRAINT questions_difficulty_rating_range
        CHECK (difficulty_rating BETWEEN 1 AND 10),
    ADD CONSTRAINT questions_difficulty_band_matches_rating
        CHECK (
            (difficulty = 'easy' AND difficulty_rating BETWEEN 1 AND 4) OR
            (difficulty = 'medium' AND difficulty_rating BETWEEN 5 AND 7) OR
            (difficulty = 'hard' AND difficulty_rating BETWEEN 8 AND 10)
        );

-- +goose Down
ALTER TABLE questions
    DROP CONSTRAINT questions_difficulty_band_matches_rating,
    DROP CONSTRAINT questions_difficulty_rating_range,
    DROP COLUMN difficulty_rating;

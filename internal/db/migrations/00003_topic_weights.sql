-- +goose Up
ALTER TABLE topics
    ADD COLUMN selection_weight integer NOT NULL DEFAULT 1
    CHECK (selection_weight BETWEEN 1 AND 1000);

-- +goose Down
ALTER TABLE topics DROP COLUMN selection_weight;

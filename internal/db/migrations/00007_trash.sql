-- +goose Up
-- Deleted things, kept as a JSON snapshot of their rows until the trash is
-- emptied, so they can be restored with their original ids.
CREATE TABLE trash (
    id         bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    kind       text        NOT NULL CHECK (kind IN ('service', 'kit', 'movement', 'shopping', 'item', 'vehicle', 'photo', 'bought')),
    label      text        NOT NULL,
    payload    jsonb       NOT NULL,
    deleted_at timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX trash_deleted_idx ON trash (deleted_at DESC);

-- +goose Down
DROP TABLE trash;

-- +goose Up
-- Vehicles with services or kits are archived instead of deleted, so their
-- service history and labour costs stay.
ALTER TABLE vehicles ADD COLUMN archived_at timestamptz;

-- +goose Down
ALTER TABLE vehicles DROP COLUMN archived_at;

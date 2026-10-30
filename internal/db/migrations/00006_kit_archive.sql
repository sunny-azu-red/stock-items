-- +goose Up
-- Kits with logged services are archived instead of deleted, so their
-- services keep their kit and the kit keeps its history.
ALTER TABLE service_kits ADD COLUMN archived_at timestamptz;

-- +goose Down
ALTER TABLE service_kits DROP COLUMN archived_at;

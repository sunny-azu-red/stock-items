-- +goose Up
-- A service logged without its own reading keeps the vehicle's reading at
-- the time, so km reminders count from about when it was done rather than
-- from an older service.
ALTER TABLE service_records ADD COLUMN odometer_at_log integer CHECK (odometer_at_log >= 0);

-- +goose Down
ALTER TABLE service_records DROP COLUMN odometer_at_log;

-- +goose Up
-- Whether logging the service moved the vehicle's odometer forward (NULL
-- for services logged before this column). Only a service that moved it
-- puts it back when deleted.
ALTER TABLE service_records ADD COLUMN odometer_moved boolean;

-- +goose Down
ALTER TABLE service_records DROP COLUMN odometer_moved;

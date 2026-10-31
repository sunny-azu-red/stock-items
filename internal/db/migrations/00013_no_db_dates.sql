-- +goose Up
-- Calendar days are decided in Go, in the app's zone (TZ, else the
-- machine's), never by the database: current_date is the day in the
-- session's zone, which is UTC (db.Open), so around midnight it isn't the
-- app's day. The store always sets these dates; without a default, an
-- insert that forgets one fails instead of quietly taking UTC's day.
ALTER TABLE service_records ALTER COLUMN performed_on DROP DEFAULT;
ALTER TABLE stock_movements ALTER COLUMN occurred_on DROP DEFAULT;

-- +goose Down
ALTER TABLE service_records ALTER COLUMN performed_on SET DEFAULT current_date;
ALTER TABLE stock_movements ALTER COLUMN occurred_on SET DEFAULT current_date;

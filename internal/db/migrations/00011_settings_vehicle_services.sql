-- +goose Up
-- How many services a vehicle's page lists (its Service history card; All
-- services → shows the rest): 10, as the code had. A setting like the other
-- list sizes, after them (the page's Lists section).
ALTER TABLE settings
    ADD COLUMN vehicle_services integer NOT NULL DEFAULT 10 CHECK (vehicle_services BETWEEN 1 AND 100);
SELECT reorder_columns('settings', ARRAY[
    'id', 'version',
    'currency', 'distance_unit', 'number_format', 'date_format', 'days_ago', 'dashboard_recent',
    'activity_page_size', 'services_page_size', 'item_history', 'vehicle_services',
    'sku_search_url', 'sku_search_prefix', 'expiring_days', 'categories',
    'due_soon_days', 'due_soon_percent', 'due_soon_min_distance', 'work_names',
    'warranty_soon_distance', 'warranty_soon_days', 'warranty_grace_percent', 'warranty_grace_days',
    'created_at', 'updated_at']);

-- +goose Down
ALTER TABLE settings DROP COLUMN vehicle_services;

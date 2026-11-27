-- +goose Up
-- The Lists section in the nav's order: an item's history (Stock), Services
-- per page, a vehicle's services (Vehicles), Activity per page. The page
-- shows the settings in the table's order.
SELECT reorder_columns('settings', ARRAY[
    'id', 'version',
    'currency', 'distance_unit', 'number_format', 'date_format', 'days_ago', 'dashboard_recent',
    'item_history', 'services_page_size', 'vehicle_services', 'activity_page_size',
    'sku_search_url', 'sku_search_prefix', 'expiring_days', 'categories',
    'due_soon_days', 'due_soon_percent', 'due_soon_min_distance', 'work_names',
    'warranty_soon_distance', 'warranty_soon_days', 'warranty_grace_percent', 'warranty_grace_days',
    'created_at', 'updated_at']);

-- +goose Down
SELECT reorder_columns('settings', ARRAY[
    'id', 'version',
    'currency', 'distance_unit', 'number_format', 'date_format', 'days_ago', 'dashboard_recent',
    'activity_page_size', 'services_page_size', 'item_history', 'vehicle_services',
    'sku_search_url', 'sku_search_prefix', 'expiring_days', 'categories',
    'due_soon_days', 'due_soon_percent', 'due_soon_min_distance', 'work_names',
    'warranty_soon_distance', 'warranty_soon_days', 'warranty_grace_percent', 'warranty_grace_days',
    'created_at', 'updated_at']);

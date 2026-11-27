-- +goose Up
-- The app's preferences and the rules' thresholds, set on the Settings page
-- (stock.Settings), in place of APP_* environment variables and constants
-- in the code. One row, made here with the values the app had until now, so
-- nothing changes until a setting is. The columns follow the page: units
-- and formats, lists, stock, services, warranty. A new setting is a later
-- migration's ADD COLUMN with its value as the default, then
-- reorder_columns (AGENTS.md); TestSettingsComplete checks the rest.
CREATE TABLE settings (
    id integer PRIMARY KEY DEFAULT 1 CHECK (id = 1),
    version integer NOT NULL DEFAULT 1,
    currency text NOT NULL DEFAULT 'EUR' CHECK (char_length(currency) BETWEEN 1 AND 10),
    distance_unit text NOT NULL DEFAULT 'km' CHECK (char_length(distance_unit) BETWEEN 1 AND 10),
    number_format text NOT NULL DEFAULT 'space_dot'
        CHECK (number_format IN ('space_dot', 'space_comma', 'comma_dot', 'dot_comma')),
    date_format text NOT NULL DEFAULT '2 Jan 2006'
        CHECK (date_format IN ('2 Jan 2006', '02.01.2006', '2006-01-02', '02/01/2006', '01/02/2006')),
    days_ago integer NOT NULL DEFAULT 30 CHECK (days_ago BETWEEN 0 AND 365),
    dashboard_recent integer NOT NULL DEFAULT 8 CHECK (dashboard_recent BETWEEN 1 AND 50),
    activity_page_size integer NOT NULL DEFAULT 200 CHECK (activity_page_size BETWEEN 10 AND 1000),
    services_page_size integer NOT NULL DEFAULT 100 CHECK (services_page_size BETWEEN 10 AND 1000),
    item_history integer NOT NULL DEFAULT 100 CHECK (item_history BETWEEN 10 AND 1000),
    sku_search_url text NOT NULL DEFAULT 'https://www.google.com/search?q={q}' CHECK (char_length(sku_search_url) BETWEEN 1 AND 500),
    sku_search_prefix text NOT NULL DEFAULT '' CHECK (char_length(sku_search_prefix) <= 100),
    expiring_days integer NOT NULL DEFAULT 60 CHECK (expiring_days BETWEEN 1 AND 365),
    categories text[] NOT NULL DEFAULT ARRAY['Oil & Fluids', 'Filters', 'Brakes', 'Gaskets & O-Rings', 'Chain & Sprockets',
        'Electrical', 'Tyres', 'Cleaning & Care', 'Hardware', 'Tools',
        'Maintenance Kits', 'Bearings & Seals', 'Controls & Cables', 'Suspension']
        CHECK (cardinality(categories) <= 100),
    due_soon_days integer NOT NULL DEFAULT 30 CHECK (due_soon_days BETWEEN 0 AND 365),
    due_soon_percent integer NOT NULL DEFAULT 10 CHECK (due_soon_percent BETWEEN 0 AND 50),
    due_soon_min_distance integer NOT NULL DEFAULT 500 CHECK (due_soon_min_distance BETWEEN 0 AND 100000),
    work_names text[] NOT NULL DEFAULT ARRAY['Oil Service', 'Air Filter', 'Brake Pads (Front)', 'Brake Pads (Rear)',
        'Brake Fluid', 'Cabin Filter', 'Clutch Fluid', 'Coolant',
        'Spark Plugs', 'Valve Check']
        CHECK (cardinality(work_names) <= 100),
    warranty_soon_distance integer NOT NULL DEFAULT 1000 CHECK (warranty_soon_distance BETWEEN 0 AND 100000),
    warranty_soon_days integer NOT NULL DEFAULT 30 CHECK (warranty_soon_days BETWEEN 0 AND 365),
    warranty_grace_percent integer NOT NULL DEFAULT 10 CHECK (warranty_grace_percent BETWEEN 0 AND 50),
    warranty_grace_days integer NOT NULL DEFAULT 30 CHECK (warranty_grace_days BETWEEN 0 AND 365),
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now()
);
INSERT INTO settings DEFAULT VALUES;

-- +goose Down
DROP TABLE settings;

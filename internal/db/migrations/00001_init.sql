-- +goose Up

CREATE TABLE vehicles (
    id          bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    name        text        NOT NULL,
    kind        text        NOT NULL DEFAULT 'motorcycle' CHECK (kind IN ('motorcycle', 'car', 'other')),
    make        text        NOT NULL DEFAULT '',
    model       text        NOT NULL DEFAULT '',
    year        integer     CHECK (year BETWEEN 1900 AND 2100),
    plate       text        NOT NULL DEFAULT '',
    vin         text        NOT NULL DEFAULT '',
    color       text        NOT NULL DEFAULT '#f97316' CHECK (color ~ '^#[0-9a-fA-F]{6}$'),
    odometer_km integer     CHECK (odometer_km >= 0),
    notes       text        NOT NULL DEFAULT '',
    created_at  timestamptz NOT NULL DEFAULT now(),
    updated_at  timestamptz NOT NULL DEFAULT now()
);
CREATE UNIQUE INDEX vehicles_name_key ON vehicles (lower(name));

-- Quantities are stored in the item's base unit: pieces, millilitres or grams.
CREATE TABLE items (
    id           bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    name         text          NOT NULL CHECK (name <> ''),
    brand        text          NOT NULL DEFAULT '',
    category     text          NOT NULL DEFAULT '',
    unit         text          NOT NULL DEFAULT 'piece' CHECK (unit IN ('piece', 'ml', 'g')),
    quantity     numeric(12,3) NOT NULL DEFAULT 0 CONSTRAINT items_quantity_non_negative CHECK (quantity >= 0),
    min_quantity numeric(12,3) CHECK (min_quantity >= 0),
    expires_on   date,
    location     text          NOT NULL DEFAULT '',
    notes        text          NOT NULL DEFAULT '',
    created_at   timestamptz   NOT NULL DEFAULT now(),
    updated_at   timestamptz   NOT NULL DEFAULT now()
);
CREATE INDEX items_name_idx ON items (lower(name));

CREATE TABLE item_skus (
    id        bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    item_id   bigint        NOT NULL REFERENCES items (id) ON DELETE CASCADE,
    code      text          NOT NULL CHECK (code <> ''),
    kind      text          NOT NULL DEFAULT 'oem' CHECK (kind IN ('oem', 'manufacturer', 'retailer', 'other')),
    label     text          NOT NULL DEFAULT '',
    pack_size numeric(12,3) CHECK (pack_size > 0),
    UNIQUE (item_id, code)
);
CREATE INDEX item_skus_code_idx ON item_skus (lower(code));

CREATE TABLE item_vehicles (
    item_id    bigint NOT NULL REFERENCES items (id) ON DELETE CASCADE,
    vehicle_id bigint NOT NULL REFERENCES vehicles (id) ON DELETE CASCADE,
    PRIMARY KEY (item_id, vehicle_id)
);
CREATE INDEX item_vehicles_vehicle_idx ON item_vehicles (vehicle_id);

CREATE TABLE item_photos (
    item_id      bigint      PRIMARY KEY REFERENCES items (id) ON DELETE CASCADE,
    content_type text        NOT NULL,
    image        bytea       NOT NULL,
    thumb        bytea       NOT NULL,
    updated_at   timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE service_kits (
    id              bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    vehicle_id      bigint      NOT NULL REFERENCES vehicles (id) ON DELETE CASCADE,
    name            text        NOT NULL CHECK (name <> ''),
    interval_km     integer     CHECK (interval_km > 0),
    interval_months integer     CHECK (interval_months > 0),
    notes           text        NOT NULL DEFAULT '',
    created_at      timestamptz NOT NULL DEFAULT now(),
    updated_at      timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX service_kits_vehicle_idx ON service_kits (vehicle_id);

CREATE TABLE service_kit_lines (
    id       bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    kit_id   bigint        NOT NULL REFERENCES service_kits (id) ON DELETE CASCADE,
    item_id  bigint        NOT NULL REFERENCES items (id) ON DELETE CASCADE,
    quantity numeric(12,3) NOT NULL CHECK (quantity > 0),
    UNIQUE (kit_id, item_id)
);

CREATE TABLE service_records (
    id           bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    vehicle_id   bigint        NOT NULL REFERENCES vehicles (id) ON DELETE CASCADE,
    kit_id       bigint        REFERENCES service_kits (id) ON DELETE SET NULL,
    performed_on date          NOT NULL DEFAULT current_date,
    odometer_km  integer       CHECK (odometer_km >= 0),
    title        text          NOT NULL CHECK (title <> ''),
    notes        text          NOT NULL DEFAULT '',
    labor_cost   numeric(12,2) CHECK (labor_cost >= 0),
    created_at   timestamptz   NOT NULL DEFAULT now()
);
CREATE INDEX service_records_vehicle_idx ON service_records (vehicle_id, performed_on DESC);
CREATE INDEX service_records_kit_idx ON service_records (kit_id, performed_on DESC);

-- Every change to items.quantity is recorded here (in the same transaction).
CREATE TABLE stock_movements (
    id                bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    item_id           bigint        NOT NULL REFERENCES items (id) ON DELETE CASCADE,
    kind              text          NOT NULL CHECK (kind IN ('initial', 'purchase', 'use', 'correction', 'service')),
    delta             numeric(12,3) NOT NULL CHECK (delta <> 0),
    total_price       numeric(12,2) CHECK (total_price >= 0),
    supplier          text          NOT NULL DEFAULT '',
    note              text          NOT NULL DEFAULT '',
    occurred_on       date          NOT NULL DEFAULT current_date,
    service_record_id bigint        REFERENCES service_records (id) ON DELETE SET NULL,
    created_at        timestamptz   NOT NULL DEFAULT now()
);
CREATE INDEX stock_movements_item_idx ON stock_movements (item_id, created_at DESC);
CREATE INDEX stock_movements_service_idx ON stock_movements (service_record_id);
CREATE INDEX stock_movements_created_idx ON stock_movements (created_at DESC);

CREATE TABLE shopping_entries (
    id         bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    item_id    bigint        REFERENCES items (id) ON DELETE CASCADE,
    free_text  text          NOT NULL DEFAULT '',
    quantity   numeric(12,3) CHECK (quantity > 0),
    note       text          NOT NULL DEFAULT '',
    created_at timestamptz   NOT NULL DEFAULT now(),
    CHECK (item_id IS NOT NULL OR free_text <> '')
);
CREATE UNIQUE INDEX shopping_entries_item_key ON shopping_entries (item_id) WHERE item_id IS NOT NULL;

-- +goose Down
DROP TABLE shopping_entries;
DROP TABLE stock_movements;
DROP TABLE service_records;
DROP TABLE service_kit_lines;
DROP TABLE service_kits;
DROP TABLE item_photos;
DROP TABLE item_vehicles;
DROP TABLE item_skus;
DROP TABLE items;
DROP TABLE vehicles;

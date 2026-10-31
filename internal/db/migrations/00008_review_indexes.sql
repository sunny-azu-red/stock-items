-- +goose Up
-- Which kits use an item (item pages, deleting an item): the unique index
-- on (kit_id, item_id) can't serve a lookup by item.
CREATE INDEX service_kit_lines_item_idx ON service_kit_lines (item_id);
-- Restores look up whether a row is itself in the trash by containment
-- (payload @> {"items": [{"id": 5}]}).
CREATE INDEX trash_payload_idx ON trash USING gin (payload jsonb_path_ops);

-- +goose Down
DROP INDEX trash_payload_idx;
DROP INDEX service_kit_lines_item_idx;

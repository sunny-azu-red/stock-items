-- +goose Up
ALTER TABLE item_skus DROP CONSTRAINT item_skus_kind_check;
ALTER TABLE item_skus ADD CONSTRAINT item_skus_kind_check
    CHECK (kind IN ('oem', 'manufacturer', 'ean', 'retailer', 'other'));

-- +goose Down
UPDATE item_skus SET kind = 'other' WHERE kind = 'ean';
ALTER TABLE item_skus DROP CONSTRAINT item_skus_kind_check;
ALTER TABLE item_skus ADD CONSTRAINT item_skus_kind_check
    CHECK (kind IN ('oem', 'manufacturer', 'retailer', 'other'));

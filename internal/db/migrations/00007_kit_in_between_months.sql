-- +goose Up
-- A kit that counts no distance may have intermediate services too: they
-- come in equal steps of its months (stock.Kit.InterMonths). So in_between
-- needs a distance or months, no longer a distance. Nothing changes.
ALTER TABLE service_kits
    DROP CONSTRAINT service_kits_in_between_needs_distance,
    ADD CONSTRAINT service_kits_in_between_needs_interval CHECK (in_between = 0 OR interval_distance IS NOT NULL OR interval_months IS NOT NULL);

-- +goose Down
-- A kit with intermediate services and months only stops it, changing
-- nothing: they have nowhere to go without this. Set the kit's
-- intermediate services to None first.
-- +goose StatementBegin
DO $$
DECLARE
    bad text;
BEGIN
    SELECT string_agg(name, ', ' ORDER BY id) INTO bad FROM service_kits WHERE in_between > 0 AND interval_distance IS NULL;
    IF bad IS NOT NULL THEN
        RAISE EXCEPTION 'kits with intermediate services and months only: %', bad;
    END IF;
END $$;
-- +goose StatementEnd
ALTER TABLE service_kits
    DROP CONSTRAINT service_kits_in_between_needs_interval,
    ADD CONSTRAINT service_kits_in_between_needs_distance CHECK (in_between = 0 OR interval_distance IS NOT NULL);

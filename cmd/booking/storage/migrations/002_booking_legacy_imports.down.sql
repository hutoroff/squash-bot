DELETE FROM booking_resource_claims WHERE import_id IS NOT NULL;
DELETE FROM booking_provider_bookings WHERE import_id IS NOT NULL;

DROP INDEX IF EXISTS booking_resource_claims_import_type;
ALTER TABLE booking_resource_claims
    DROP CONSTRAINT IF EXISTS booking_resource_claims_one_owner,
    DROP COLUMN IF EXISTS import_id,
    ALTER COLUMN operation_id SET NOT NULL;

ALTER TABLE booking_provider_bookings
    DROP CONSTRAINT IF EXISTS booking_provider_bookings_one_owner,
    DROP COLUMN IF EXISTS import_id,
    ALTER COLUMN create_operation_id SET NOT NULL;

DROP TABLE IF EXISTS booking_imports;

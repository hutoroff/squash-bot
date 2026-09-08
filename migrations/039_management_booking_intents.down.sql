ALTER TABLE court_bookings
    DROP CONSTRAINT IF EXISTS court_bookings_reliable_origin_complete,
    DROP COLUMN IF EXISTS management_create_operation_id,
    DROP COLUMN IF EXISTS booking_service_key,
    DROP COLUMN IF EXISTS booking_provider,
    DROP COLUMN IF EXISTS booking_facility_id,
    DROP COLUMN IF EXISTS booking_facility_uuid,
    DROP COLUMN IF EXISTS booking_facility_slug,
    DROP COLUMN IF EXISTS booking_sport;
DROP TABLE IF EXISTS management_booking_operations;
DROP TABLE IF EXISTS management_booking_intents;

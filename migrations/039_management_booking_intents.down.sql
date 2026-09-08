DROP INDEX IF EXISTS uq_games_management_booking_intent;
ALTER TABLE games DROP COLUMN IF EXISTS management_booking_intent_id;
DROP INDEX IF EXISTS uq_auto_booking_results_management_intent;
ALTER TABLE auto_booking_results DROP COLUMN IF EXISTS management_booking_intent_id;

ALTER TABLE court_bookings
    DROP CONSTRAINT IF EXISTS court_bookings_reliable_origin_complete,
    DROP COLUMN IF EXISTS management_create_operation_id,
    DROP COLUMN IF EXISTS booking_service_key,
    DROP COLUMN IF EXISTS booking_provider,
    DROP COLUMN IF EXISTS booking_facility_id,
    DROP COLUMN IF EXISTS booking_facility_uuid,
    DROP COLUMN IF EXISTS booking_facility_slug,
    DROP COLUMN IF EXISTS booking_sport;
DROP TABLE IF EXISTS management_booking_notifications;
DROP TABLE IF EXISTS management_booking_operations;
DROP TABLE IF EXISTS management_booking_intents;

DROP TABLE IF EXISTS booking_operation_events;
DROP TABLE IF EXISTS booking_provider_bookings;
DROP TABLE IF EXISTS booking_resource_claims;
DROP TABLE IF EXISTS booking_operation_steps;
ALTER TABLE IF EXISTS booking_accounts DROP CONSTRAINT IF EXISTS booking_accounts_quarantine_operation_fk;
ALTER TABLE IF EXISTS booking_accounts DROP CONSTRAINT IF EXISTS booking_accounts_active_operation_fk;
DROP TABLE IF EXISTS booking_operations;
DROP TABLE IF EXISTS booking_accounts;
DROP TABLE IF EXISTS booking_metadata;

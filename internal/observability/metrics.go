package observability

// Shared metric names are contracts for dashboards and instrumentation. New
// labels must remain bounded infrastructure dimensions.
const (
	MetricBuildInfo                        = "squash_build_info"
	MetricServiceReady                     = "squash_service_ready"
	MetricHTTPRequestsTotal                = "squash_http_requests_total"
	MetricHTTPRequestDurationSeconds       = "squash_http_request_duration_seconds"
	MetricDependencyRequestsTotal          = "squash_dependency_requests_total"
	MetricDependencyRequestDurationSeconds = "squash_dependency_request_duration_seconds"
	MetricDiscoveryRegistrationSuccess     = "squash_discovery_registration_success"
	MetricDiscoveryLastSuccessTimestamp    = "squash_discovery_last_success_timestamp_seconds"
	MetricRegistryStorageHealthy           = "squash_registry_storage_healthy"
	MetricServiceLiveInstances             = "squash_service_live_instances"
	MetricServiceExpectedInstances         = "squash_service_expected_instances"
	MetricSchedulerRunsTotal               = "squash_scheduler_runs_total"
	MetricSchedulerLastCompletedTimestamp  = "squash_scheduler_last_completed_timestamp_seconds"
	MetricBookingOperationsTotal           = "squash_booking_operations_total"
	MetricTelegramUpdatesTotal             = "squash_telegram_updates_total"
)

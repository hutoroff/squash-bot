#!/usr/bin/env node
// Versioned repository templates, not a request to provision the owner's Grafana.
import { readFileSync, writeFileSync } from 'node:fs';
import { fileURLToPath } from 'node:url';
import { join, resolve } from 'node:path';

const dir = fileURLToPath(new URL('.', import.meta.url));
const datasource = { type: 'prometheus', uid: 'squash_prometheus' };
export const alerts = [
  {
    uid: 'squash_management_missing', title: 'Management unreachable', service: 'management', for: '1m',
    expr: '(up{job="management"} == bool 0) or absent(up{job="management"})',
    note: 'Static management scrape down or absent; independent of HTTP SD.',
  },
  {
    uid: 'squash_required_service_missing', title: 'Required service missing', for: '1m',
    expr: '((squash_service_expected_instances{job="management"} - on(service_key) squash_service_live_instances{job="management"} > bool 0) or (squash_service_expected_instances{job="management"} unless on(service_key) squash_service_live_instances{job="management"})) and on() (squash_registry_storage_healthy{job="management"} == 1)',
    note: 'Inventory detects never-registered and expired instances; storage outage alerts separately.',
  },
  {
    uid: 'squash_registry_storage_down', title: 'Registry storage unavailable', service: 'management', for: '1m',
    expr: 'squash_registry_storage_healthy{job="management"} == bool 0',
    note: 'Authoritative registry snapshot cannot be read; live counts are omitted.',
  },
  {
    uid: 'squash_unsupported_duplicates', title: 'Unsupported duplicate instances', for: '30s',
    expr: 'squash_service_live_instances{job="management",service_key!="web"} > bool 1',
    note: 'Management, Telegram and configured booking keys must remain single-active.',
  },
  {
    uid: 'squash_service_unready', title: 'Service not ready', for: '2m',
    expr: '(squash_service_ready == bool 0) * on(job, instance) group_left() (up == 1)',
    note: 'An instance is scraped successfully but reports local readiness 0.',
  },
  {
    uid: 'squash_scheduler_stalled', title: 'Scheduler stalled', service: 'management', for: '1m',
    expr: '((time() - squash_scheduler_last_completed_timestamp_seconds{job="management",exported_job="post_leaderboard"}) > bool on() (2 * squash_scheduler_poll_interval_seconds{job="management"} + 60)) or ((time() - process_start_time_seconds{job="management"}) > bool on() (2 * squash_scheduler_poll_interval_seconds{job="management"} + 60) unless on() squash_scheduler_last_completed_timestamp_seconds{job="management",exported_job="post_leaderboard"})',
    note: 'Last job of the sequential poll has not returned; includes the never-completed-after-start case.',
  },
  {
    uid: 'squash_booking_unknown_new', title: 'New unknown provider execution', service: 'booking', for: '0s',
    expr: 'sum(increase(booking_external_executions_total{outcome="unknown"}[5m])) or vector(0)',
    note: 'A new unknown execution; persistent uncertainty has separate durable alerts.',
  },
  {
    uid: 'squash_management_unknown_durable', title: 'Management booking unresolved', service: 'management', for: '0s',
    expr: 'squash_management_booking_operations{job="management",state="outcome_unknown"}',
    note: 'Durable management unknown operations persist across counter/process resets.',
  },
  {
    uid: 'squash_provider_unknown_durable', title: 'Provider booking unresolved', for: '0s', optional: true,
    expr: 'booking_operations_unknown',
    note: 'Provider ledger unknown count persists across counter/process resets; only when booking is expected.',
  },
  {
    uid: 'squash_monitoring_unavailable', title: 'Prometheus monitoring unavailable', service: 'prometheus', for: '1m',
    expr: '(up{job="prometheus"} == bool 0) or absent(up{job="prometheus"})',
    note: 'Prometheus self-scrape absent/down; query errors require Grafana Error=Alerting.',
  },
];

function query(rule) {
  return [
    { refId: 'A', datasourceUid: datasource.uid, relativeTimeRange: { from: 300, to: 0 },
      model: { datasource, expr: rule.expr, instant: true, intervalMs: 1000, maxDataPoints: 43200, refId: 'A' } },
    { refId: 'B', datasourceUid: '__expr__', model: { datasource: { type: '__expr__', uid: '__expr__' },
      expression: 'A', intervalMs: 1000, maxDataPoints: 43200, reducer: 'last', refId: 'B', type: 'reduce',
      conditions: [{ evaluator: { params: [], type: 'gt' }, operator: { type: 'and' },
        query: { params: ['B'] }, reducer: { params: [], type: 'last' }, type: 'query' }] } },
    { refId: 'C', datasourceUid: '__expr__', model: { datasource: { type: '__expr__', uid: '__expr__' },
      expression: 'B', intervalMs: 1000, maxDataPoints: 43200, refId: 'C', type: 'threshold',
      conditions: [{ evaluator: { params: [0], type: 'gt' }, operator: { type: 'and' },
        query: { params: ['C'] }, reducer: { params: [], type: 'last' }, type: 'query' }] } },
  ];
}
export const ruleFile = { apiVersion: 1, groups: [{ orgId: 1, name: 'squash-service-health', folder: 'Squash Bot', interval: '30s',
  rules: alerts.map(rule => ({ uid: rule.uid, title: rule.title, condition: 'C', data: query(rule),
    noDataState: rule.optional || ['squash_required_service_missing', 'squash_unsupported_duplicates'].includes(rule.uid) ? 'NoData' : 'Alerting',
    execErrState: 'Alerting', for: rule.for,
    annotations: { summary: rule.note }, labels: { deployment: 'squash-bot', severity: 'critical', ...(rule.service ? { service_key: rule.service } : {}) },
    isPaused: !!rule.optional })) }] };

const panel = (id, title, expr, unit = 'short', description = '', legend = '{{__name__}} {{service_key}} {{job}} {{exported_job}} {{outcome}} {{state}}') => ({ id, title, type: 'timeseries',
  datasource, gridPos: { h: 8, w: 12, x: (id - 1) % 2 * 12, y: Math.floor((id - 1) / 2) * 8 },
  fieldConfig: { defaults: { unit }, overrides: [] }, options: { legend: { displayMode: 'list', placement: 'bottom' } },
  targets: (Array.isArray(expr) ? expr : [expr]).map((query, index) => ({
    expr: query, refId: String.fromCharCode(65 + index),
    legendFormat: legend,
  })), description });
export const dashboard = {
  uid: 'squash-service-discovery', title: 'Squash Bot / Service health', schemaVersion: 39, version: 1,
  tags: ['squash-bot', 'discovery'], timezone: 'browser', refresh: '30s', time: { from: 'now-6h', to: 'now' },
  panels: [
    panel(1, 'Expected and ready instances', ['squash_service_expected_instances', 'squash_service_live_instances'], 'short', 'Management inventory remains present for never-registered services.'),
    panel(2, 'Scrape success and service readiness', ['up', 'squash_service_ready'], 'short', 'Missing discovered up series does not mean healthy; check inventory.'),
    panel(3, 'Build versions', 'squash_build_info', 'short', 'Version is only a label on build info.', '{{service_key}} {{version}}'),
    panel(4, 'Lease / registry storage', ['squash_discovery_registration_success', 'squash_registry_storage_healthy'], 'short'),
    panel(5, 'Last registration age', 'time() - (squash_discovery_last_success_timestamp_seconds > 0)', 's', 'No series means no successful registration yet; see expected/live inventory.'),
    panel(6, 'HTTP failures (application and booking)', ['sum by (job, status) (increase(squash_http_requests_total{status=~"5.."}[5m]))', 'sum by (job, status) (increase(booking_http_requests_total{status=~"5.."}[5m]))'], 'short'),
    panel(7, 'HTTP p95 latency', ['histogram_quantile(0.95, sum by (le, job) (rate(squash_http_request_duration_seconds_bucket[5m])))', 'histogram_quantile(0.95, sum by (le, job) (rate(booking_http_request_duration_seconds_bucket[5m])))'], 's'),
    panel(8, 'Dependency failures', 'sum by (job, target, outcome) (increase(squash_dependency_requests_total{outcome=~"transport_error|resolution_error|5.."}[5m]))', 'short'),
    panel(9, 'Provider execution outcomes', 'sum by (kind, outcome) (increase(booking_external_executions_total[5m]))', 'short', 'Counter resets on restart; use durable unresolved panels too.'),
    panel(10, 'Durable unknown / pending', ['booking_operations_unknown', 'squash_management_booking_operations{state="outcome_unknown"}', 'squash_management_booking_pending_notifications'], 'short'),
    panel(11, 'Oldest unresolved age', ['booking_oldest_unknown_age_seconds', 'squash_management_booking_oldest_pending_age_seconds'], 's'),
    panel(12, 'Booking storage / risk', ['squash_management_booking_storage_healthy', 'booking_database_healthy', 'squash_management_booking_risk_authorized', 'booking_risk_replacements'], 'short', 'Booking database health is process readiness; ledger-read errors can yield zero, not proof of no unknowns.'),
    panel(13, 'Scheduler last completion age', 'time() - squash_scheduler_last_completed_timestamp_seconds', 's', 'The application scheduler job label is exported_job; job remains management.'),
    panel(14, 'Scheduler outcomes / interval', ['sum by (exported_job, outcome) (increase(squash_scheduler_runs_total[30m]))', 'squash_scheduler_poll_interval_seconds'], 'short', 'Unclassified is not business success.'),
    panel(15, 'Go/process memory', ['go_memstats_heap_alloc_bytes', 'process_resident_memory_bytes'], 'bytes'),
    panel(16, 'Goroutines / Prometheus health', ['go_goroutines', 'prometheus_tsdb_head_series', 'up{job="prometheus"}'], 'short'),
    panel(17, 'Dependency p95 latency', 'histogram_quantile(0.95, sum by (le, job, target) (rate(squash_dependency_request_duration_seconds_bucket[5m])))', 's'),
    panel(18, 'Provider execution p95 latency', 'histogram_quantile(0.95, sum by (le, kind) (rate(booking_external_execution_duration_seconds_bucket[5m])))', 's'),
    panel(19, 'Telegram update outcomes', 'sum by (kind, outcome) (increase(squash_telegram_updates_total[5m]))', 'short'),
    panel(20, 'Prometheus TSDB size', 'prometheus_tsdb_storage_blocks_bytes', 'bytes'),
  ],
};

if (process.argv[1] && fileURLToPath(import.meta.url) === resolve(process.argv[1])) {
  let stale = false;
  for (const [name, value] of [['alerts.json', ruleFile], ['dashboard.json', dashboard]]) {
    const dest = join(dir, name);
    const content = JSON.stringify(value, null, 2) + '\n';
    if (process.argv.includes('--check')) {
      if (readFileSync(dest, 'utf8') !== content) { console.error(`${name}: stale`); stale = true; }
    } else writeFileSync(dest, content);
  }
  if (stale) process.exitCode = 1;
}

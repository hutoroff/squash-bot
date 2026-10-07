#!/usr/bin/env node
// Explicit, Docker-backed deployment check. Intentionally not part of Docker-free check-fast.
import assert from 'node:assert/strict';
import { execFileSync } from 'node:child_process';
import { mkdtempSync, readFileSync, rmSync, writeFileSync } from 'node:fs';
import { tmpdir } from 'node:os';
import { join, resolve } from 'node:path';
import { fileURLToPath } from 'node:url';

const root = resolve(fileURLToPath(new URL('../..', import.meta.url)));
const scratch = mkdtempSync(join(tmpdir(), 'squash-monitoring-check-'));
const token = join(scratch, 'monitoring_token');
const envFile = join(scratch, 'synthetic.env');
// No inherited Compose/application environment and no default owner .env.
const env = { PATH: process.env.PATH ?? '/usr/bin:/bin', HOME: process.env.HOME ?? '/tmp' };
if (process.env.DOCKER_HOST) env.DOCKER_HOST = process.env.DOCKER_HOST;
if (process.env.DOCKER_CONTEXT) env.DOCKER_CONTEXT = process.env.DOCKER_CONTEXT;
const execute = (...args) => execFileSync('docker', args, { cwd: root, env, encoding: 'utf8', stdio: ['ignore', 'pipe', 'pipe'], timeout: 120_000 });

try {
  writeFileSync(token, 'synthetic-monitoring-bearer-not-a-credential', { mode: 0o644 });
  const syntheticEnv = [
    'COMPOSE_PROJECT_NAME=squash_synthetic_check',
    'DOCKERHUB_USERNAME=synthetic',
    'DOCKERHUB_TOKEN=synthetic',
    'POSTGRES_PASSWORD=synthetic',
    'MONITORING_TOKEN=synthetic-monitoring-bearer-not-a-credential',
    'INTERNAL_API_SECRET=synthetic-internal',
    'TELEGRAM_BOT_TOKEN=synthetic',
    'TELEGRAM_BOT_NAME=synthetic',
    'JWT_SECRET=synthetic',
    `PROMETHEUS_TOKEN_FILE=${token}`,
  ].join('\n') + '\n';
  const composeConfig = (overrides = '') => {
    writeFileSync(envFile, syntheticEnv + overrides, { mode: 0o600 });
    return JSON.parse(execute('compose', '--env-file', envFile, '-f', 'docker-compose.prod.yml', 'config', '--format', 'json'));
  };
  const config = composeConfig();
  const services = config.services;
  const network = config.networks.internal;
  assert.equal(network.external, true);
  assert.equal(network.name, 'squash_services');
  for (const name of ['postgres', 'management', 'telegram', 'booking', 'web', 'db-backup', 'prometheus']) {
    assert.ok(Object.hasOwn(services[name].networks, 'internal'), `${name} must join the external network`);
  }
  for (const name of ['management', 'telegram', 'booking']) {
    assert.equal(services[name].deploy.replicas, 1, `${name} must remain singleton`);
    assert.ok(!services[name].ports?.length, `${name} must not publish admin/business ports`);
    assert.ok(!services[name].depends_on?.management && !services[name].depends_on?.booking && !services[name].depends_on?.telegram,
      `${name} must not require another application at startup`);
  }
  assert.ok(!services.postgres.ports?.length, 'database must not be published');
  assert.ok(!services['db-backup'].ports?.length, 'backup must not be published');
  assert.deepEqual(services.web.ports.map(p => p.target), [8082], 'only the web ingress may be public');
  assert.equal(services.booking.stop_grace_period, '1m50s');
  assert.deepEqual(Object.keys(services.management.depends_on), ['postgres']);
  assert.deepEqual(Object.keys(services.booking.depends_on), ['postgres']);
  assert.equal(services.management.environment.DISCOVERY_ALLOWED_CIDRS, '172.30.90.0/24');
  assert.equal(services.telegram.environment.DISCOVERY_NETWORK_INTERFACE, 'eth0');
  assert.equal(services.management.environment.DISCOVERY_MODE, 'static');
  assert.equal(services.web.environment.DISCOVERY_MODE, 'static');
  assert.equal(services.telegram.environment.DISCOVERY_MODE, 'static');
  assert.equal(services.management.environment.SPORTS_BOOKING_SERVICE_URL, '');
  assert.equal(services.telegram.environment.MANAGEMENT_SERVICE_URL, 'http://management:8080');
  const inventory = JSON.parse(services.management.environment.MONITORED_SERVICE_KEYS);
  assert.deepEqual(inventory.map(s => s.service_key), ['management', 'telegram', 'web']);
  const shadow = composeConfig('DISCOVERY_URL=http://management:8080\n');
  assert.equal(shadow.services.telegram.environment.DISCOVERY_MODE, 'static');
  assert.equal(shadow.services.telegram.environment.DISCOVERY_URL, 'http://management:8080');
  const registry = composeConfig([
    'DISCOVERY_MODE=registry',
    'DISCOVERY_URL=http://management:8080',
    'MANAGEMENT_SERVICE_URL=',
    'SPORTS_BOOKING_SERVICE_URL=',
    'BOOKING_SERVICE_KEY=booking.eversports.primary',
  ].join('\n') + '\n');
  for (const name of ['management', 'telegram', 'web']) {
    assert.equal(registry.services[name].environment.DISCOVERY_MODE, 'registry');
  }
  assert.equal(registry.services.telegram.environment.MANAGEMENT_SERVICE_URL, '');
  assert.equal(registry.services.web.environment.MANAGEMENT_SERVICE_URL, '');
  assert.equal(registry.services.management.environment.SPORTS_BOOKING_SERVICE_URL, '');
  assert.equal(registry.services.management.environment.BOOKING_SERVICE_KEY, 'booking.eversports.primary');

  const prom = services.prometheus;
  assert.match(prom.image, /^prom\/prometheus:v3\.9\.1@sha256:[a-f0-9]{64}$/);
  assert.equal(prom.ports.length, 1);
  assert.equal(prom.ports[0].host_ip, '127.0.0.1');
  assert.equal(prom.ports[0].target, 9090);
  assert.ok(prom.volumes.some(v => v.target === '/run/secrets/monitoring_token' && v.source === token && v.read_only && v.bind?.create_host_path === false));
  assert.ok(prom.volumes.some(v => v.target === '/etc/prometheus/prometheus.yml' && v.read_only && v.bind?.create_host_path === false));
  assert.ok(prom.volumes.some(v => v.target === '/prometheus' && v.type === 'volume'));
  assert.ok(prom.command.includes('--storage.tsdb.retention.time=15d'));
  assert.ok(prom.command.includes('--storage.tsdb.retention.size=4GB'));
  assert.ok(!prom.command.some(flag => /enable-admin-api|enable-lifecycle|remote-write-receiver/.test(flag)));
  assert.ok(!prom.environment?.MONITORING_TOKEN, 'Prometheus must use a file, not an environment bearer');
  assert.equal(prom.labels['com.centurylinklabs.watchtower.enable'], 'false');

  const text = readFileSync(join(root, 'monitoring/prometheus/prometheus.yml'), 'utf8');
  const jobs = Object.fromEntries(text.split(/^  - job_name: /m).slice(1)
    .map(block => [block.split('\n', 1)[0], block]));
  const management = jobs.management;
  const discovered = jobs['discovered-services'];
  const self = jobs.prometheus;
  assert.match(management, /targets: \['management:9090'\]/);
  assert.match(discovered, /url: http:\/\/management:8080\/internal\/discovery\/v1\/prometheus-targets/);
  assert.match(self, /targets: \['localhost:9090'\]/);
  const credentials = /credentials_file: \/run\/secrets\/monitoring_token/g;
  assert.equal((management.match(credentials) ?? []).length, 1, 'static management scrape must authenticate');
  assert.equal((discovered.match(credentials) ?? []).length, 2, 'HTTP SD and discovered scrapes must each authenticate');
  assert.equal((self.match(credentials) ?? []).length, 0, 'local self-scrape must not need the application token');
  assert.doesNotMatch(text, /\$\{|INTERNAL_API_SECRET|MONITORING_TOKEN/);

  // The pinned image runs the selected version's promtool; no network or application starts.
  execute('run', '--rm', '--network', 'none', '--read-only',
    '-v', `${join(root, 'monitoring/prometheus/prometheus.yml')}:/etc/prometheus/prometheus.yml:ro`,
    '-v', `${token}:/run/secrets/monitoring_token:ro`,
    '--entrypoint', '/bin/promtool', prom.image, 'check', 'config', '/etc/prometheus/prometheus.yml');
  console.log('Synthetic production Compose topology and pinned-image promtool config: PASS');
} catch (error) {
  console.error('Synthetic monitoring configuration check: FAIL');
  // Do not print rendered Compose (which could contain an interpolated bearer).
  console.error(error.message);
  process.exitCode = 1;
} finally {
  rmSync(scratch, { recursive: true, force: true });
}

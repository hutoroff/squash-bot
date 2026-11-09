// Offline staging Compose contract check. Never starts a service or reads .env.
import assert from 'node:assert/strict';
import { spawnSync } from 'node:child_process';
import fs from 'node:fs';
import os from 'node:os';
import path from 'node:path';
import { fileURLToPath } from 'node:url';

const root = fileURLToPath(new URL('../../', import.meta.url));
const temp = fs.mkdtempSync(path.join(os.tmpdir(), 'squash-staging-config-'));
try {
  const tokenFile = path.join(temp, 'monitoring-token');
  const envFile = path.join(temp, 'staging.env');
  fs.writeFileSync(tokenFile, 'y'.repeat(32), { mode: 0o600 });
  fs.writeFileSync(envFile, [
    'POSTGRES_PASSWORD=synthetic-database-password',
    'TELEGRAM_BOT_TOKEN=x',
    'TELEGRAM_BOT_NAME=synthetic_bot',
    'INTERNAL_API_SECRET=' + 'x'.repeat(32),
    'MONITORING_TOKEN=' + 'y'.repeat(32),
    'JWT_SECRET=' + 'z'.repeat(32),
    'BOOKING_IDENTITY_KEY=' + 'a'.repeat(64),
    'BOOKING_SERVICE_KEY=eversports.staging.squash',
    'EVERSPORTS_FACILITY_ID=1',
    'EVERSPORTS_FACILITY_UUID=00000000-0000-4000-8000-000000000001',
    'EVERSPORTS_FACILITY_SLUG=staging-only',
    'EVERSPORTS_SQUASH_SPORT_UUID=00000000-0000-4000-8000-000000000002',
    'STAGING_BACKEND_SUBNET=172.30.91.0/24',
    'STAGING_PROMETHEUS_TOKEN_FILE=' + tokenFile,
  ].join('\n') + '\n', { mode: 0o600 });
  const result = spawnSync('docker', ['compose', '--project-name', 'squash-staging', '--env-file', envFile,
    '-f', path.join(root, 'docker-compose.staging.yml'), 'config', '--format', 'json'], {
    cwd: root, encoding: 'utf8', timeout: 30_000, maxBuffer: 4 * 1024 * 1024,
    env: { PATH: process.env.PATH, HOME: process.env.HOME, STAGING_REV: 'fixture-revision', STAGING_ENV_FILE: envFile },
  });
  // Config may contain secrets: do not print stdout/stderr on error.
  assert.equal(result.status, 0, 'Docker Compose must parse the staging file with synthetic inputs');
  const config = JSON.parse(result.stdout);
  const services = config.services;
  assert.deepEqual(Object.keys(services).sort(), ['booking', 'management', 'postgres', 'prometheus', 'telegram', 'web']);
  assert.equal(config.name, 'squash-staging');
  assert.equal(config.networks.backend.internal, true);
  assert.equal(config.networks.backend.ipam.config[0].subnet, '172.30.91.0/24');
  assert.equal(config.networks.egress.internal ?? false, false);
  for (const service of Object.values(services)) {
    assert.equal(service.labels['com.centurylinklabs.watchtower.enable'], 'false');
  }
  for (const name of ['management', 'telegram', 'booking', 'web']) {
    const service = services[name];
    assert.equal(service.pull_policy, 'build');
    assert.equal(service.image, `squash-staging/${name}:fixture-revision`);
    assert.equal(service.build.args.VERSION, 'fixture-revision');
    assert.equal(service.ports?.length ?? 0, name === 'web' ? 1 : 0);
    assert.equal(service.networks.backend.interface_name, 'eth0');
  }
  assert.deepEqual(Object.keys(services.booking.networks), ['backend']);
  assert.equal(services.management.environment.SPORTS_BOOKING_SERVICE_URL, '');
  assert.equal(services.management.environment.BOOKING_SERVICE_KEY, '');
  assert.equal(services.management.environment.DISCOVERY_MODE, 'static');
  assert.equal(services.telegram.environment.TELEGRAM_WEBHOOK_URL, '');
  assert.equal(services.web.environment.DISCOVERY_MODE, 'static');
  assert.match(services.management.environment.MONITORED_SERVICE_KEYS, /eversports\.staging\.squash/);
  for (const name of ['web', 'prometheus']) {
    assert.equal(services[name].ports.length, 1);
    assert.equal(services[name].ports[0].host_ip, '127.0.0.1');
  }
  for (const name of ['booking', 'postgres', 'prometheus', 'web']) {
    assert.deepEqual(Object.keys(services[name].networks), ['backend']);
  }
  for (const name of ['management', 'telegram']) {
    assert.deepEqual(Object.keys(services[name].networks).sort(), ['backend', 'egress']);
    assert.equal(services[name].networks.egress.interface_name, 'eth1');
  }
  assert.equal(services.prometheus.volumes.find(v => v.target === '/run/secrets/monitoring_token').read_only, true);
  console.log('PASS: dedicated staging Compose topology and local revision tags (synthetic config only)');
} finally {
  fs.rmSync(temp, { recursive: true, force: true });
}

#!/usr/bin/env node
// Offline validation of repository templates only. Does not contact or start Grafana.
import assert from 'node:assert/strict';
import { execFileSync } from 'node:child_process';
import { mkdtempSync, readFileSync, rmSync, writeFileSync } from 'node:fs';
import { tmpdir } from 'node:os';
import { join } from 'node:path';
import { fileURLToPath } from 'node:url';
import { alerts, dashboard, ruleFile } from '../../monitoring/grafana/generate.mjs';

const root = fileURLToPath(new URL('../..', import.meta.url));
const dir = join(root, 'monitoring/grafana');
const json = file => JSON.parse(readFileSync(join(dir, file), 'utf8'));
const expectedImage = 'prom/prometheus:v3.9.1@sha256:1f0f50f06acaceb0f5670d2c8a658a599affe7b0d8e78b898c1035653849a702';
const scratch = mkdtempSync(join(tmpdir(), 'squash-grafana-fixture-'));
try {
  assert.deepEqual(json('alerts.json'), ruleFile, 'regenerate alerts.json after edits');
  assert.deepEqual(json('dashboard.json'), dashboard, 'regenerate dashboard.json after edits');
  assert.equal(ruleFile.apiVersion, 1);
  assert.equal(ruleFile.groups[0].interval, '30s');
  assert.equal(new Set(alerts.map(a => a.uid)).size, alerts.length);
  for (const rule of ruleFile.groups[0].rules) {
    assert.match(rule.uid, /^[a-z0-9_]{1,40}$/);
    assert.equal(rule.condition, 'C');
    assert.deepEqual(rule.data.map(d => d.refId), ['A', 'B', 'C']);
    assert.equal(rule.data[0].datasourceUid, 'squash_prometheus');
    assert.equal(rule.data[0].model.instant, true);
    assert.equal(rule.data[2].model.type, 'threshold');
    assert.equal(rule.execErrState, 'Alerting');
    assert.ok(['Alerting', 'NoData'].includes(rule.noDataState));
    assert.equal(rule.labels.deployment, 'squash-bot');
    assert.ok(!JSON.stringify(rule).includes('Bearer '));
  }
  const rulesByUID = Object.fromEntries(ruleFile.groups[0].rules.map(r => [r.uid, r]));
  assert.equal(rulesByUID.squash_required_service_missing.noDataState, 'NoData');
  assert.equal(rulesByUID.squash_registry_storage_down.execErrState, 'Alerting');
  assert.equal(rulesByUID.squash_provider_unknown_durable.isPaused, true, 'optional booking rule must not page by default');
  assert.equal(dashboard.schemaVersion, 39);
  assert.equal(dashboard.uid, 'squash-service-discovery');
  assert.ok(dashboard.panels.length >= 16);
  assert.equal(new Set(dashboard.panels.map(p => p.id)).size, dashboard.panels.length);
  assert.equal(dashboard.panels[0].targets.length, 2, 'expected and live series must not suppress each other');
  for (const panel of dashboard.panels) {
    assert.equal(panel.datasource.uid, 'squash_prometheus');
    for (const target of panel.targets) assert.ok(target.expr.length > 0);
  }
  const privateSource = json('datasource-private.example.yaml').datasources[0];
  const proxySource = json('datasource-proxy.example.yaml').datasources[0];
  for (const source of [privateSource, proxySource]) {
    assert.equal(source.access, 'proxy');
    assert.equal(source.url, '$SQUASH_PROMETHEUS_URL');
    assert.equal(source.uid, 'squash_prometheus');
  }
  assert.equal(proxySource.secureJsonData.httpHeaderValue1, 'Bearer $SQUASH_GRAFANA_QUERY_TOKEN');
  assert.ok(!privateSource.secureJsonData);
  assert.equal(json('contact-point.example.json').contactPoints[0].receivers[0].disableResolveMessage, false);
  assert.equal(json('policy.example.json').policies[0].routes[0].repeat_interval, '1h');
  assert.deepEqual(json('policy.example.json').policies[0].routes[0].group_by, ['deployment', 'service_key', 'alertname']);
  assert.ok(json('mute-time.example.json').muteTimes.length > 0);
  assert.equal(json('dashboard-provider.example.yaml').providers[0].options.path, '/etc/grafana/squash-dashboards');
  assert.equal(readFileSync(join(root, 'docker-compose.prod.yml'), 'utf8').includes('grafana:'), false, 'do not add local Grafana');

  // Prometheus parses the exact Grafana PromQL expressions and runs synthetic
  // target-disappearance/zero-series tests; rules.yml is disposable and is NOT
  // a Prometheus alerting/notification configuration.
  const rules = [
    'groups:',
    '  - name: grafana-expression-fixtures',
    '    interval: 30s',
    '    rules:',
    ...alerts.flatMap(a => [
      `      - alert: ${a.uid}`,
      // Grafana's C threshold fires only for values > 0; Prometheus native
      // alert rules fire for *any* returned series, including boolean zero.
      `        expr: ${JSON.stringify(`(${a.expr}) > 0`)}`,
      `        for: ${a.for}`,
      '        labels:',
      '          deployment: squash-bot',
      '          severity: critical',
      ...(a.service ? [`          service_key: ${a.service}`] : []),
    ]),
    ...dashboard.panels.flatMap(panel => panel.targets.flatMap((target, index) => [
      `      - record: squash_dashboard_fixture_${panel.id}_${index}`,
      `        expr: ${JSON.stringify(target.expr)}`,
    ])),
  ].join('\n') + '\n';
  writeFileSync(join(scratch, 'rules.yml'), rules);
  writeFileSync(join(scratch, 'tests.yml'), readFileSync(join(dir, 'alert-fixtures.yml')));
  const env = { PATH: process.env.PATH ?? '/usr/bin:/bin', HOME: process.env.HOME ?? '/tmp' };
  if (process.env.DOCKER_HOST) env.DOCKER_HOST = process.env.DOCKER_HOST;
  if (process.env.DOCKER_CONTEXT) env.DOCKER_CONTEXT = process.env.DOCKER_CONTEXT;
  for (const args of [['check', 'rules', '/fixtures/rules.yml'], ['test', 'rules', '/fixtures/tests.yml']]) {
    const output = execFileSync('docker', ['run', '--rm', '--network', 'none', '--read-only',
      '--tmpfs', '/tmp:rw,noexec,nosuid,size=64m', '-v', `${scratch}:/fixtures:ro`, '--entrypoint', '/bin/promtool', expectedImage, ...args],
    { cwd: root, env, encoding: 'utf8', stdio: ['ignore', 'pipe', 'pipe'], timeout: 120_000 });
    if (output.trim()) console.log(output.trim());
  }
  console.log('Grafana template structure and offline PromQL fixtures: PASS');
} catch (error) {
  console.error('Grafana template/PromQL check: FAIL');
  console.error(error.message);
  process.exitCode = 1;
} finally {
  rmSync(scratch, { recursive: true, force: true });
}

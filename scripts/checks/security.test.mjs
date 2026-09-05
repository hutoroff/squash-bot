import assert from 'node:assert/strict';
import { execFileSync } from 'node:child_process';
import fs from 'node:fs';
import os from 'node:os';
import path from 'node:path';
import test from 'node:test';
import { checkDependencies } from './security.mjs';

function directory(t, assets = true) {
  const root = fs.mkdtempSync(path.join(os.tmpdir(), 'squash-security-test-'));
  t.after(() => fs.rmSync(root, { recursive: true, force: true }));
  if (assets) {
    fs.mkdirSync(path.join(root, 'web/frontend/dist'), { recursive: true });
    fs.writeFileSync(path.join(root, 'web/frontend/dist/index.html'), 'synthetic assets');
  }
  return root;
}

function npmReport(severity) {
  const counts = { info: 0, low: 0, moderate: 0, high: 0, critical: 0, total: 0 };
  const vulnerabilities = {};
  if (severity) {
    counts[severity] = counts.total = 1;
    vulnerabilities.synthetic = { severity, range: '<1.0.1', fixAvailable: true, via: [{ url: 'https://example.com/advisory' }] };
  }
  return JSON.stringify({ auditReportVersion: 2, vulnerabilities, metadata: { vulnerabilities: counts } });
}

test('missing assets block Go analysis without hiding the npm result', t => {
  const calls = [], output = [];
  const status = checkDependencies(directory(t, false), {
    execute: program => { calls.push(program); return { status: 0, stdout: npmReport() }; },
    log: line => output.push(line),
  });
  assert.equal(status, 1);
  assert.deepEqual(calls, ['npm']);
  assert.match(output.join('\n'), /NOT RUN: govulncheck/);
  assert.match(output.join('\n'), /PASS: npm/);
});

test('network/tool failures do not pass or print raw diagnostics; both analyzers are attempted', t => {
  for (const result of [
    { status: 1, stdout: '', stderr: 'synthetic-sensitive-diagnostic' },
    { status: null, error: new Error('synthetic-sensitive-diagnostic') },
    { status: 0, stdout: '{}' },
    { status: 1, stdout: JSON.stringify({ error: { summary: 'synthetic-sensitive-diagnostic' } }) },
  ]) {
    const calls = [], output = [];
    assert.equal(checkDependencies(directory(t), {
      execute: (program, args, options) => {
        calls.push(program);
        assert.equal(options.timeout, 300_000);
        if (program === 'npm') assert.ok(args.includes('--package-lock-only'));
        return result;
      }, log: line => output.push(line),
    }), 1);
    assert.deepEqual(calls, ['go', 'npm']);
    assert.match(output.join('\n'), /FAIL: govulncheck/);
    assert.match(output.join('\n'), /FAIL: npm/);
    assert.ok(!output.join('\n').includes('synthetic-sensitive-diagnostic'));
  }
});

test('vulnerability findings stay failing, lower npm severities remain visible', t => {
  for (const severity of ['high', 'moderate', undefined]) {
    const output = [];
    const status = checkDependencies(directory(t), {
      execute: program => program === 'go' ? { status: 0, stdout: 'No vulnerabilities found.\n' } :
        { status: severity === 'high' ? 1 : 0, stdout: npmReport(severity) },
      log: line => output.push(line),
    });
    assert.equal(status, severity === 'high' ? 1 : 0);
    if (severity) assert.match(output.join('\n'), new RegExp(`${severity}: synthetic`));
  }
  assert.equal(checkDependencies(directory(t), {
    execute: program => program === 'go' ? { status: 1, stdout: '=== Symbol Results ===\nsynthetic vulnerability' } :
      { status: 0, stdout: npmReport() }, log: () => {},
  }), 1);
});

test('repository ignores personal files/evidence without excluding shared resources or templates', t => {
  const root = directory(t, false);
  fs.copyFileSync(new URL('../../.gitignore', import.meta.url), path.join(root, '.gitignore'));
  execFileSync('git', ['-C', root, 'init', '-q']);
  const ignored = [
    '.env', '.env.production', 'tmp/agent/report.txt', '.claude/settings.local.json', '.claude/.credentials.json',
    '.pi/settings.json', '.pi/auth.json', '.pi/sessions/session.jsonl', '.pi/npm/package/index.js', '.pi/git/package/index.js',
    '.codex/config.toml', '.codex/auth.json', '.codex/sessions/session.jsonl',
  ];
  const visible = ['.env.example', '.agents/skills/booking/SKILL.md', '.claude/skills/booking', '.pi/extensions/example.ts', 'cmd/web/webserver/auth_test.go'];
  const output = execFileSync('git', [
    '-C', root, '-c', 'core.excludesFile=/dev/null', 'check-ignore', '--no-index', '--stdin',
  ], { input: [...ignored, ...visible].join('\n') + '\n', encoding: 'utf8' });
  assert.deepEqual(output.trim().split('\n'), ignored);
});

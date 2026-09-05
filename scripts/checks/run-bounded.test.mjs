import assert from 'node:assert/strict';
import { spawn } from 'node:child_process';
import fs from 'node:fs';
import os from 'node:os';
import path from 'node:path';
import { fileURLToPath } from 'node:url';
import test from 'node:test';

const repository = fileURLToPath(new URL('../../', import.meta.url));
const runner = path.join(repository, 'scripts/checks/run-bounded.mjs');

function directory(t) {
  const root = fs.mkdtempSync(path.join(os.tmpdir(), 'squash-deadline-test-'));
  t.after(() => fs.rmSync(root, { recursive: true, force: true }));
  return root;
}

function execute(program, args, options = {}) {
  const child = spawn(program, args, { detached: true, stdio: ['ignore', 'pipe', 'pipe'], ...options });
  let stdout = '', stderr = '';
  child.stdout.on('data', data => { stdout += data; });
  child.stderr.on('data', data => { stderr += data; });
  const done = new Promise((resolve, reject) => {
    const watchdog = setTimeout(() => {
      try { process.kill(-child.pid, 'SIGKILL'); } catch (error) { if (error.code !== 'ESRCH') reject(error); }
      reject(new Error('Test watchdog expired: command did not honor its own deadline'));
    }, 8_000);
    child.once('error', error => { clearTimeout(watchdog); reject(error); });
    child.once('close', (code, signal) => { clearTimeout(watchdog); resolve({ code, signal, stdout, stderr }); });
  });
  return { child, done };
}

function bounded(seconds, script, args = []) {
  return execute(process.execPath, [runner, String(seconds), 'Synthetic check', process.execPath, '-e', script, ...args]);
}

test('preserves argv, streams, exit codes and a noninteractive environment', async () => {
  for (const code of [0, 7]) {
    const result = await bounded(3, `
      const fs = require('node:fs');
      console.log(JSON.stringify({ arg: process.argv[1], ci: process.env.CI, git: process.env.GIT_TERMINAL_PROMPT, stdin: fs.readFileSync(0, 'utf8') }));
      console.error('synthetic stderr');
      process.exitCode = ${code};
    `, ['literal ; $(not-a-command)']).done;
    assert.equal(result.code, code);
    assert.deepEqual(JSON.parse(result.stdout), { arg: 'literal ; $(not-a-command)', ci: '1', git: '0', stdin: '' });
    assert.match(result.stderr, /synthetic stderr/);
  }
});

test('reports missing commands without exposing arguments', async () => {
  const result = await execute(process.execPath, [runner, '1', 'Missing tool', '/does-not-exist/squash-check-tool', 'synthetic-sensitive-argument']).done;
  assert.equal(result.code, 127);
  assert.match(result.stderr, /Missing tool.*ENOENT/);
  assert.ok(!result.stderr.includes('synthetic-sensitive-argument'));
});

test('invalid or unbounded deadlines fail before starting the command', async () => {
  for (const seconds of ['0', '-1', 'NaN', 'Infinity', '86401', '']) {
    const result = await bounded(seconds, "console.log('must-not-start')").done;
    assert.equal(result.code, 2);
    assert.ok(!result.stdout.includes('must-not-start'));
  }
});

function stubbornTree(t) {
  const root = fs.mkdtempSync(path.join(os.tmpdir(), 'squash-deadline-tree-'));
  const ready = path.join(root, 'ready');
  const heartbeat = path.join(root, 'heartbeat');
  // Only clean up this test's newly created process group if the assertion/controller fails.
  t.after(() => {
    if (fs.existsSync(ready)) {
      const { leader } = JSON.parse(fs.readFileSync(ready, 'utf8'));
      try { process.kill(-leader, 'SIGKILL'); } catch (error) { if (error.code !== 'ESRCH') throw error; }
    }
    fs.rmSync(root, { recursive: true, force: true });
  });
  const script = `
    const fs = require('node:fs');
    const { spawn } = require('node:child_process');
    const [ready, heartbeat] = process.argv.slice(1);
    const descendant = spawn(process.execPath, ['-e', \`
      const fs = require('node:fs');
      process.on('SIGTERM', () => {});
      fs.writeFileSync(process.argv[1], 'ready');
      setInterval(() => fs.appendFileSync(process.argv[1], '.'), 20);
    \`, heartbeat], { stdio: 'inherit' });
    fs.writeFileSync(ready, JSON.stringify({ leader: process.pid, descendant: descendant.pid }));
    process.on('SIGTERM', () => process.exit(0));
    setInterval(() => {}, 1000);
  `;
  return { ready, heartbeat, script };
}

async function assertStopped(heartbeat) {
  assert.ok(fs.existsSync(heartbeat), 'descendant did not start');
  const before = fs.readFileSync(heartbeat, 'utf8');
  await new Promise(resolve => setTimeout(resolve, 100));
  assert.equal(fs.readFileSync(heartbeat, 'utf8'), before, 'descendant survived termination');
}

test('timeout stays failed when the leader exits zero and a descendant ignores SIGTERM', async t => {
  const { ready, heartbeat, script } = stubbornTree(t);
  const result = await bounded(1, script, [ready, heartbeat]).done;
  assert.equal(result.code, 124);
  assert.match(result.stderr, /Synthetic check timed out/);
  await assertStopped(heartbeat);
});

test('cancelling the controller also terminates its command group', async t => {
  for (const [signal, code] of [['SIGINT', 130], ['SIGTERM', 143]]) {
    const { ready, heartbeat, script } = stubbornTree(t);
    const { child, done } = bounded(60, script, [ready, heartbeat]);
    const deadline = Date.now() + 3_000;
    while (!fs.existsSync(heartbeat) && Date.now() < deadline) {
      await new Promise(resolve => setTimeout(resolve, 20));
    }
    child.kill(signal);
    const result = await done;
    assert.equal(result.code, code);
    assert.match(result.stderr, /interrupted/);
    await assertStopped(heartbeat);
  }
});

function checkout(t) {
  const root = directory(t);
  const write = (name, content, mode = 0o644) => {
    const target = path.join(root, name);
    fs.mkdirSync(path.dirname(target), { recursive: true });
    fs.writeFileSync(target, content, { mode });
  };
  for (const file of ['Makefile', 'scripts/checks/doctor.sh', 'scripts/checks/check.sh', 'scripts/checks/check-fast.sh', 'scripts/checks/bootstrap.sh', 'scripts/checks/run-bounded.mjs']) {
    const source = path.join(repository, file);
    if (fs.existsSync(source)) write(file, fs.readFileSync(source), file.endsWith('.sh') ? 0o755 : 0o644);
  }
  write('go.mod', 'module synthetic\n\ngo 1.25.0\n');
  write('web/frontend/.node-version', process.versions.node.split('.')[0]);
  fs.mkdirSync(path.join(root, 'web/frontend/src'), { recursive: true });
  for (const file of ['index.html', 'package.json', 'package-lock.json', 'tsconfig.json', 'tsconfig.node.json', 'vite.config.ts']) write(`web/frontend/${file}`, 'synthetic');
  write('web/frontend/dist/index.html', 'synthetic assets');
  write('scripts/checks/fixture.test.mjs', '');
  write('tools/git', '#!/bin/sh\nexit 0\n', 0o755);
  write('tools/gofmt', '#!/bin/sh\nexit 0\n', 0o755);
  write('tools/go', '#!/bin/sh\nif [ "$1" = env ]; then echo go1.26.6; fi\nexit 0\n', 0o755);
  write('tools/npm', '#!/bin/sh\nif [ "$1" = --version ]; then echo 10.9.2; fi\nexit 0\n', 0o755);
  return { root, write, env: { ...process.env, PATH: `${path.join(root, 'tools')}${path.delimiter}${process.env.PATH}` } };
}

test('make doctor bounds a stalled Docker probe', async t => {
  const { root, write, env } = checkout(t);
  write('tools/docker', '#!/bin/sh\necho called > docker-probed\nexec /bin/sleep 3\n', 0o755);
  const result = await execute('make', ['doctor', 'DOCTOR_TIMEOUT=1'], { cwd: root, env }).done;
  assert.ok(fs.existsSync(path.join(root, 'docker-probed')), 'doctor did not reach Docker');
  assert.notEqual(result.code, 0);
  assert.match(result.stderr, /timed out/);
});

for (const [phase, name] of [['ci', 'install'], ['run', 'frontend build']]) {
  test(`make bootstrap bounds a stalled ${name} before later work can run`, async t => {
    const { root, write, env } = checkout(t);
    write('tools/npm', `#!/bin/sh\nif [ "$3" = "${phase}" ]; then echo started > phase-started; /bin/sleep 3; fi\nif [ "$3" = run ]; then echo completed > built; fi\n`, 0o755);
    const result = await execute('make', ['bootstrap', 'BOOTSTRAP_TIMEOUT=1'], { cwd: root, env }).done;
    assert.ok(fs.existsSync(path.join(root, 'phase-started')), `${name} did not start`);
    assert.notEqual(result.code, 0);
    assert.match(result.stderr, /timed out/);
    assert.ok(!fs.existsSync(path.join(root, 'built')));
  });
}

test('make check bounds a stalled Go build before later checks can run', async t => {
  const { root, write, env } = checkout(t);
  write('tools/go', '#!/bin/sh\necho started > build-started\n/bin/sleep 3\necho called > after-build\n', 0o755);
  const result = await execute('make', ['check', 'CHECK_TIMEOUT=1'], { cwd: root, env }).done;
  assert.ok(fs.existsSync(path.join(root, 'build-started')), 'Go build did not start');
  assert.notEqual(result.code, 0);
  assert.match(result.stderr, /timed out/);
  assert.ok(!fs.existsSync(path.join(root, 'after-build')));
});

test('make check-fast bounds a stalled Go vet before later checks can run', async t => {
  const { root, write, env } = checkout(t);
  write('tools/go', '#!/bin/sh\necho started > vet-started\n/bin/sleep 3\necho called > after-vet\n', 0o755);
  const result = await execute('make', ['check-fast', 'CHECK_FAST_TIMEOUT=1'], { cwd: root, env }).done;
  assert.ok(fs.existsSync(path.join(root, 'vet-started')), 'Go vet did not start');
  assert.notEqual(result.code, 0);
  assert.match(result.stderr, /timed out/);
  assert.ok(!fs.existsSync(path.join(root, 'after-vet')));
});

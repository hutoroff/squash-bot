import assert from 'node:assert/strict';
import { spawnSync } from 'node:child_process';
import fs from 'node:fs';
import os from 'node:os';
import path from 'node:path';
import { fileURLToPath } from 'node:url';
import test from 'node:test';

const root = fileURLToPath(new URL('../../', import.meta.url));
const script = path.join(root, 'scripts/staging-up.sh');

test('staging wrapper fails closed and only executes the isolated staging project', t => {
  const dir = fs.mkdtempSync(path.join(os.tmpdir(), 'squash-staging-wrapper-'));
  t.after(() => fs.rmSync(dir, { recursive: true, force: true }));
  const bin = path.join(dir, 'bin');
  fs.mkdirSync(bin);
  const envFile = path.join(dir, 'staging.env');
  fs.writeFileSync(envFile, 'synthetic fixture only\n', { mode: 0o600 });
  const log = path.join(dir, 'docker-commands');
  const badConfig = path.join(dir, 'bad-config');
  const remoteContext = path.join(dir, 'remote-context');
  fs.writeFileSync(path.join(bin, 'docker'), `#!/bin/sh
# Simulate a Docker CLI without the context show subcommand.
if [ "$1 $2" = 'context show' ]; then echo 'Usage: docker context COMMAND' >&2; exit 1; fi
if [ "$1 $2 $3" = 'context ls --format' ]; then
  if [ -e ${JSON.stringify(remoteContext)} ]; then echo production; else printf '\\ndefault\\n\\n'; fi
  exit 0
fi
if [ "$1 $2 $3" = 'compose version --short' ]; then echo 5.1.1; exit 0; fi
[ -z "${'${STAGING_TEST_UNTRUSTED:-}'}" ] || exit 98
[ "$STAGING_REV" = fixture-revision ] || exit 97
printf '%s\\n' "$*" >> ${JSON.stringify(log)}
case "$*" in *'config --quiet'*) [ ! -e ${JSON.stringify(badConfig)} ];; *'up --build --detach --wait'*) exit 0;; *) exit 99;; esac
`, { mode: 0o755 });
  fs.writeFileSync(path.join(bin, 'uname'), '#!/bin/sh\necho Linux\n', { mode: 0o755 });
  fs.writeFileSync(path.join(bin, 'git'), `#!/bin/sh
case "$1" in status) [ "$STAGING_TEST_DIRTY" = yes ] && echo ' M fixture';; rev-parse) echo fixture-revision;; *) exit 99;; esac
`, { mode: 0o755 });
  const base = { ...process.env, PATH: `${bin}:${process.env.PATH}`, DOCKER_HOST: '', DOCKER_CONTEXT: '', DOCKER_CONFIG: '',
    STAGING_ENV_FILE: envFile, STAGING_DEDICATED_HOST: 'yes', STAGING_TEST_DIRTY: 'no',
    STAGING_TEST_UNTRUSTED: 'must-not-reach-compose', STAGING_REV: 'wrong-revision' };
  const invoke = extra => spawnSync('sh', [script], { cwd: root, env: { ...base, ...extra }, encoding: 'utf8', timeout: 10_000 });

  assert.notEqual(invoke({ STAGING_DEDICATED_HOST: '' }).status, 0);
  assert.equal(fs.existsSync(log), false, 'no Docker calls without dedicated-host confirmation');
  assert.notEqual(invoke({ DOCKER_HOST: 'tcp://synthetic.test:2375' }).status, 0);
  assert.equal(fs.existsSync(log), false, 'remote Docker refused before config');
  fs.writeFileSync(remoteContext, '');
  assert.match(invoke().stderr, /use the local default Docker context/);
  assert.equal(fs.existsSync(log), false, 'non-default active context refused before config');
  fs.rmSync(remoteContext);
  assert.notEqual(invoke({ STAGING_TEST_DIRTY: 'yes' }).status, 0);
  assert.equal(fs.existsSync(log), false, 'dirty candidate refused before config');
  fs.writeFileSync(badConfig, '');
  assert.notEqual(invoke().status, 0);
  assert.equal(fs.readFileSync(log, 'utf8').split('\n').filter(Boolean).length, 1, 'bad config cannot start app');
  fs.rmSync(badConfig);
  fs.writeFileSync(log, '');

  const deployed = invoke();
  assert.equal(deployed.status, 0, deployed.stderr);
  const commands = fs.readFileSync(log, 'utf8').split('\n').filter(Boolean);
  assert.equal(commands.length, 2);
  for (const command of commands) {
    assert.match(command, /--project-name squash-staging/);
    assert.match(command, /docker-compose\.staging\.yml/);
    assert.doesNotMatch(command, /docker-compose\.prod\.yml|\bpull\b|\bpush\b/);
  }
  assert.match(commands[0], /config --quiet$/);
  assert.match(commands[1], /up --build --detach --wait$/);
});

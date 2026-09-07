import assert from 'node:assert/strict';
import { spawnSync } from 'node:child_process';
import fs from 'node:fs';
import os from 'node:os';
import path from 'node:path';
import { fileURLToPath } from 'node:url';
import test from 'node:test';

const repository = fileURLToPath(new URL('../../', import.meta.url));
const healthcheck = path.join(repository, 'scripts/healthcheck.sh');

test('booking health check uses authenticated admin readiness without expanding the token on the host', t => {
  const root = fs.mkdtempSync(path.join(os.tmpdir(), 'squash-healthcheck-test-'));
  t.after(() => fs.rmSync(root, { recursive: true, force: true }));
  const bin = path.join(root, 'bin');
  const log = path.join(root, 'docker.log');
  fs.mkdirSync(bin);
  fs.writeFileSync(path.join(bin, 'docker'), `#!/bin/sh
printf '%s\\n' "$*" >> "$FAKE_DOCKER_LOG"
if [ "$1" = compose ]; then
  for arg in "$@"; do service="$arg"; done
  printf '%s-id\\n' "$service"
  exit 0
fi
if [ "$1" = inspect ]; then
  printf 'true\\n'
  exit 0
fi
exit 0
`, { mode: 0o755 });
  fs.writeFileSync(path.join(bin, 'curl'), '#!/bin/sh\nexit 99\n', { mode: 0o755 });

  const result = spawnSync('bash', [healthcheck], {
    cwd: repository,
    encoding: 'utf8',
    env: {
      ...process.env,
      PATH: `${bin}:${process.env.PATH}`,
      FAKE_DOCKER_LOG: log,
      HEALTHCHECK_BOT_TOKEN: 'synthetic-bot-token',
      HEALTHCHECK_CHAT_ID: 'synthetic-chat-id',
    },
  });
  assert.equal(result.status, 0, result.stderr);
  const commands = fs.readFileSync(log, 'utf8');
  assert.match(commands, /exec booking-id sh -c .*BOOKING_MONITORING_SECRET.*BOOKING_ADMIN_PORT:-9091.*\/health\/ready/);
  assert.ok(commands.includes('$BOOKING_MONITORING_SECRET'), 'monitoring token must expand inside the container');
  assert.doesNotMatch(commands, /synthetic-monitoring-secret/);
  assert.match(commands, /exec management-id wget -qO- http:\/\/localhost:8080\/health/);
});

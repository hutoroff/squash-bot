import assert from 'node:assert/strict';
import fs from 'node:fs';
import test from 'node:test';

for (const service of ['management', 'telegram', 'booking', 'web']) {
  test(`Dockerfile.${service} builds the complete main package`, () => {
    const dockerfile = fs.readFileSync(new URL(`../../Dockerfile.${service}`, import.meta.url), 'utf8');
    const buildLine = dockerfile.split('\n').find(line => line.startsWith('RUN CGO_ENABLED=0 GOOS=linux go build '));
    assert.ok(buildLine, 'missing Linux Go build stage');
    assert.ok(buildLine.includes('-ldflags="-X main.Version=${VERSION}"'), 'release version must be embedded');
    assert.ok(buildLine.endsWith(`-o /${service} ./cmd/${service}`), `must build ./cmd/${service}, not main.go alone`);
  });
}

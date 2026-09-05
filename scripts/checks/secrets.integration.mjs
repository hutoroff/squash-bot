// Explicit opt-in: downloads/runs pinned Gitleaks, never part of the offline fast loop.
import assert from 'node:assert/strict';
import { execFileSync } from 'node:child_process';
import fs from 'node:fs';
import os from 'node:os';
import path from 'node:path';
import test from 'node:test';
import { checkSecrets } from './secrets.mjs';

test('pinned Gitleaks detects synthetic index, worktree, untracked and template secrets without leaking them', { timeout: 360_000 }, t => {
  const root = fs.mkdtempSync(path.join(os.tmpdir(), 'squash-gitleaks-smoke-'));
  t.after(() => fs.rmSync(root, { recursive: true, force: true }));
  const git = (...args) => execFileSync('git', ['-C', root, ...args], { stdio: 'pipe' });
  git('init', '-q');
  git('config', 'core.excludesFile', '/dev/null');
  // Deliberately fake token, assembled to keep this test's source free of a literal secret.
  const token = ['ghp', 'aB3dE5fG7hJ9kL2mN4pQ6rS8tU0vW1xY3zA5'].join('_');
  fs.writeFileSync(path.join(root, 'partial.txt'), `token = "${token}"\n`);
  git('add', 'partial.txt');
  fs.writeFileSync(path.join(root, 'partial.txt'), 'safe worktree version\n');
  fs.writeFileSync(path.join(root, 'dirty.txt'), 'safe staged version\n');
  git('add', 'dirty.txt');
  fs.writeFileSync(path.join(root, 'dirty.txt'), `token = "${token}" // gitleaks:allow\n`);
  for (const name of ['untracked.txt', '.env.example', 'behavior_test.go']) {
    fs.writeFileSync(path.join(root, name), `token = "${token}"\n`);
  }
  const output = [];
  assert.equal(checkSecrets(root, { log: line => output.push(line) }), 1, output.join('\n'));
  for (const location of ['index "partial.txt":1', 'worktree "dirty.txt":1', 'worktree "untracked.txt":1', 'worktree ".env.example":1', 'worktree "behavior_test.go":1']) {
    assert.ok(output.some(line => line.includes(location)), `Missing ${location}; ${output.join('\n')}`);
  }
  assert.ok(!output.join('\n').includes(token));
  git('rm', '--cached', '-f', 'partial.txt');
  for (const name of ['dirty.txt', 'untracked.txt', '.env.example', 'behavior_test.go']) {
    fs.writeFileSync(path.join(root, name), 'safe replacement\n');
  }
  output.length = 0;
  assert.equal(checkSecrets(root, { log: line => output.push(line) }), 0, output.join('\n'));
  assert.match(output.join('\n'), /PASS: Gitleaks/);
});

import assert from 'node:assert/strict';
import { execFileSync } from 'node:child_process';
import fs from 'node:fs';
import os from 'node:os';
import path from 'node:path';
import test from 'node:test';
import { candidates, checkSecrets, runGitleaks } from './secrets.mjs';

function repository(t) {
  const root = fs.mkdtempSync(path.join(os.tmpdir(), 'squash-secrets-test-'));
  t.after(() => fs.rmSync(root, { recursive: true, force: true }));
  const git = (...args) => execFileSync('git', ['-C', root, ...args], { stdio: 'pipe' });
  git('init', '-q');
  git('config', 'core.excludesFile', '/dev/null');
  const write = (name, contents) => {
    fs.mkdirSync(path.dirname(path.join(root, name)), { recursive: true });
    fs.writeFileSync(path.join(root, name), contents);
  };
  return { root, git, write };
}

// Fixtures stay synthetic and local. No test contacts a token provider or creates a real commit.
test('collects distinct index/worktree snapshots, untracked paths, templates, and odd filenames', t => {
  const { root, git, write } = repository(t);
  write('partial.txt', 'staged value');
  write('missing.txt', 'staged then removed');
  git('add', '.');
  write('partial.txt', 'unstaged value');
  fs.unlinkSync(path.join(root, 'missing.txt'));
  write('odd\n name.txt', 'untracked');
  write('.env.example', 'template');
  write('testdata/example_test.go', 'test fixture');
  const files = candidates(root);
  assert.deepEqual(files.map(f => [f.snapshot, f.name, f.content.toString()]).sort(), [
    ['index', 'partial.txt', 'staged value'], ['index', 'missing.txt', 'staged then removed'],
    ['worktree', 'partial.txt', 'unstaged value'], ['worktree', 'odd\n name.txt', 'untracked'],
    ['worktree', '.env.example', 'template'], ['worktree', 'testdata/example_test.go', 'test fixture'],
  ].sort());
});

test('honors repository exclusions but does not hide forced tracked credential paths', t => {
  const { root, git, write } = repository(t);
  write('.gitignore', '.env\n.env.*\n!.env.example\ntmp/\n.claude/settings.local.json\n');
  write('.env', 'synthetic private content');
  write('tmp/report.txt', 'synthetic evidence');
  write('.claude/settings.local.json', '{}');
  assert.deepEqual(candidates(root).map(f => f.name), ['.gitignore']);
  git('add', '-f', '.env');
  assert.throws(() => candidates(root), /Private\/credential candidate.*contents were not read/);
});

test('scans link text without following file or directory symlinks', t => {
  const { root, git, write } = repository(t);
  fs.symlinkSync('/does-not-exist/private.env', path.join(root, 'link'));
  assert.equal(candidates(root)[0].content.toString(), '/does-not-exist/private.env');
  write('nested/file.txt', 'original');
  git('add', 'nested/file.txt');
  fs.rmSync(path.join(root, 'nested'), { recursive: true });
  fs.symlinkSync(os.tmpdir(), path.join(root, 'nested'));
  // A directory symlink is treated as link text, not recursively scanned.
  assert.ok(candidates(root).every(f => f.name !== 'nested/file.txt' || f.snapshot === 'index'));
});

test('renames and deletions inspect only surviving index blobs', t => {
  const { root, git, write } = repository(t);
  write('old.txt', 'content');
  git('add', 'old.txt');
  git('mv', 'old.txt', 'new.txt');
  assert.deepEqual(candidates(root).map(f => f.name), ['new.txt']);
  git('rm', '-f', 'new.txt');
  assert.deepEqual(candidates(root), []);
});

test('fails closed for an unmerged index', t => {
  const { root, git, write } = repository(t);
  write('conflict.txt', 'synthetic');
  const blob = git('hash-object', '-w', 'conflict.txt').toString().trim();
  execFileSync('git', ['-C', root, 'update-index', '--index-info'], {
    input: `100644 ${blob} 1\tconflict.txt\n100644 ${blob} 2\tconflict.txt\n`, stdio: ['pipe', 'pipe', 'pipe'],
  });
  assert.throws(() => candidates(root), /Unmerged/);
});

test('outputs locations only, keeps raw content private, and removes temporary snapshots', t => {
  const { root, write } = repository(t);
  const secret = 'synthetic-sensitive-value';
  write('candidate.txt', secret);
  const output = [];
  let temporary;
  const status = checkSecrets(root, {
    log: line => output.push(line),
    scan: directory => {
      temporary = directory;
      assert.equal(fs.readFileSync(path.join(directory, 'candidates/worktree/candidate.txt'), 'utf8'), secret);
      assert.equal(fs.statSync(directory).mode & 0o777, 0o700);
      return [{ File: 'candidates/worktree/candidate.txt', RuleID: 'test-rule', StartLine: 1, Secret: secret, Match: secret }];
    },
  });
  assert.equal(status, 1);
  assert.match(output.join('\n'), /worktree "candidate.txt":1 \[test-rule\]/);
  assert.ok(!output.join('\n').includes(secret));
  assert.ok(!fs.existsSync(temporary));
});

test('scanner exceptions and malformed reports cannot pass or disclose diagnostics', t => {
  const { root, write } = repository(t);
  write('candidate.txt', 'safe');
  for (const scan of [
    () => { throw new Error('synthetic-sensitive-diagnostic'); },
    () => null,
    () => [{ File: 'outside', RuleID: 'test-rule', StartLine: 1 }],
  ]) {
    const output = [];
    assert.equal(checkSecrets(root, { scan, log: line => output.push(line) }), 1);
    assert.match(output.join('\n'), /FAIL: secret scan incomplete/);
    assert.ok(!output.join('\n').includes('synthetic-sensitive-diagnostic'));
  }
});

test('missing Go executable is a scanner failure, not an empty clean report', t => {
  const { root } = repository(t);
  const previous = process.env.PATH;
  process.env.PATH = root;
  try {
    assert.throws(() => runGitleaks(root), /go failed or timed out/);
  } finally {
    process.env.PATH = previous;
  }
});

test('an empty candidate set does not launch the scanner or imply history was scanned', t => {
  const { root } = repository(t);
  const output = [];
  assert.equal(checkSecrets(root, { scan: () => assert.fail('unexpected scan'), log: line => output.push(line) }), 0);
  assert.match(output.join('\n'), /no local.*committed history was not scanned/);
});

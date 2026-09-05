import { spawnSync } from 'node:child_process';
import fs from 'node:fs';
import os from 'node:os';
import path from 'node:path';
import { fileURLToPath, pathToFileURL } from 'node:url';

export const gitleaksModule = 'github.com/zricethezav/gitleaks/v8@v8.30.0';
const configPath = fileURLToPath(new URL('./gitleaks.toml', import.meta.url));
const maxBytes = 32 * 1024 * 1024;
class CheckError extends Error {}

// Never forward child output on failure: Git/scanner diagnostics can contain file contents.
function command(cwd, program, args, timeout = 30_000) {
  const result = spawnSync(program, args, {
    cwd, encoding: 'buffer', timeout, maxBuffer: maxBytes,
    env: { ...process.env, CI: '1', GIT_TERMINAL_PROMPT: '0' },
  });
  if (result.error || result.status !== 0) {
    throw new CheckError(`${program} failed or timed out; raw diagnostics withheld (may contain secrets)`);
  }
  return result.stdout;
}

function git(root, ...args) {
  return command(root, 'git', ['--literal-pathspecs', ...args]);
}

function names(output) {
  const text = output.toString('utf8');
  if (!Buffer.from(text).equals(output)) throw new CheckError('Non-UTF-8 candidate path; scan not completed');
  return text.split('\0').filter(Boolean);
}

function validatePath(name) {
  if (path.isAbsolute(name) || name.split('/').some(part => ['', '.', '..', '.git'].includes(part))) {
    throw new CheckError('Unsafe candidate path; scan not completed');
  }
  const base = path.basename(name);
  if (((base === '.env' || base.startsWith('.env.')) && base !== '.env.example') ||
      /\.(pem|key|p12|pfx)$/.test(base) ||
      /(^|\/)(\.pi|\.codex)\/(auth\.json|settings\.json|config\.toml)$/.test(name) ||
      /(^|\/)\.claude\/(settings\.local\.json|\.credentials\.json)$/.test(name)) {
    throw new CheckError(`Private/credential candidate ${JSON.stringify(name)}: remove it from the change set; contents were not read`);
  }
}

function worktreeContent(root, name) {
  // Git lists symlinks without traversing them. Also reject replaced parent directories.
  let current = root;
  for (const part of name.split('/').slice(0, -1)) {
    current = path.join(current, part);
    if (!fs.lstatSync(current).isDirectory()) throw new CheckError('Non-directory candidate parent; scan not completed');
  }
  const target = path.join(root, name);
  const stat = fs.lstatSync(target);
  if (stat.isSymbolicLink()) return Buffer.from(fs.readlinkSync(target));
  if (!stat.isFile() || stat.size > maxBytes) throw new CheckError('Unsupported or oversized candidate; scan not completed');
  const fd = fs.openSync(target, fs.constants.O_RDONLY | fs.constants.O_NOFOLLOW);
  try {
    return fs.readFileSync(fd);
  } finally {
    fs.closeSync(fd);
  }
}

export function candidates(root) {
  const staged = names(git(root, 'diff', '--cached', '--name-only', '-z', '--no-renames', '--diff-filter=ACMT'));
  const working = new Set([
    ...names(git(root, 'diff', '--name-only', '-z', '--no-renames', '--diff-filter=ACMT')),
    ...names(git(root, 'ls-files', '--others', '--exclude-standard', '-z')),
  ]);
  // Validate all names before reading any candidate, including force-added ignored files.
  [...staged, ...working].forEach(validatePath);
  const result = [];
  for (const name of staged) {
    const entries = names(git(root, 'ls-files', '--stage', '-z', '--', name));
    if (entries.length !== 1) throw new CheckError('Unmerged or missing index entry; scan not completed');
    const match = /^(100644|100755|120000) ([a-f0-9]+) 0\t/.exec(entries[0]);
    if (!match) throw new CheckError('Unsupported index entry; scan not completed');
    result.push({ snapshot: 'index', name, content: git(root, 'cat-file', 'blob', match[2]) });
  }
  if (git(root, 'ls-files', '--unmerged', '-z').length) throw new CheckError('Unmerged index; scan not completed');
  for (const name of working) {
    result.push({ snapshot: 'worktree', name, content: worktreeContent(root, name) });
  }
  return result;
}

export function runGitleaks(directory) {
  // An explicit config, empty ignore file, and isolated cwd avoid implicit local overrides.
  // go run uses the pinned module in Go's cache; it does not change go.mod or install globally.
  const output = command(directory, 'go', [
    'run', gitleaksModule, 'dir', 'candidates',
    '--config', 'gitleaks.toml', '--gitleaks-ignore-path', '.gitleaksignore',
    '--ignore-gitleaks-allow', '--redact=100', '--no-banner', '--no-color',
    '--log-level=error', '--report-format=json', '--report-path=-', '--exit-code=0', '--timeout=120',
  ], 300_000);
  // Findings use exit 0 intentionally: go run otherwise collapses findings and scanner errors.
  // A successful command MUST also supply a valid JSON report; absence is never a clean scan.
  try {
    const report = JSON.parse(output.toString('utf8'));
    if (!Array.isArray(report)) throw new Error();
    return report;
  } catch {
    throw new CheckError('Invalid or missing Gitleaks report; scan not completed');
  }
}

export function checkSecrets(root, { scan = runGitleaks, log = console.log } = {}) {
  let temporary;
  try {
    const files = candidates(root);
    if (!files.length) {
      log('PASS: no local secret-scan candidates (committed history was not scanned)');
      return 0;
    }
    temporary = fs.mkdtempSync(path.join(os.tmpdir(), 'squash-secrets-'));
    fs.mkdirSync(path.join(temporary, 'candidates'));
    fs.copyFileSync(configPath, path.join(temporary, 'gitleaks.toml'));
    fs.writeFileSync(path.join(temporary, '.gitleaksignore'), '', { mode: 0o600 });
    const known = new Map();
    for (const file of files) {
      const relative = path.join('candidates', file.snapshot, file.name);
      const destination = path.join(temporary, relative);
      fs.mkdirSync(path.dirname(destination), { recursive: true, mode: 0o700 });
      fs.writeFileSync(destination, file.content, { mode: 0o600 });
      known.set(relative, file);
    }
    const findings = scan(temporary);
    if (!Array.isArray(findings)) throw new CheckError('Invalid Gitleaks findings; scan not completed');
    // Validate the entire report before printing. Never print Match, Secret, context or raw errors.
    const locations = findings.map(finding => {
      const relative = path.relative(temporary, path.resolve(temporary, finding.File ?? ''));
      const file = known.get(relative);
      if (!file || !Number.isInteger(finding.StartLine) || finding.StartLine < 1 ||
          !/^[a-z0-9-]{1,80}$/.test(finding.RuleID ?? '')) {
        throw new CheckError('Invalid Gitleaks finding metadata; scan not completed');
      }
      return `${file.snapshot} ${JSON.stringify(file.name)}:${finding.StartLine} [${finding.RuleID}]`;
    });
    for (const location of locations) log(`FINDING: ${location}`);
    if (findings.length) {
      log(`FAIL: ${findings.length} secret finding(s); remove real secrets or review narrowly scoped synthetic-fixture exceptions`);
      return 1;
    }
    log(`PASS: Gitleaks ${gitleaksModule.split('@').at(-1)} scanned ${files.length} local candidate snapshot(s)`);
    return 0;
  } catch (error) {
    const detail = error instanceof CheckError ? error.message : 'operation failed; raw diagnostics withheld (may contain secrets)';
    log(`FAIL: secret scan incomplete — ${detail}`);
    return 1;
  } finally {
    if (temporary) fs.rmSync(temporary, { recursive: true, force: true });
  }
}

if (process.argv[1] && import.meta.url === pathToFileURL(path.resolve(process.argv[1])).href) {
  const root = fileURLToPath(new URL('../../', import.meta.url));
  process.exitCode = checkSecrets(root);
}

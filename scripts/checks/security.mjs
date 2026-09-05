import { spawnSync } from 'node:child_process';
import fs from 'node:fs';
import path from 'node:path';
import { fileURLToPath, pathToFileURL } from 'node:url';
import { checkSecrets } from './secrets.mjs';

export function checkDependencies(root, { execute = spawnSync, log = console.log } = {}) {
  let status = 0;
  const options = { cwd: root, encoding: 'utf8', timeout: 300_000, maxBuffer: 32 * 1024 * 1024, env: { ...process.env, CI: '1' } };
  log('\n==> Pinned Go vulnerability analysis (govulncheck v1.7.0)');
  if (!fs.existsSync(path.join(root, 'web/frontend/dist/index.html'))) {
    log('NOT RUN: govulncheck requires embedded frontend assets; run make bootstrap');
    status = 1;
  } else {
    const result = execute('go', ['run', 'golang.org/x/vuln/cmd/govulncheck@v1.7.0', './...'], options);
    // Only show the analyzer's normal report, not download/compiler diagnostics.
    const report = result.stdout?.trim() ?? '';
    if (!result.error && (report.startsWith('=== Symbol Results ===') || report === 'No vulnerabilities found.')) {
      log(report);
      log(result.status === 0 ? 'PASS: govulncheck' : 'FAIL: govulncheck reported vulnerabilities');
      if (result.status !== 0) status = 1;
    } else {
      log('FAIL: govulncheck unavailable, timed out, or produced no valid report; check Go, assets and advisory-network access');
      status = 1;
    }
  }

  log('\n==> Locked frontend dependency audit (all dependencies; high/critical fail)');
  const result = execute('npm', [
    '--prefix', 'web/frontend', 'audit', '--package-lock-only', '--json', '--audit-level=high',
    '--fetch-retries=0', '--fetch-timeout=60000',
  ], options);
  try {
    if (result.error || ![0, 1].includes(result.status)) throw new Error();
    const report = JSON.parse(result.stdout);
    const counts = report.metadata?.vulnerabilities;
    if (report.error || report.auditReportVersion !== 2 || !report.vulnerabilities || !counts ||
        !['info', 'low', 'moderate', 'high', 'critical', 'total'].every(key => Number.isInteger(counts[key]) && counts[key] >= 0) ||
        Object.keys(report.vulnerabilities).length !== counts.total) throw new Error();
    for (const [name, entry] of Object.entries(report.vulnerabilities)) {
      const fix = entry.fixAvailable === true ? 'available; review lockfile update' :
        entry.fixAvailable?.name ? `${entry.fixAvailable.name}@${entry.fixAvailable.version}${entry.fixAvailable.isSemVerMajor ? ' (major upgrade)' : ''}` : 'not supplied by npm';
      log(`${entry.severity}: ${name} ${entry.range}; fix: ${fix}`);
      for (const advisory of entry.via) {
        if (typeof advisory !== 'string') log(`  ${advisory.url}`);
      }
    }
    log(`npm finding counts: ${JSON.stringify(report.metadata.vulnerabilities)}`);
    const blocking = report.metadata.vulnerabilities.high + report.metadata.vulnerabilities.critical;
    if (result.status !== 0 || blocking > 0) {
      log('FAIL: npm audit reported blocking vulnerabilities');
      status = 1;
    } else {
      log('PASS: npm audit threshold (low/moderate findings above remain open, not waived)');
    }
  } catch {
    log('FAIL: npm audit unavailable, timed out, or produced no valid report; check npm, lockfile and registry-network access');
    status = 1;
  }
  return status;
}

if (process.argv[1] && import.meta.url === pathToFileURL(path.resolve(process.argv[1])).href) {
  const root = fileURLToPath(new URL('../../', import.meta.url));
  console.log('==> Prospective-change secret scan');
  const secrets = checkSecrets(root);
  const dependencies = checkDependencies(root);
  process.exitCode = secrets || dependencies;
  console.log(process.exitCode ? '\nFAIL: security checks have findings or incomplete verification; see docs/security-checks.md' : '\nPASS: security checks (within the documented scope and thresholds)');
}

import { spawn } from 'node:child_process';
import { constants } from 'node:os';

// One controller per make entrypoint. Nested shell helpers inherit this command
// group and deadline; they must not start another detached controller.
const [secondsText, label, command, ...args] = process.argv.slice(2);
const seconds = Number(secondsText);
if (!secondsText || !Number.isFinite(seconds) || seconds <= 0 || seconds > 86400 || !label || !command) {
  console.error('Usage: node scripts/checks/run-bounded.mjs <seconds: 0 < n <= 86400> <label> <command> [args...]');
  process.exitCode = 2;
} else if (process.platform === 'win32') {
  console.error('FAIL: bounded checks require POSIX process groups (macOS or Linux)');
  process.exitCode = 2;
} else {
  process.exitCode = await run();
}

function run() {
  return new Promise(resolve => {
    let deadline, escalation;
    let stopped = false;
    let finished = false;
    const child = spawn(command, args, {
      detached: true,
      // No shell interpretation, no interactive stdin, and no raw argv in diagnostics.
      stdio: ['ignore', 'inherit', 'inherit'],
      env: { ...process.env, CI: '1', GIT_TERMINAL_PROMPT: '0' },
    });

    function signalGroup(signal) {
      if (!child.pid) return;
      try {
        process.kill(-child.pid, signal);
      } catch (error) {
        if (error.code !== 'ESRCH') console.error(`FAIL: ${label}: could not signal its command group`);
      }
    }

    function finish(code) {
      if (finished) return;
      finished = true;
      clearTimeout(deadline);
      clearTimeout(escalation);
      process.off('SIGINT', interrupt);
      process.off('SIGTERM', terminate);
      child.unref();
      resolve(code);
    }

    function stop(code, message) {
      if (stopped || finished) return;
      stopped = true;
      clearTimeout(deadline);
      console.error(`FAIL: ${label} ${message}; terminating its command group`);
      signalGroup('SIGTERM');
      // Keep the escalation even if the leader exits successfully on SIGTERM:
      // descendants may still be running, ignoring SIGTERM, or holding pipes open.
      escalation = setTimeout(() => {
        signalGroup('SIGKILL');
        finish(code);
      }, 1000);
    }

    const interrupt = () => stop(130, 'interrupted (SIGINT)');
    const terminate = () => stop(143, 'interrupted (SIGTERM)');
    process.on('SIGINT', interrupt);
    process.on('SIGTERM', terminate);
    deadline = setTimeout(() => stop(124, `timed out after ${seconds}s`), seconds * 1000);

    child.once('error', error => {
      // Do not let an error race turn a timeout/cancellation into another result.
      if (stopped) return;
      const reason = ['ENOENT', 'EACCES', 'ENOEXEC'].includes(error.code) ? error.code : 'spawn error';
      console.error(`FAIL: ${label} could not start (${reason})`);
      finish(error.code === 'ENOENT' ? 127 : 126);
    });
    child.once('exit', (code, signal) => {
      if (!stopped) finish(code ?? 128 + (constants.signals[signal] ?? 1));
    });
  });
}

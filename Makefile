SHELL := /bin/sh

# Overall entrypoint deadlines, in seconds; includes downloads/builds/test setup.
DOCTOR_TIMEOUT ?= 30
BOOTSTRAP_TIMEOUT ?= 600
CHECK_FAST_TIMEOUT ?= 900
CHECK_TIMEOUT ?= 1800
CHECK_SECRETS_TIMEOUT ?= 600
CHECK_SECURITY_TIMEOUT ?= 1200
BOUNDED = node scripts/checks/run-bounded.mjs

.PHONY: doctor bootstrap check-fast check check-secrets check-security

doctor:
	@command -v node >/dev/null 2>&1 || { printf 'ERROR: Node is required for bounded prerequisite checks; see web/frontend/.node-version.\n' >&2; exit 1; }
	@$(BOUNDED) "$(DOCTOR_TIMEOUT)" "Prerequisite checks" ./scripts/checks/doctor.sh

bootstrap:
	@$(BOUNDED) "$(BOOTSTRAP_TIMEOUT)" "Frontend bootstrap" sh scripts/checks/bootstrap.sh

check-fast:
	@$(BOUNDED) "$(CHECK_FAST_TIMEOUT)" "Fast verification" ./scripts/checks/check-fast.sh

check:
	@$(BOUNDED) "$(CHECK_TIMEOUT)" "Full verification" ./scripts/checks/check.sh

check-secrets:
	@$(BOUNDED) "$(CHECK_SECRETS_TIMEOUT)" "Secret scan" node scripts/checks/secrets.mjs

check-security:
	@$(BOUNDED) "$(CHECK_SECURITY_TIMEOUT)" "Security verification" ./scripts/checks/check-security.sh

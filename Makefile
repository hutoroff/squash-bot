SHELL := /bin/sh

.PHONY: doctor bootstrap check-fast check check-secrets check-security

doctor:
	@./scripts/checks/doctor.sh

bootstrap:
	@printf '\n==> Installing locked frontend dependencies\n'
	npm --prefix web/frontend ci
	@printf '\n==> Building embedded frontend assets\n'
	npm --prefix web/frontend run build

check-fast:
	@./scripts/checks/check-fast.sh

check:
	@./scripts/checks/check.sh

check-secrets:
	@node scripts/checks/secrets.mjs

check-security:
	@./scripts/checks/check-security.sh

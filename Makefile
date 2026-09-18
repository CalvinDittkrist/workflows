# The gate: `make check` runs everything CI gates on, locally and in the CI job named `check`.
SCRIPTS := $(wildcard plugins/*/scripts/*.sh scripts/*.sh)

.PHONY: check lint validate standard test
check: lint validate standard test

lint:
	@command -v shellcheck >/dev/null || { echo 'error: shellcheck not installed; brew install shellcheck' >&2; exit 1; }
	shellcheck -s bash $(SCRIPTS)
	@for f in $(SCRIPTS); do bash -n "$$f" || exit 1; done

validate:
	@command -v claude >/dev/null || { echo 'error: claude not installed; npm install -g @anthropic-ai/claude-code' >&2; exit 1; }
	claude plugin validate . --strict
	@set -e; for p in plugins/*; do echo "claude plugin validate $$p --strict"; claude plugin validate "$$p" --strict; done
	@set -e; for d in plugins/*/skills plugins/*/agents; do [ ! -d "$$d" ] || { echo "claude plugin validate $$d --strict"; claude plugin validate "$$d" --strict; }; done

standard:
	plugins/repo-standards/scripts/check.sh

test:
	python3 -m unittest discover -s tests -v

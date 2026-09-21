# The gate: `make check` runs everything CI gates on, locally and in the CI job named `check`.
SCRIPTS := $(wildcard plugins/*/scripts/*.sh scripts/*.sh) $(wildcard tests/shims/*)

.PHONY: check lint validate standard test ui factory browser
check: lint validate standard test ui factory browser

# The factory's dashboard: an npm package that Vite builds into factory/ui/dist/app, which the binary
# embeds. Every target below needs that build, so it is a file the others depend on.
UI := factory/ui
UI_BUILD := $(UI)/dist/app/index.html
UI_SOURCES := $(UI)/index.html $(UI)/vite.config.js $(wildcard $(UI)/src/*)

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

# The dashboard: the same lint and build the CI job runs, on the sources the binary serves.
ui: $(UI_BUILD)
	npm --prefix $(UI) run lint

$(UI_BUILD): $(UI)/node_modules $(UI_SOURCES)
	npm --prefix $(UI) run build

$(UI)/node_modules: $(UI)/package-lock.json
	@command -v npm >/dev/null || { echo 'error: npm not installed; brew install node (or https://nodejs.org), the factory embeds a dashboard that is built with it' >&2; exit 1; }
	npm --prefix $(UI) ci
	@touch $@

# The dashboard read the way the maintainer reads it: a real browser against the real binary in fake
# mode. Chromium is downloaded once into Playwright's own cache; the call is a no-op after that.
browser: $(UI_BUILD)
	npm --prefix $(UI) exec -- playwright install chromium
	npm --prefix $(UI) test

# The factory is a Go service; its tests start the real binary and watch it from outside. They read
# the dashboard out of the binary, so the build it embeds has to be there before they run.
factory: $(UI_BUILD)
	@command -v go >/dev/null || { echo 'error: go not installed; brew install go (or https://go.dev/dl), the factory is written in Go' >&2; exit 1; }
	@dirs="$$(go -C factory list -f '{{.Dir}}' ./...)" || exit 1; \
		unformatted="$$(gofmt -l $$dirs)" || { echo 'error: gofmt could not run; it ships with Go, put the bin directory of the Go installation on PATH' >&2; exit 1; }; \
		[ -z "$$unformatted" ] || { echo "error: not formatted: $$unformatted; run gofmt -w factory" >&2; exit 1; }
	go -C factory vet ./...
	@sc="$$(command -v staticcheck 2>/dev/null || true)"; [ -n "$$sc" ] || sc="$$(go env GOPATH)/bin/staticcheck"; \
		[ -x "$$sc" ] || { echo 'error: staticcheck not installed; go install honnef.co/go/tools/cmd/staticcheck@2026.2.1' >&2; exit 1; }; \
		echo "$$sc ./... (in factory)"; cd factory && "$$sc" ./...
	go -C factory test -race ./... # the service is goroutines over shared run records: the gate says so

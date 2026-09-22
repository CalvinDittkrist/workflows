package main

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"
)

// What a run runs with, and how it comes to be that. The factory updates the worker plugin before
// every run, so a fix merged into the pipeline reaches the next unattended run without anybody
// touching the host, and it writes down the versions it then starts the worker with, so a run that
// went wrong can be read back to what made it.
//
// It updates those two things and nothing else. Claude Code is the host's, installed and upgraded
// by whoever runs the host, and the factory is the process doing the updating: a service that
// replaced its own binary or the agent runtime under a session it is about to start would be
// changing itself while it works, and no operator asked it to ([ADR 0036]).
//
// [ADR 0036]: ../docs/adr/0036-the-factory-updates-the-worker-plugin-and-nothing-else.md

// The marketplace the workflow is distributed from and the plugin a run is made of. They are named
// here as the session's settings name the plugins it switches off: what the factory starts is the
// worker plugin of the workflows marketplace, not something a configuration file chooses.
const (
	marketplace  = "workflows"
	workerPlugin = "worker@" + marketplace
	// pluginDirFlag is how a worker command names a plugin directory to load instead, which the
	// versions of a run have to reckon with.
	pluginDirFlag = "--plugin-dir"
)

const (
	// updateTimeout bounds one plugin command. It fetches the marketplace over the host's line, so it
	// has the room a slow line needs; a command that hangs must not hold the run it belongs to.
	updateTimeout = 5 * time.Minute
	// versionTimeout bounds a question asked of what is installed, which answers from the disk.
	versionTimeout = 30 * time.Second
)

// prepare brings the worker plugin up to date and records the versions this run runs with. It is
// called between the claim and the worker, which is the moment nothing of this factory is running:
// one worker at a time ([ADR 0025]), so no session is reading the plugin while it is written.
//
// Nothing here fails a run. An update that did not work leaves the installed state, which is a
// worker that works — an older one — and the run says so in a warning rather than giving the issue
// back for a reason that has nothing to do with it.
//
// [ADR 0025]: ../docs/adr/0025-one-queue-one-worker-work-in-progress-first.md
func (f *Factory) prepare(ctx context.Context, r *Run) {
	if f.fake {
		// Fake mode runs this binary as its worker: there is no plugin in the run to update and no
		// version of one to record. It also must change nothing on the machine it is tried out on,
		// and a plugin the host installed is exactly that.
		return
	}
	f.updatePlugins(ctx, r)
	if ctx.Err() != nil {
		// The factory is stopping or the deadline passed while the plugins were updated. The run ends
		// in a moment for that reason, and a record saying it ran with versions nobody read would be
		// a second, invented one.
		return
	}
	f.recordVersions(ctx, r)
}

// updatePlugins updates the marketplace and then the worker plugin in it, each with Claude Code's
// own command. The two are separate acts: a marketplace that could not be fetched still holds the
// release it last knew, and the plugin update can take that one.
//
// Neither command is given a scope: `plugin update` acts on the user scope by default ([plugins
// reference]), which is the scope a run is started from and the one workerVersion reads back.
//
// [plugins reference]: https://code.claude.com/docs/en/plugins-reference.md
func (f *Factory) updatePlugins(ctx context.Context, r *Run) {
	for _, step := range []struct {
		what string
		args []string
	}{
		{"the marketplace " + marketplace, []string{"plugin", "marketplace", "update", marketplace}},
		{"the plugin " + workerPlugin, []string{"plugin", "update", workerPlugin}},
	} {
		out, reason, err := command(ctx, updateTimeout, "claude", step.args...)
		if ctx.Err() != nil {
			return // the factory is stopping or the deadline passed; the run's own end says that
		}
		if err != nil {
			f.warn(r, "could not update "+step.what,
				fmt.Sprintf("%s could not be updated: %s; this run uses the state installed on this host", step.what, reason))
			continue
		}
		f.runs.event(r, Event{Kind: "factory", Title: "updated " + step.what, Body: string(out)})
	}
}

// recordVersions writes down what this run is about to be made of, read after the update and before
// the worker is started. The factory's own version is on the record from the moment the run was
// created; these two are asked of the host.
func (f *Factory) recordVersions(ctx context.Context, r *Run) {
	// One statement each: either may warn, and the order the warnings land in is this order.
	worker := f.workerVersion(ctx, r)
	claudeCode := f.claudeVersion(ctx, r)
	f.runs.update(r, func() { r.Versions.Worker, r.Versions.ClaudeCode = worker, claudeCode })
	f.runs.event(r, Event{Kind: "factory", Title: "versions",
		Body: fmt.Sprintf("worker %s, Claude Code %s, factory %s", orUnknown(worker), orUnknown(claudeCode), r.Versions.Factory)})
}

// orUnknown is a version that could not be read, as the log says it.
func orUnknown(version string) string {
	if version == "" {
		return "unknown"
	}
	return version
}

// installedPlugin is one row of `claude plugin list --json`: a plugin as one scope of this host has
// it. Only what the record carries is read; the row holds more.
type installedPlugin struct {
	ID      string `json:"id"` // name@marketplace
	Version string `json:"version"`
	Scope   string `json:"scope"`
	Enabled bool   `json:"enabled"`
}

// workerVersion is the version of the worker plugin this host has installed and switched on. A
// plugin is listed once per scope it is installed in, and the scope of a run is the host's: the
// machine user the factory runs as installs the plugin once ([ADR 0027]) and every run of every
// connected repository is started from that one install. A row that belongs to some checkout on the
// host is another session's business and not what this run runs with, and one that is switched off
// is not what the session runs either — a version recorded off such a row would name a worker the
// run never had.
//
// A host that gives its worker a plugin directory of its own (worker_args with --plugin-dir, see
// the README) is the one case where the installed row is not the answer: a plugin loaded that way
// takes precedence over the installed one of the same name for that session, and it is listed by no
// `claude plugin list` that is not given the same flag ([plugins]). Such a run is made of a
// checkout, whose version is nothing this factory can read, so it records none and says so — a
// number off the install would name a worker the session did not load.
//
// [ADR 0027]: ../docs/adr/0027-the-factorys-isolation-boundary-is-the-host.md
// [plugins]: https://code.claude.com/docs/en/plugins.md
func (f *Factory) workerVersion(ctx context.Context, r *Run) string {
	if dirs := pluginDirs(f.settings.WorkerArgs); len(dirs) > 0 {
		f.versionUnread(ctx, r, "this host loads its worker from "+strings.Join(dirs, ", ")+
			" (worker_args --plugin-dir), which takes precedence over anything installed")
		return ""
	}
	out, reason, err := command(ctx, versionTimeout, "claude", "plugin", "list", "--json")
	if err != nil {
		f.versionUnread(ctx, r, "the installed plugins could not be listed: "+reason)
		return ""
	}
	var installed []installedPlugin
	if err := json.Unmarshal(out, &installed); err != nil {
		f.versionUnread(ctx, r, "the installed plugins are not a plugin list: "+err.Error())
		return ""
	}
	for _, plugin := range installed {
		if plugin.ID == workerPlugin && hostScope(plugin.Scope) && plugin.Enabled {
			return plugin.Version
		}
	}
	f.versionUnread(ctx, r, workerPlugin+" is not installed and enabled for the user this factory runs as")
	return ""
}

// pluginDirs are the plugin directories the host adds to every worker command. The flag takes one
// path and is repeated for more ([CLI reference]); the documented form separates flag and value,
// and the joined one is read as well rather than passed over as an argument of something else.
//
// [CLI reference]: https://code.claude.com/docs/en/cli-reference.md
func pluginDirs(workerArgs []string) []string {
	var dirs []string
	for i, arg := range workerArgs {
		switch {
		case arg == pluginDirFlag && i+1 < len(workerArgs):
			dirs = append(dirs, workerArgs[i+1])
		case strings.HasPrefix(arg, pluginDirFlag+"="):
			dirs = append(dirs, strings.TrimPrefix(arg, pluginDirFlag+"="))
		}
	}
	return dirs
}

// hostScope says that an installed plugin belongs to the host rather than to a checkout on it: the
// machine user's own install, or one an administrator deployed for the whole machine. A plugin is
// installed in one of four scopes — user, project, local, managed ([plugins reference]) — and the
// two that outlive a checkout are the run's.
//
// [plugins reference]: https://code.claude.com/docs/en/plugins-reference.md
func hostScope(scope string) bool { return scope == "user" || scope == "managed" }

// claudeVersion is the Claude Code the worker session will be, as the binary reports itself. The
// factory only ever reads it: upgrading Claude Code is the host's business ([ADR 0036]).
//
// [ADR 0036]: ../docs/adr/0036-the-factory-updates-the-worker-plugin-and-nothing-else.md
func (f *Factory) claudeVersion(ctx context.Context, r *Run) string {
	out, reason, err := command(ctx, versionTimeout, "claude", "--version")
	if err != nil {
		f.versionUnread(ctx, r, "the version of Claude Code could not be read: "+reason)
		return ""
	}
	version := versionOf(string(out))
	if version == "" {
		f.versionUnread(ctx, r, "the version of Claude Code was printed empty")
	}
	return version
}

// versionOf is the version out of what `claude --version` prints, which is the number and the name
// of the program after it ("2.1.278 (Claude Code)"). The record carries the number; a line that is
// not of that shape is recorded whole rather than read into something it is not.
func versionOf(printed string) string {
	line := firstLine(printed)
	number, _, _ := strings.Cut(line, " ")
	if number == "" {
		return line
	}
	return number
}

// versionUnread says that a version could not be read. It is a warning like a failed update: a run
// nobody can trace to what it ran with is the thing this record exists to prevent. A factory that is
// stopping says nothing — the run's own end is the reason then.
func (f *Factory) versionUnread(ctx context.Context, r *Run, said string) {
	if ctx.Err() != nil {
		return
	}
	f.warn(r, "a version could not be read", said+"; this run does not say which version it ran with")
}

package main

import (
	"context"
	"fmt"
	"strings"
	"time"
)

// What a run runs with. A run's sessions are made of the factory's own prompts and of Claude Code, and
// nothing else: the factory updates nothing before a run and runs no plugin ([ADR 0042]). The version
// of the factory is on the record from the moment the run was created; the version of Claude Code is
// asked of the host before the first session, so a run that went wrong can be read back to what made
// it. Claude Code is the host's, installed and upgraded by whoever runs the host.
//
// [ADR 0042]: ../docs/adr/0042-the-factory-carries-its-own-prompts-and-updates-no-plugin.md

// versionTimeout bounds the question asked of the installed Claude Code, which answers from the disk.
const versionTimeout = 30 * time.Second

// recordVersions writes down the Claude Code this run's sessions will be. Fake mode runs no Claude
// Code, and records none.
func (f *Factory) recordVersions(ctx context.Context, r *Run) {
	if f.fake {
		return
	}
	claudeCode := f.claudeVersion(ctx, r)
	if ctx.Err() != nil {
		// The factory is stopping or the deadline passed while the version was read. The run ends in a
		// moment for that reason, and a record saying it ran with a version nobody read would be a
		// second, invented one.
		return
	}
	f.runs.update(r, func() { r.Versions.ClaudeCode = claudeCode })
	f.runs.event(r, Event{Kind: "factory", Title: "versions",
		Body: fmt.Sprintf("Claude Code %s, factory %s", orUnknown(claudeCode), r.Versions.Factory)})
}

// orUnknown is a version that could not be read, as the log says it.
func orUnknown(version string) string {
	if version == "" {
		return "unknown"
	}
	return version
}

// claudeVersion is the Claude Code the sessions will be, as the binary reports itself. The factory
// only ever reads it: upgrading Claude Code is the host's business.
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
	if number, name, ok := strings.Cut(line, " "); ok && number != "" && name == "(Claude Code)" {
		return number
	}
	return line
}

// versionUnread says that the version could not be read. It is a warning, and the run goes on: a run
// nobody can trace to what it ran with is the thing this record exists to prevent, and no reason to
// give the issue back. A factory that is stopping says nothing — the run's own end is the reason then.
func (f *Factory) versionUnread(ctx context.Context, r *Run, said string) {
	if ctx.Err() != nil {
		return
	}
	f.warn(r, "a version could not be read", said+"; this run does not say which version it ran with")
}

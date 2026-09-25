package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"os/exec"
	"slices"
	"strings"
	"time"
	"unicode"
)

// The quota check. The factory and the maintainer share one Claude subscription, so before every run
// the factory asks the quota-axi the host has installed how much of it is left and waits when too
// little is. It is a courtesy and not a guard: a check that cannot answer lets the run start with a
// warning, because the worst case of that is a slow lunchtime and the worst case of the other way
// round is a host that stopped for days without telling anyone ([ADR 0028], [ADR 0037]).
//
// [ADR 0028]: ../docs/adr/0028-the-quota-check-is-a-courtesy-not-a-guard.md
// [ADR 0037]: ../docs/adr/0037-the-quota-check-waits-below-12-percent-of-the-workers-scope.md

// quotaSchema is the version of quota-axi's JSON output this reading is written against. Another
// version is output the factory cannot read, which is the operator's cue to look at the release
// notes before installing it on the host.
const quotaSchema = 5

// quotaTimeout is how long one check may take. quota-axi asks the provider's own endpoint over the
// host's line, and a line that hangs must not hold the factory's loop for longer than that.
const quotaTimeout = 30 * time.Second

// allModels is the scope quota-axi gives the limits every model counts against: the session and the
// weekly window.
const allModels = "all_models"

// workerModel is the model the worker agent runs on unless worker_args names another. The factory's
// definition of that agent names it (workerAgents), so the scope the check reads is the one the
// worker spends.
const workerModel = "opus"

// scope is one row quota-axi reports: how much of it is left and when the windows that limit it
// reset. A scope the provider does not limit on its own is not reported at all.
type scope struct {
	name      string
	remaining float64
	reset     time.Time // the latest reset of its limiting windows; zero when quota-axi named none
}

// quota is one reading: the all-models scope and, of every model a run spends, its scope when the
// provider limits it apart.
type quota struct {
	scopes []scope
}

// remaining is the smaller of the scopes' percentages, which is what the next run can spend, and the
// scope that sets it.
func (q quota) remaining() (float64, string) {
	least, name := q.scopes[0].remaining, q.scopes[0].name
	for _, s := range q.scopes[1:] {
		if s.remaining < least {
			least, name = s.remaining, s.name
		}
	}
	return least, name
}

// below says whether any scope has less left than the threshold, and until when: the latest reset of
// the scopes below it, since the next run needs all of them back. A scope below the threshold that
// names no reset is output the factory cannot act on, so it is an error, and the caller fails open.
func (q quota) below(threshold float64) (bool, time.Time, error) {
	var until time.Time
	low := false
	for _, s := range q.scopes {
		if s.remaining >= threshold {
			continue
		}
		if s.reset.IsZero() {
			return true, time.Time{}, fmt.Errorf("the scope %s has %s left and quota-axi names no reset time for it", s.name, percent(s.remaining))
		}
		low = true
		if s.reset.After(until) {
			until = s.reset
		}
	}
	return low, until, nil
}

// quotaReport is the part of quota-axi's JSON output the check reads, in schema version 5.
type quotaReport struct {
	SchemaVersion int `json:"schemaVersion"`
	Providers     []struct {
		Provider string `json:"provider"`
		State    struct {
			Stale bool `json:"stale"`
		} `json:"state"`
		Windows []struct {
			ID       string `json:"id"`
			ResetsAt string `json:"resetsAt"`
		} `json:"windows"`
		QuotaSemantics struct {
			EffectiveAvailability []struct {
				Scope                     string   `json:"scope"`
				Status                    string   `json:"status"`
				EffectivePercentRemaining *float64 `json:"effectivePercentRemaining"`
				LimitingWindowIDs         []string `json:"limitingWindowIds"`
			} `json:"effectiveAvailability"`
		} `json:"quotaSemantics"`
	} `json:"providers"`
}

// readQuota runs the configured quota-axi for the Claude provider and reads the scopes of these models
// out of its answer. An expired credential is renewed on the way: quota-axi runs Claude Code's own
// `claude doctor` for that, which spends no quota, and no session of the factory reads the credential
// while a check runs, because the factory works one run at a time and checks only between sessions
// ([ADR 0048]). With the flag that forbade the renewal, every run after a quiet night started without
// a reading, which is when the maintainer's share of the window is most likely in use.
//
// [ADR 0048]: ../docs/adr/0048-the-quota-check-renews-an-expired-credential.md
func (f *Factory) readQuota(ctx context.Context, models []string) (quota, error) {
	ctx, cancel := context.WithTimeout(ctx, quotaTimeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, f.settings.QuotaAxi, "--provider", "claude", "--json")
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	if err := cmd.Run(); err != nil {
		if errors.Is(ctx.Err(), context.DeadlineExceeded) {
			return quota{}, fmt.Errorf("%s did not answer within %s", f.settings.QuotaAxi, quotaTimeout)
		}
		if said := firstLine(stderr.String()); said != "" {
			return quota{}, fmt.Errorf("%s failed: %v: %s", f.settings.QuotaAxi, err, said)
		}
		return quota{}, fmt.Errorf("%s failed: %v", f.settings.QuotaAxi, err)
	}
	return parseQuota(stdout.Bytes(), models)
}

// parseQuota reads the all-models scope and the scopes of the models out of quota-axi's output.
func parseQuota(raw []byte, models []string) (quota, error) {
	var report quotaReport
	if err := json.Unmarshal(raw, &report); err != nil {
		return quota{}, fmt.Errorf("quota-axi printed something that is not its JSON report: %v", err)
	}
	if report.SchemaVersion != quotaSchema {
		return quota{}, fmt.Errorf("quota-axi reports in schema version %d and the factory reads version %d; install the pinned quota-axi again", report.SchemaVersion, quotaSchema)
	}
	for _, provider := range report.Providers {
		if provider.Provider != "claude" {
			continue
		}
		// A stale reading is quota-axi's cache of an older answer: its percentages and resets may
		// be long gone, so the factory neither waits on it nor resumes by it.
		if provider.State.Stale {
			return quota{}, errors.New("quota-axi's reading of claude is stale, so it does not say how much is left now")
		}
		resets := map[string]time.Time{}
		for _, w := range provider.Windows {
			if at, err := time.Parse(time.RFC3339, w.ResetsAt); err == nil {
				resets[w.ID] = at
			}
		}
		read := quota{}
		for _, row := range provider.QuotaSemantics.EffectiveAvailability {
			if row.Scope != allModels && !slices.ContainsFunc(models, func(model string) bool { return modelScope(row.Scope, model) }) {
				continue
			}
			if row.Status != "known" || row.EffectivePercentRemaining == nil {
				return quota{}, fmt.Errorf("quota-axi does not know how much of the scope %s is left (status %q)", row.Scope, row.Status)
			}
			s := scope{name: row.Scope, remaining: *row.EffectivePercentRemaining}
			for _, id := range row.LimitingWindowIDs {
				if at := resets[id]; at.After(s.reset) {
					s.reset = at
				}
			}
			if row.Scope == allModels {
				read.scopes = append([]scope{s}, read.scopes...)
			} else {
				read.scopes = append(read.scopes, s)
			}
		}
		if len(read.scopes) == 0 || read.scopes[0].name != allModels {
			return quota{}, errors.New("quota-axi reports no all_models scope for claude")
		}
		return read, nil
	}
	return quota{}, errors.New("quota-axi reports nothing for the provider claude; is this host's Claude Code logged in?")
}

// spends is the models a run of the repository spends: the worker's, and the model of every reviewer
// of its panel that names one of its own rather than inheriting the worker's.
func (f *Factory) spends(repository string) []string {
	models := []string{f.settings.WorkerModel}
	// Which change class a run's change is of is known only once it is made, so every reviewer a class
	// of the repository names may run, beside the panel.
	knobs := f.reviewFor(repository)
	names := slices.Clone(knobs.Reviewers)
	for _, class := range knobs.Classes {
		names = append(names, class.Reviewers...)
	}
	for _, name := range names {
		if model := reviewers[name].model; model != "inherit" && !slices.Contains(models, model) {
			models = append(models, model)
		}
	}
	return models
}

// modelScope says whether a scope of quota-axi is the one of this model: model:opus is the scope of
// opus, of claude-opus-4-1 and of opus[1m] alike, because a model is named by its family in every
// spelling Claude Code takes.
func modelScope(name, model string) bool {
	family, ok := strings.CutPrefix(name, "model:")
	if !ok || family == "" {
		return false
	}
	words := strings.FieldsFunc(strings.ToLower(model), func(r rune) bool {
		return !unicode.IsLetter(r) && !unicode.IsDigit(r)
	})
	for _, word := range words {
		if word == strings.ToLower(family) {
			return true
		}
	}
	return false
}

// quotaAllows is the check before a run. It answers whether the run may start and, when it may, the
// warning it starts with: empty when the check answered, and why it did not otherwise. When it may
// not, the factory waits until the reset quota-axi reported, and the interface says so.
func (f *Factory) quotaAllows(ctx context.Context, repository string) (bool, string) {
	if f.settings.QuotaAxi == "" {
		return true, ""
	}
	read, err := f.readQuota(ctx, f.spends(repository))
	if err == nil {
		var low bool
		var until time.Time
		low, until, err = read.below(float64(f.settings.QuotaMinimum))
		if err == nil && low {
			left, name := read.remaining()
			f.waitForQuota(until, fmt.Sprintf("%s of %s is left, below the minimum of %d %%", percent(left), name, f.settings.QuotaMinimum))
			return false, ""
		}
	}
	f.mu.Lock()
	f.quotaUntil = nil
	f.mu.Unlock()
	if ctx.Err() != nil {
		return false, "" // the factory is stopping, and a check it cut off is not a check that failed
	}
	if err != nil {
		log.Printf("warning: the quota check failed, so the next run starts without it: %v", err)
		return true, "the quota check failed, so this run started without knowing how much of the Claude quota is left: " + err.Error()
	}
	return true, ""
}

// quotaExhausted is the check after a run that ended in an error: whether a scope the run spends is
// used up, and until when. Only then is the error the quota's rather than the issue's. A check
// that cannot answer says no, and the run is failed like any other: fail open here means that the
// run's outcome is what the factory knows, not what it guesses.
func (f *Factory) quotaExhausted(ctx context.Context, repository string) (bool, string, time.Time, error) {
	if f.settings.QuotaAxi == "" {
		return false, "", time.Time{}, nil
	}
	read, err := f.readQuota(ctx, f.spends(repository))
	if err != nil {
		return false, "", time.Time{}, err
	}
	exhausted, until, err := read.below(quotaEmpty)
	if err != nil || !exhausted {
		return false, "", time.Time{}, err
	}
	_, name := read.remaining()
	f.waitForQuota(until, "the scope "+name+" is exhausted")
	return true, name, until, nil
}

// quotaEmpty is the threshold below which a scope is used up: quota-axi counts in whole percent, and
// less than one of them is nothing left.
const quotaEmpty = 1

// waitForQuota makes the factory wait until the reset: the interface says it waits for quota and
// until when, nothing starts before then, and the check runs again once it has passed.
func (f *Factory) waitForQuota(until time.Time, why string) {
	f.mu.Lock()
	already := f.quotaUntil != nil && f.quotaUntil.Equal(until)
	f.quotaUntil = &until
	f.mu.Unlock()
	if !already {
		log.Printf("waiting for quota: %s; nothing starts before the reset at %s", why, until.Format(time.RFC3339))
	}
}

// waitingForQuota says until when the factory waits for quota, and whether it still does. A wait
// whose reset has passed is over whether or not a check followed it: a line that emptied during the
// wait asks nothing of quota-axi, and the interface must not show a wait for a moment gone by.
func (f *Factory) waitingForQuota(now time.Time) (time.Time, bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.quotaUntil == nil {
		return time.Time{}, false
	}
	if !f.quotaUntil.After(now) {
		f.quotaUntil = nil
		return time.Time{}, false
	}
	return *f.quotaUntil, true
}

// percent writes a percentage as the interface and the log show it.
func percent(p float64) string { return fmt.Sprintf("%g %%", p) }

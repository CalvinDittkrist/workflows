package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"maps"
	"net"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"time"
)

// Config is the configuration file, the whole of it. The factory is configured by this file alone:
// nothing is read from the environment and nothing is written back.
type Config struct {
	Listen     string   `json:"listen"`
	Label      string   `json:"label"`
	Deadline   string   `json:"deadline"`
	Poll       string   `json:"poll"`
	DataDir    string   `json:"data_dir"`
	WorkerArgs []string `json:"worker_args"`
	// WorkerEnv is the worker knobs every run of this host is given: the variables the worker
	// plugin's scripts read for themselves, such as WF_REVIEW_ROUNDS, by name and value. They are
	// the knobs a local claim takes with --env and nothing else: what a run is — its mode, its
	// issue, its base — is the factory's, and a name outside the list is refused (workerKnobs).
	WorkerEnv map[string]string `json:"worker_env"`
	// Paused is a pointer because its default is not the zero value: a file that does not name it
	// runs paused, so working a line unattended is always something the operator wrote down.
	Paused *bool `json:"paused"`
	// Notify is the GitHub logins the factory tells how a run ended, written without the @. Nobody
	// watches the host, so this is how the maintainer learns of it, and GitHub is the only channel
	// there is ([ADR 0023]).
	//
	// [ADR 0023]: ../docs/adr/0023-github-is-the-only-control-surface-of-the-factory.md
	Notify       []string    `json:"notify"`
	Repositories []Connected `json:"repositories"`
	// QuotaAxi is the path of the quota-axi installed on the host, and QuotaMinimum the percentage of
	// the Claude quota below which no run starts. Without the path the check is off ([ADR 0028]); the
	// minimum is a pointer because 0 is a setting of its own, a check that never waits.
	//
	// [ADR 0028]: ../docs/adr/0028-the-quota-check-is-a-courtesy-not-a-guard.md
	QuotaAxi     string `json:"quota_axi"`
	QuotaMinimum *int   `json:"quota_minimum"`
	// CI is the knobs of the ci stage for every connected repository, and a repository's own ci
	// object overrides them one knob at a time (ciKnobs).
	CI     *ciKnobs     `json:"ci"`
	Review *reviewKnobs `json:"review"`
}

// Connected is one repository the factory works: its name on GitHub and, optionally, the branch a
// run of it branches off. The base is the explicit setting of the base branch rule, and it belongs
// to the repository rather than to the factory, because a host connects repositories that do not
// agree on one — one on main, the next on dev ([ADR 0022]).
//
// [ADR 0022]: ../docs/adr/0022-the-factory-is-a-second-driver-over-the-worker-pipeline.md
type Connected struct {
	Name   string       `json:"name"`
	Base   string       `json:"base"`
	CI     *ciKnobs     `json:"ci"`
	Review *reviewKnobs `json:"review"`
	// wait is the ci stage's knobs for this repository, and panel the review stage's: the host's, with
	// what the repository's own ci and review objects name written over them. Load fills them in.
	wait  ciSettings
	panel reviewSettings
}

// UnmarshalJSON takes a connected repository as the name alone or as an object with its settings, so
// the common case stays one line in the file and a repository that needs a base branch says so.
func (c *Connected) UnmarshalJSON(raw []byte) error {
	if len(raw) > 0 && raw[0] == '"' {
		var name string
		if err := json.Unmarshal(raw, &name); err != nil {
			return err
		}
		*c = Connected{Name: name}
		return nil
	}
	// A type of its own, without this method, so the object form is decoded and not read again by it.
	type settings Connected
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	var read settings
	if err := decoder.Decode(&read); err != nil {
		return fmt.Errorf(`%w; a repository is "owner/name" or {"name": "owner/name", "base": "dev", "ci": {"repair_rounds": 2}, "review": {"rounds": 2}}`, err)
	}
	*c = Connected(read)
	return nil
}

// Settings is what the factory runs on: the configuration file, validated, with its durations parsed
// and its defaults filled in.
type Settings struct {
	Listen       string
	Label        string
	Deadline     time.Duration
	Poll         time.Duration
	DataDir      string
	WorkerArgs   []string
	WorkerEnv    map[string]string
	Paused       bool // as the file said when it was read; a running factory asks Factory.Paused
	Notify       []string
	Repositories []Connected
	QuotaAxi     string // empty: the quota check is off
	QuotaMinimum int
	// CI is the host's knobs of the ci stage, and each connected repository carries its own, resolved
	// against them.
	CI ciSettings
	// Review is the host's knobs of the review stage, resolved the same way.
	Review reviewSettings
	// WorkerModel is the model the worker runs on, the one whose quota scope the check reads: the
	// worker agent's own unless worker_args names another with --model.
	WorkerModel string
}

const (
	defaultListen       = "127.0.0.1:7341"
	defaultLabel        = "factory"
	defaultDeadline     = 120 * time.Minute
	defaultPoll         = 60 * time.Second
	defaultQuotaMinimum = 12

	configFields = "listen, label, deadline, poll, data_dir, worker_args, worker_env, paused, notify, repositories, quota_axi, quota_minimum, ci, review"
)

// A repository is named as owner/name; the factory never takes a URL or a local path, because the
// same name has to identify the repository on GitHub and in an issue's link.
var repository = regexp.MustCompile(`^[A-Za-z0-9._-]+/[A-Za-z0-9._-]+$`)

// A GitHub login is alphanumeric with single hyphens between, at most 39 characters, and it reaches
// gh as an argument and a comment as a mention: a spelling GitHub does not have would ask for a
// review from nobody, or mention somebody the factory was never told to notify.
var githubLogin = regexp.MustCompile(`^[A-Za-z0-9](?:-?[A-Za-z0-9])*$`)

// loginLength is the longest login GitHub gives out.
const loginLength = 39

// A base branch is a branch name, and it reaches git as a ref and gh as an argument: no spelling
// that opens with a hyphen, walks out of refs/heads with .. or ends a ref name.
var branchSpelling = regexp.MustCompile(`^[A-Za-z0-9._/-]+$`)

// validBase says whether a name may be used as a base branch. It is asked of the configuration here
// and of what a repository declares for itself (declaredBase), because both end up in the same ref
// and the same command line. The rules below git's are git's own for refs/heads/<name>, the ones
// `git check-ref-format` enforces, and a drift test holds them against that command: a name this
// says yes to and git says no to would pass the start of the factory and fail the first claim of
// that repository, which costs an issue a run.
func validBase(name string) bool {
	if !branchSpelling.MatchString(name) || strings.HasPrefix(name, "-") {
		return false
	}
	if strings.Contains(name, "..") || strings.HasPrefix(name, "/") || strings.HasSuffix(name, "/") || strings.HasSuffix(name, ".") {
		return false
	}
	// git reads a ref as the components between its slashes: none of them may be empty, open with a
	// dot or end in .lock, the name git locks a reference with while it writes it.
	for _, part := range strings.Split(name, "/") {
		if part == "" || strings.HasPrefix(part, ".") || strings.HasSuffix(part, ".lock") {
			return false
		}
	}
	return true
}

// workerFlags are the arguments of the worker command the run itself is defined by: the agent, the
// prompt, the shape of the output the factory reads the run from, the permission mode being
// unattended costs, and the settings object that carries the mode, the issue, the base branch and
// the compact pin. worker_args is added to that command, so an operator's own copy of one of them
// would be a second value for something the factory has decided — and a worker started with someone
// else's --settings would review and open its pull request against the wrong branch, or merge what
// it built. What Claude Code makes of two of the same flag is not what the factory rests on: it is
// refused before a run is started (README, Configuration).
var workerFlags = []string{"--settings", "--agent", "--permission-mode", "--output-format", "-p", "--print"}

// factoryOwns names the flag of the worker command an argument would be a second value for, or "".
func factoryOwns(arg string) string {
	for _, flag := range workerFlags {
		if arg == flag || strings.HasPrefix(arg, flag+"=") {
			return flag
		}
	}
	return ""
}

// workerKnobs are the names worker_env may set: the variables the worker plugin's scripts read for
// themselves, which is the list the orchestrator's claim.sh accepts for --env (env_accepted), and a
// drift test holds the two together. An empty value is a setting of its own, as it is on a claim.
// What a run is stays out of the list: WF_MODE, WF_ISSUE and WF_BASE_BRANCH are the factory's
// (workerVariables), WF_REVIEW_MANDATE is the word of a driver that starts a worker session on a
// review, which the factory, answering reviews itself, never is, and a variable of the host's shell
// is not a setting of the workflow.
//
// The knobs of the review and of the wait for CI are the factory's own since it runs those stages
// itself, so worker_env refuses them and names the knob each one moved to (movedKnobs).
var workerKnobs = []string{"WF_REVIEWERS", "WF_REVIEW_ROUNDS", "WF_CI_REPAIR_ROUNDS", "WF_PR_BOT_REVIEWERS", "WF_PR_REVIEW_WAIT", "WF_HANDOFF_TOKENS", "WF_CONTEXT_MAX_AGE", "WF_HANDOFF_SESSION_MS", "WF_HANDOFF_POLL_SECONDS", "WF_DOCS_TIMEOUT"}

// hostKnobs is the names worker_env takes: the worker knobs without the ones the factory's stages took.
func hostKnobs() []string {
	out := []string{}
	for _, name := range workerKnobs {
		if _, moved := movedKnobs[name]; !moved {
			out = append(out, name)
		}
	}
	return out
}

// modelOf is the model a worker started with these arguments runs on: the last --model among them, as
// either spelling of the flag, and the worker agent's own model when they name none.
func modelOf(args []string) string {
	model := workerModel
	for i, arg := range args {
		if value, ok := strings.CutPrefix(arg, "--model="); ok {
			model = value
		} else if arg == "--model" && i+1 < len(args) {
			model = args[i+1]
		}
	}
	return model
}

// unspecified says whether a host is a spelling of "every interface": 0.0.0.0, ::, ::0, ::ffff:0.0.0.0
// and the rest of them. A host that is not an IP literal at all is decided after binding, where the
// address the kernel actually chose is known.
func unspecified(host string) bool {
	ip := net.ParseIP(host)
	return ip != nil && ip.IsUnspecified()
}

// Load reads the configuration file and refuses it unless it is usable. Every error names the fix,
// because the operator reads it in the host's journal and has no shell session to try things in.
func Load(path string) (Settings, error) {
	bad := func(format string, a ...any) (Settings, error) {
		return Settings{}, fmt.Errorf("%s: "+format, append([]any{path}, a...)...)
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		return Settings{}, fmt.Errorf("%s cannot be read: %w; copy factory/factory.example.json to it and name the repositories this host works on", path, err)
	}
	var c Config
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&c); err != nil {
		return bad("%v; the fields are %s, see factory/factory.example.json", err, configFields)
	}
	if decoder.More() {
		return bad("more than one JSON value; the configuration is one object with the fields %s", configFields)
	}

	s := Settings{
		Listen:       defaultListen,
		Label:        defaultLabel,
		Deadline:     defaultDeadline,
		Poll:         defaultPoll,
		WorkerArgs:   c.WorkerArgs,
		Paused:       c.Paused == nil || *c.Paused,
		Notify:       []string{},
		Repositories: []Connected{},
		QuotaMinimum: defaultQuotaMinimum,
		WorkerModel:  modelOf(c.WorkerArgs),
	}
	if c.Listen != "" {
		host, port, err := net.SplitHostPort(c.Listen)
		if err != nil || port == "" {
			return bad("listen %q is not an address; write it as host:port, such as %q", c.Listen, defaultListen)
		}
		// The factory has no login of its own, so it must not answer on every interface: reaching it
		// from elsewhere is the tailnet's job, and a wildcard address would put an unauthenticated
		// interface on every network the host is on.
		if host == "" || unspecified(host) {
			return bad("listen %q answers on every interface; bind it to one address, such as %q or the host's tailnet address", c.Listen, defaultListen)
		}
		s.Listen = c.Listen
	}
	if c.Label != "" {
		if strings.TrimSpace(c.Label) == "" {
			return bad("label %q is blank; name the routing label, such as %q", c.Label, defaultLabel)
		}
		s.Label = c.Label
	}
	if c.Deadline != "" {
		d, err := time.ParseDuration(c.Deadline)
		if err != nil || d <= 0 {
			return bad("deadline %q is not a positive duration; write it as \"90m\" or \"2h30m\"", c.Deadline)
		}
		s.Deadline = d
	}
	if c.Poll != "" {
		d, err := time.ParseDuration(c.Poll)
		if err != nil || d <= 0 {
			return bad("poll %q is not a positive duration; write it as \"60s\" or \"2m\"", c.Poll)
		}
		s.Poll = d
	}
	for _, arg := range c.WorkerArgs {
		if flag := factoryOwns(arg); flag != "" {
			return bad("worker_args carries %s, which the factory gives the worker itself; remove it — worker_args adds arguments to a run, it cannot replace the ones the run is defined by", flag)
		}
	}
	// The names are read in order, so the one the error names is the same on every start.
	for _, name := range slices.Sorted(maps.Keys(c.WorkerEnv)) {
		if moved, ok := movedKnobs[name]; ok {
			return bad("worker_env carries %s, which is a knob of the %s stage the factory runs itself; write it as \"%s\": {\"%s\": ...} at the top of the file or on the repository", name, moved[0], moved[0], moved[1])
		}
		if !slices.Contains(workerKnobs, name) {
			return bad("worker_env carries %s, which is not a worker knob; the names are %s, and an empty value is a setting of its own", name, strings.Join(hostKnobs(), ", "))
		}
	}
	s.WorkerEnv = c.WorkerEnv
	named := map[string]bool{}
	for _, who := range c.Notify {
		// The login reaches gh as an argument and a comment as a mention, so a spelling GitHub does
		// not have is refused here rather than turned into a notification nobody reads.
		if !githubLogin.MatchString(who) || len(who) > loginLength {
			return bad("notify carries %q, which is not a GitHub login; write it as \"octocat\", without the @", who)
		}
		// GitHub reads a login without regard to case, so two spellings of one person would mention
		// them twice and ask them for two reviews of the same pull request.
		if named[strings.ToLower(who)] {
			return bad("notify names %q twice; remove the duplicate", who)
		}
		named[strings.ToLower(who)] = true
		s.Notify = append(s.Notify, who)
	}
	if c.QuotaAxi != "" {
		// A path and never a name: a name would be looked up on PATH, and the check is the binary the
		// operator installed and pinned, not whatever answers to quota-axi on this host today. Nothing
		// is fetched from npm, which is why npx is no way to name it either.
		if !filepath.IsAbs(c.QuotaAxi) {
			return bad("quota_axi %q is not an absolute path; name the quota-axi installed on this host, such as \"/usr/bin/quota-axi\", or leave it out to switch the quota check off", c.QuotaAxi)
		}
		s.QuotaAxi = c.QuotaAxi
	}
	if c.QuotaMinimum != nil {
		if *c.QuotaMinimum < 0 || *c.QuotaMinimum > 100 {
			return bad("quota_minimum %d is not a percentage; write it as a number from 0 to 100, such as %d", *c.QuotaMinimum, defaultQuotaMinimum)
		}
		s.QuotaMinimum = *c.QuotaMinimum
	}
	host, err := defaultCI.over(c.CI)
	if err != nil {
		return bad("ci: %v", err)
	}
	s.CI = host
	panel, err := defaultReview.over(c.Review)
	if err != nil {
		return bad("review: %v", err)
	}
	s.Review = panel
	if strings.TrimSpace(c.DataDir) == "" {
		return bad("data_dir is missing; name the directory the runs are written to, such as \"/var/lib/factory\"")
	}
	s.DataDir = c.DataDir
	if len(c.Repositories) == 0 {
		return bad("repositories is empty; name at least one connected repository as \"owner/name\"")
	}
	seen := map[string]bool{}
	for _, r := range c.Repositories {
		// The clone of a repository is a directory named after it under the data directory, so an
		// owner or a name of nothing but dots would step out of that directory, and the name is given
		// to gh as an argument, where one that opens with a hyphen would be read as a flag. GitHub
		// has neither.
		owner, name, _ := strings.Cut(r.Name, "/")
		if !repository.MatchString(r.Name) || strings.Trim(owner, ".") == "" || strings.Trim(name, ".") == "" ||
			strings.HasPrefix(owner, "-") || strings.HasPrefix(name, "-") {
			return bad("repository %q is not owner/name; write it as \"CalvinDittkrist/workflows\"", r.Name)
		}
		// The base branch is given to git as a ref and to gh as an argument, so a name that opens
		// with a hyphen or walks out of refs/heads is refused here rather than in a command line.
		if r.Base != "" && !validBase(r.Base) {
			return bad("the base branch %q of %s is not a branch name; write it as \"dev\", or leave it out to follow the repository's default branch", r.Base, r.Name)
		}
		// GitHub reads owner and name without regard to case, and so does the filesystem of many a
		// host: two spellings of one repository would be one clone and two places in the line.
		if seen[strings.ToLower(r.Name)] {
			return bad("repository %q is named twice; remove the duplicate", r.Name)
		}
		seen[strings.ToLower(r.Name)] = true
		if r.wait, err = host.over(r.CI); err != nil {
			return bad("the ci of %s: %v", r.Name, err)
		}
		if r.panel, err = panel.over(r.Review); err != nil {
			return bad("the review of %s: %v", r.Name, err)
		}
		s.Repositories = append(s.Repositories, r)
	}
	return s, nil
}

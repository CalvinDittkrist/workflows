package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net"
	"os"
	"regexp"
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
	// Paused is a pointer because its default is not the zero value: a file that does not name it
	// runs paused, so working a line unattended is always something the operator wrote down.
	Paused       *bool       `json:"paused"`
	Repositories []Connected `json:"repositories"`
}

// Connected is one repository the factory works: its name on GitHub and, optionally, the branch a
// run of it branches off. The base is the explicit setting of the base branch rule, and it belongs
// to the repository rather than to the factory, because a host connects repositories that do not
// agree on one — one on main, the next on dev ([ADR 0022]).
//
// [ADR 0022]: ../docs/adr/0022-the-factory-is-a-second-driver-over-the-worker-pipeline.md
type Connected struct {
	Name string `json:"name"`
	Base string `json:"base"`
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
		return fmt.Errorf(`%w; a repository is "owner/name" or {"name": "owner/name", "base": "dev"}`, err)
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
	Paused       bool
	Repositories []Connected
}

const (
	defaultListen   = "127.0.0.1:7341"
	defaultLabel    = "factory"
	defaultDeadline = 120 * time.Minute
	defaultPoll     = 60 * time.Second

	configFields = "listen, label, deadline, poll, data_dir, worker_args, paused, repositories"
)

// A repository is named as owner/name; the factory never takes a URL or a local path, because the
// same name has to identify the repository on GitHub and in an issue's link.
var repository = regexp.MustCompile(`^[A-Za-z0-9._-]+/[A-Za-z0-9._-]+$`)

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
		Repositories: []Connected{},
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
		s.Repositories = append(s.Repositories, r)
	}
	return s, nil
}

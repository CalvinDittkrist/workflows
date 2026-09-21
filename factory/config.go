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
	Listen       string   `json:"listen"`
	Label        string   `json:"label"`
	Deadline     string   `json:"deadline"`
	Poll         string   `json:"poll"`
	DataDir      string   `json:"data_dir"`
	WorkerArgs   []string `json:"worker_args"`
	Paused       bool     `json:"paused"`
	Repositories []string `json:"repositories"`
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
	Repositories []string
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
		Paused:       c.Paused,
		Repositories: []string{},
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
		// owner or a name of nothing but dots would step out of that directory. GitHub has neither.
		owner, name, _ := strings.Cut(r, "/")
		if !repository.MatchString(r) || strings.Trim(owner, ".") == "" || strings.Trim(name, ".") == "" {
			return bad("repository %q is not owner/name; write it as \"CalvinDittkrist/workflows\"", r)
		}
		// GitHub reads owner and name without regard to case, and so does the filesystem of many a
		// host: two spellings of one repository would be one clone and two places in the line.
		if seen[strings.ToLower(r)] {
			return bad("repository %q is named twice; remove the duplicate", r)
		}
		seen[strings.ToLower(r)] = true
		s.Repositories = append(s.Repositories, r)
	}
	return s, nil
}

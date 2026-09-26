package main

// The update tick: the factory binary run with -update by a systemd timer, as root, once an hour.
// One tick is short. It reads the configuration, the newest release, the running factory and the
// binary on disk, does one thing and exits. It never waits for a drain: the drain is the running
// factory's, and the next tick reads where it stands.
//
// The tick installs only what the release workflow of this repository built at the tag of the
// version it installs, as gh's attestation check says without a login. It never downgrades, it
// writes nothing into the factory's data directory, and it keeps its own state (what it said once)
// in a root-owned directory of its own. It talks to gh and systemd through their commands, run as
// child processes by absolute path and never through a shell.

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"time"
)

const (
	// releaseRepository is the repository whose factory releases a host installs.
	releaseRepository = "CalvinDittkrist/workflows"
	// releaseTagPrefix opens every factory version tag; a plugin or milestone tag never carries it.
	releaseTagPrefix = "factory/v"
	// updateStateDir is the updater's own directory, root's and not the factory's.
	updateStateDir = "/var/lib/factory-update"
	// factoryUnit is the service the tick signals and asks about.
	factoryUnit = "factory.service"
	// The commands the tick runs, by absolute path: root's PATH is not what decides which binary
	// verifies a release or signals the service.
	ghCommand        = "/usr/bin/gh"
	systemctlCommand = "/usr/bin/systemctl"
	// The five-part policy of the attestation check.
	releaseIdentity = `^https://github\.com/CalvinDittkrist/workflows/\.github/workflows/factory-release\.yml@refs/tags/factory/v[0-9]+\.[0-9]+\.[0-9]+$`
	actionsIssuer   = "https://token.actions.githubusercontent.com"
	slsaProvenance  = "https://slsa.dev/provenance/v1"
	// updateTimeout bounds a whole tick, downloads and the attestation check included, so a hung
	// network never keeps the next tick from starting.
	updateTimeout = 10 * time.Minute
)

// semver is a factory version, major.minor.patch.
type semver [3]int

func parseSemver(s string) (semver, bool) {
	var v semver
	parts := strings.Split(s, ".")
	if len(parts) != 3 {
		return v, false
	}
	for i, part := range parts {
		n, err := strconv.Atoi(part)
		if err != nil || n < 0 || part != strconv.Itoa(n) {
			return v, false
		}
		v[i] = n
	}
	return v, true
}

func (v semver) less(w semver) bool {
	for i := range v {
		if v[i] != w[i] {
			return v[i] < w[i]
		}
	}
	return false
}

func (v semver) String() string { return fmt.Sprintf("%d.%d.%d", v[0], v[1], v[2]) }

// release is a published factory release: its version and the URLs of the two files a tick needs.
type release struct {
	version semver
	tag     string
	binary  string // the download URL of factory-linux-<arch>
	bundle  string // the download URL of factory-v<version>.sigstore.json
}

// runningFactory is what the line endpoint says of the process that runs.
type runningFactory struct {
	Version  string `json:"version"`
	Draining bool   `json:"draining"`
	Now      []Run  `json:"now"`
}

// updater is one tick.
type updater struct {
	ctx    context.Context
	client *http.Client
	exe    string // the installed binary, the file this tick runs as
	state  string // updateStateDir
	said   map[string]string
}

// update is the tick, from the configuration to its one action. An error is the tick's alone: it
// blocks nothing, and the next tick starts over.
func update(config string) error {
	settings, err := Load(config)
	if err != nil {
		return err
	}
	exe, err := os.Executable()
	if err != nil {
		return fmt.Errorf("the path of this binary cannot be read: %w", err)
	}
	if exe, err = filepath.EvalSymlinks(exe); err != nil {
		return fmt.Errorf("the path of this binary cannot be resolved: %w", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), updateTimeout)
	defer cancel()
	u := &updater{ctx: ctx, client: &http.Client{Timeout: 5 * time.Minute}, exe: exe, state: updateStateDir}
	if err := u.loadState(); err != nil {
		return err
	}
	if !settings.AutoUpdate {
		u.once("auto_update", "off", "auto-update off: the configuration says auto_update false, and the tick touches nothing")
		return u.saveState()
	}
	u.once("auto_update", "on", "auto-update on")
	err = u.tick(settings.Listen)
	if saved := u.saveState(); err == nil {
		err = saved
	}
	return err
}

// tick reads the three facts and does exactly one thing.
func (u *updater) tick(listen string) error {
	newest, err := u.newestRelease()
	if err != nil {
		return err
	}
	fileVersion, err := u.fileVersion(u.exe)
	if err != nil {
		return err
	}
	// The updater never downgrades: a release older than the binary on disk is ignored.
	target := newest.version
	if newest.version.less(fileVersion) {
		u.once("older", newest.version.String(), fmt.Sprintf("the newest release %s is older than the binary on disk, %s; the updater never downgrades and ignores it",
			newest.version, fileVersion))
		target = fileVersion
	}

	process, reachable, err := u.running(listen)
	if err != nil {
		return err
	}
	if !reachable {
		return u.unreachable(listen, newest, fileVersion)
	}
	processVersion, ok := parseSemver(process.Version)
	if !ok {
		return fmt.Errorf("the running factory reports the version %q, which is not major.minor.patch; the tick does nothing", process.Version)
	}
	if processVersion != fileVersion {
		log.Printf("the running factory is %s, the binary on disk %s", processVersion, fileVersion)
	}

	switch {
	case !processVersion.less(target):
		u.once("current", target.String(), fmt.Sprintf("up to date: the running factory is %s, the newest release %s", processVersion, newest.version))
		return nil
	case !fileVersion.less(target) && process.Draining:
		log.Printf("the binary on disk is %s and the factory %s drains; %s", fileVersion, processVersion, waitsFor(process.Now))
		return nil
	case !fileVersion.less(target):
		log.Printf("the binary on disk is %s and the factory runs %s without draining; sending SIGHUP again", fileVersion, processVersion)
		return u.hangup()
	}
	if err := u.install(newest); err != nil {
		return err
	}
	if process.Draining {
		log.Printf("installed %s; the factory drains already and systemd starts it when the drain ends: %s", newest.version, waitsFor(process.Now))
		return nil
	}
	log.Printf("installed %s", newest.version)
	if len(process.Now) > 0 {
		log.Printf("a run in .now stops no install: %s", waitsFor(process.Now))
	}
	return u.hangup()
}

// waitsFor names the run the drain waits for.
func waitsFor(now []Run) string {
	if len(now) == 0 {
		return "the drain waits for no run and ends in a moment"
	}
	names := make([]string, 0, len(now))
	for _, r := range now {
		names = append(names, fmt.Sprintf("run %d (%s#%d)", r.ID, r.Repository, r.Issue))
	}
	return "the drain waits for " + strings.Join(names, ", ")
}

// unreachable is a tick whose factory does not answer. A service the operator stopped gets the
// newer file and is not started; any other state is one the tick does not act on.
func (u *updater) unreachable(listen string, newest release, fileVersion semver) error {
	active, result, err := u.serviceState()
	if err != nil {
		return err
	}
	if active != "inactive" || result != "success" {
		return fmt.Errorf("the factory does not answer on http://%s/api/line and %s is %s with the result %s; the tick does nothing and tries again at the next one",
			listen, factoryUnit, active, result)
	}
	if !fileVersion.less(newest.version) {
		u.once("current", "stopped "+fileVersion.String(), fmt.Sprintf("%s is stopped and the binary on disk is %s, the newest release %s; nothing to install", factoryUnit, fileVersion, newest.version))
		return nil
	}
	if err := u.install(newest); err != nil {
		return err
	}
	log.Printf("installed %s; %s was stopped by the operator (inactive, success), so nothing is started", newest.version, factoryUnit)
	return nil
}

// newestRelease is the highest version among the repository's published factory releases, read
// through the REST API without a login. The latest-release endpoint is not used: factory releases
// are published with latest set to false.
func (u *updater) newestRelease() (release, error) {
	type asset struct {
		Name string `json:"name"`
		URL  string `json:"browser_download_url"`
	}
	type listed struct {
		Tag        string  `json:"tag_name"`
		Draft      bool    `json:"draft"`
		Prerelease bool    `json:"prerelease"`
		Assets     []asset `json:"assets"`
	}
	var best *listed
	var bestVersion semver
	for page := 1; ; page++ {
		url := fmt.Sprintf("https://api.github.com/repos/%s/releases?per_page=100&page=%d", releaseRepository, page)
		var releases []listed
		if err := u.getJSON(url, &releases); err != nil {
			return release{}, fmt.Errorf("the releases of %s cannot be read: %w", releaseRepository, err)
		}
		for i := range releases {
			r := &releases[i]
			if r.Draft || r.Prerelease || !strings.HasPrefix(r.Tag, releaseTagPrefix) {
				continue
			}
			v, ok := parseSemver(strings.TrimPrefix(r.Tag, releaseTagPrefix))
			if ok && (best == nil || bestVersion.less(v)) {
				best, bestVersion = r, v
			}
		}
		if len(releases) < 100 {
			break
		}
	}
	if best == nil {
		return release{}, fmt.Errorf("%s has no published factory release", releaseRepository)
	}
	found := release{version: bestVersion, tag: best.Tag}
	binary, bundle := "factory-linux-"+runtime.GOARCH, "factory-v"+bestVersion.String()+".sigstore.json"
	for _, a := range best.Assets {
		switch a.Name {
		case binary:
			found.binary = a.URL
		case bundle:
			found.bundle = a.URL
		}
	}
	return found, nil
}

func (u *updater) getJSON(url string, into any) error {
	request, err := http.NewRequestWithContext(u.ctx, http.MethodGet, url, nil)
	if err != nil {
		return err
	}
	request.Header.Set("Accept", "application/vnd.github+json")
	response, err := u.client.Do(request)
	if err != nil {
		return err
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return fmt.Errorf("%s answered %s", url, response.Status)
	}
	return json.NewDecoder(response.Body).Decode(into)
}

// running reads the line endpoint. A factory that does not answer is not an error here: whether
// that is a stop the operator made is systemd's to say.
func (u *updater) running(listen string) (runningFactory, bool, error) {
	var process runningFactory
	request, err := http.NewRequestWithContext(u.ctx, http.MethodGet, "http://"+listen+"/api/line", nil)
	if err != nil {
		return process, false, err
	}
	client := &http.Client{Timeout: 30 * time.Second}
	response, err := client.Do(request)
	if err != nil {
		return process, false, nil
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return process, false, fmt.Errorf("the factory's http://%s/api/line answered %s; the tick does nothing", listen, response.Status)
	}
	if err := json.NewDecoder(response.Body).Decode(&process); err != nil {
		return process, false, fmt.Errorf("the factory's http://%s/api/line cannot be read: %w", listen, err)
	}
	return process, true, nil
}

// fileVersion is what a binary reports on -version: the version of the file, never of the process.
func (u *updater) fileVersion(path string) (semver, error) {
	out, err := exec.CommandContext(u.ctx, path, "-version").Output()
	if err != nil {
		return semver{}, fmt.Errorf("%s -version failed: %w", path, err)
	}
	said := strings.TrimSpace(string(out))
	v, ok := parseSemver(strings.TrimPrefix(said, "factory "))
	if !ok || !strings.HasPrefix(said, "factory ") {
		return semver{}, fmt.Errorf("%s -version printed %q, not factory major.minor.patch", path, said)
	}
	return v, nil
}

// serviceState is the service's ActiveState and Result as systemd reports them.
func (u *updater) serviceState() (string, string, error) {
	out, err := exec.CommandContext(u.ctx, systemctlCommand, "show", factoryUnit, "--property=ActiveState,Result").Output()
	if err != nil {
		return "", "", fmt.Errorf("%s show %s failed: %w", systemctlCommand, factoryUnit, err)
	}
	var active, result string
	for _, line := range strings.Split(string(out), "\n") {
		if value, ok := strings.CutPrefix(line, "ActiveState="); ok {
			active = value
		}
		if value, ok := strings.CutPrefix(line, "Result="); ok {
			result = value
		}
	}
	return active, result, nil
}

// hangup has the running factory drain, to the factory's main process alone so the worker of the
// run that is going does not die of the signal.
func (u *updater) hangup() error {
	out, err := exec.CommandContext(u.ctx, systemctlCommand, "kill", "--kill-whom=main", "-s", "HUP", factoryUnit).CombinedOutput()
	if err != nil {
		return fmt.Errorf("%s kill -s HUP %s failed: %w: %s", systemctlCommand, factoryUnit, err, strings.TrimSpace(string(out)))
	}
	log.Printf("sent SIGHUP to %s: it drains and systemd starts the binary on disk", factoryUnit)
	return nil
}

// install downloads the release's binary and bundle, verifies the attestation and renames the
// binary over the installed one, keeping that one beside it as the previous binary. A refused or
// broken file installs nothing.
func (u *updater) install(r release) error {
	if r.binary == "" || r.bundle == "" {
		return fmt.Errorf("the release %s lacks factory-linux-%s or its attestation bundle; nothing is installed", r.tag, runtime.GOARCH)
	}
	work, err := os.MkdirTemp(u.state, "tick-")
	if err != nil {
		return fmt.Errorf("the updater's work directory cannot be made in %s: %w", u.state, err)
	}
	defer os.RemoveAll(work)
	// The new binary is written beside the installed one, so the rename that installs it stays on
	// one file system and is atomic.
	fresh, err := os.CreateTemp(filepath.Dir(u.exe), ".factory-new-")
	if err != nil {
		return fmt.Errorf("the new binary cannot be written beside %s: %w", u.exe, err)
	}
	freshPath := fresh.Name()
	installed := false
	defer func() {
		if !installed {
			os.Remove(freshPath)
		}
	}()
	err = u.download(r.binary, fresh)
	if closed := fresh.Close(); err == nil {
		err = closed
	}
	if err != nil {
		return fmt.Errorf("factory-linux-%s of %s cannot be downloaded: %w; nothing is installed", runtime.GOARCH, r.tag, err)
	}
	bundle := filepath.Join(work, "bundle.sigstore.json")
	if err := u.downloadTo(r.bundle, bundle); err != nil {
		return fmt.Errorf("the attestation bundle of %s cannot be downloaded: %w; nothing is installed", r.tag, err)
	}
	if err := u.verify(freshPath, bundle, r, work); err != nil {
		return err
	}
	if err := os.Chmod(freshPath, 0o755); err != nil {
		return fmt.Errorf("the new binary cannot be made executable: %w; nothing is installed", err)
	}
	if got, err := u.fileVersion(freshPath); err != nil || got != r.version {
		return fmt.Errorf("the verified binary of %s does not report that version (%v, %v); nothing is installed", r.tag, got, err)
	}
	previous := u.exe + ".previous"
	if err := os.Remove(previous); err != nil && !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("the previous binary %s cannot be replaced: %w; nothing is installed", previous, err)
	}
	if err := os.Link(u.exe, previous); err != nil {
		return fmt.Errorf("the installed binary cannot be kept as %s: %w; nothing is installed", previous, err)
	}
	if err := os.Rename(freshPath, u.exe); err != nil {
		return fmt.Errorf("the new binary cannot be renamed over %s: %w; nothing is installed", u.exe, err)
	}
	installed = true
	log.Printf("verified the attestation of %s and installed it as %s, keeping the one before as %s", r.tag, u.exe, previous)
	return nil
}

func (u *updater) download(url string, into io.Writer) error {
	request, err := http.NewRequestWithContext(u.ctx, http.MethodGet, url, nil)
	if err != nil {
		return err
	}
	response, err := u.client.Do(request)
	if err != nil {
		return err
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return fmt.Errorf("%s answered %s", url, response.Status)
	}
	_, err = io.Copy(into, response.Body)
	return err
}

func (u *updater) downloadTo(url, path string) error {
	file, err := os.Create(path)
	if err != nil {
		return err
	}
	err = u.download(url, file)
	if closed := file.Close(); err == nil {
		err = closed
	}
	return err
}

// verify runs gh's attestation check with the five-part policy and without a login. gh gets a home
// of its own in the tick's work directory, which the tick removes, so the trust roots are fetched
// through TUF on every tick and no token of anybody's is read.
func (u *updater) verify(binary, bundle string, r release, work string) error {
	cmd := exec.CommandContext(u.ctx, ghCommand, "attestation", "verify", binary,
		"--repo", releaseRepository,
		"--bundle", bundle,
		"--cert-identity-regex", releaseIdentity,
		"--source-ref", "refs/tags/"+r.tag,
		"--cert-oidc-issuer", actionsIssuer,
		"--deny-self-hosted-runners",
		"--predicate-type", slsaProvenance)
	cmd.Env = []string{
		"PATH=/usr/bin:/bin",
		"HOME=" + work,
		"XDG_CONFIG_HOME=" + filepath.Join(work, "config"),
		"XDG_CACHE_HOME=" + filepath.Join(work, "cache"),
		"XDG_DATA_HOME=" + filepath.Join(work, "data"),
		"XDG_STATE_HOME=" + filepath.Join(work, "state"),
		"GH_CONFIG_DIR=" + filepath.Join(work, "gh"),
		"GH_PROMPT_DISABLED=1",
		"GH_NO_UPDATE_NOTIFIER=1",
	}
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("the attestation of %s was refused (%v): %s; nothing is installed, and the next tick tries again",
			r.tag, err, strings.Join(strings.Fields(string(out)), " "))
	}
	return nil
}

// once logs a message when what it says about key differs from what the last tick said, so the
// journal carries a state change once and not every hour.
func (u *updater) once(key, value, message string) {
	if u.said[key] == value {
		return
	}
	u.said[key] = value
	log.Print(message)
}

func (u *updater) statePath() string { return filepath.Join(u.state, "said.json") }

// loadState reads what earlier ticks said. The directory is made root's alone when it is missing.
func (u *updater) loadState() error {
	u.said = map[string]string{}
	if err := os.MkdirAll(u.state, 0o700); err != nil {
		return fmt.Errorf("the updater's directory %s cannot be made: %w; the tick runs as root", u.state, err)
	}
	raw, err := os.ReadFile(u.statePath())
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("%s cannot be read: %w", u.statePath(), err)
	}
	if err := json.Unmarshal(raw, &u.said); err != nil {
		// A state that does not read only costs a line said twice.
		u.said = map[string]string{}
	}
	return nil
}

func (u *updater) saveState() error {
	raw, err := json.Marshal(u.said)
	if err != nil {
		return err
	}
	temp := u.statePath() + ".new"
	if err := os.WriteFile(temp, raw, 0o600); err != nil {
		return fmt.Errorf("the updater's state cannot be written: %w", err)
	}
	if err := os.Rename(temp, u.statePath()); err != nil {
		return fmt.Errorf("the updater's state cannot be written: %w", err)
	}
	return nil
}

package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"path"
	"regexp"
	"slices"
	"strconv"
	"strings"
)

// Change classes ([ADR 0041]): an ordered list of rules a connected repository writes, each with a
// name, the path patterns it covers, the gate command it runs before the pull request and, optionally,
// the reviewers it asks. The factory determines the class from the files changed between the merge base
// and the head: the first class whose patterns cover every changed file applies, and when none does the
// built-in class full applies, the repository's single gate command (gate.command) and the review's
// panel. It determines it before the review, whose reviewers it decides, and again before the gate on
// the final head, because a fix can add a file the first class does not cover. CI runs the full gate
// on every pull request all the same, so what a class leaves out is caught in the ci stage.
//
// [ADR 0041]: ../docs/adr/0041-a-change-class-decides-the-gate-and-the-reviewers-before-the-pull-request.md

// classFull is the built-in class, which applies when no class of the repository does.
const classFull = "full"

// changeClass is one class as a run reads it. Reviewers is nil for a class that names none, which
// asks the review's panel.
type changeClass struct {
	Name      string
	Paths     []string
	Gate      gateCommand
	Reviewers []string
}

// classKnob is one class as the configuration writes it. The gate is kept raw, so a gate that is left
// out is told apart from an empty list, which is a class without a gate.
type classKnob struct {
	Name      string          `json:"name"`
	Paths     []string        `json:"paths"`
	Gate      json.RawMessage `json:"gate"`
	Reviewers *[]string       `json:"reviewers"`
}

const classFields = "name, paths, gate, reviewers"

// classExample is the shape of a class, which every refusal of one ends with.
const classExample = `write a class as {"name": "docs", "paths": ["docs/**", "*.md"], "gate": [], "reviewers": ["docs"]}`

// UnmarshalJSON refuses a field a class does not have and names the ones it has.
func (k *classKnob) UnmarshalJSON(raw []byte) error {
	type plain classKnob // without this method, so the object is decoded and not read again by it
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	var read plain
	if err := decoder.Decode(&read); err != nil {
		return fmt.Errorf("a change class: %w; its fields are %s, and %s", err, classFields, classExample)
	}
	*k = classKnob(read)
	return nil
}

// A class's name is recorded on the run, shown on the dashboard and written into the pull request, so
// it is a word and not a sentence.
var className = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,39}$`)

// readClasses is the classes a review object writes, in their order, or the reason they are refused.
func readClasses(knobs []classKnob) ([]changeClass, error) {
	out := []changeClass{}
	seen := map[string]bool{}
	for i, k := range knobs {
		c, err := k.read()
		if err != nil {
			return nil, fmt.Errorf("class %d: %v", i+1, err)
		}
		if seen[c.Name] {
			return nil, fmt.Errorf("class %d: the name %q is taken by an earlier class; give every class a name of its own", i+1, c.Name)
		}
		seen[c.Name] = true
		out = append(out, c)
	}
	return out, nil
}

// read is one class of the configuration checked: a name, at least one pattern and every one of them a
// pattern, a gate command in one of its forms, and reviewers the review has.
func (k classKnob) read() (changeClass, error) {
	name := strings.TrimSpace(k.Name)
	switch {
	case name == "":
		return changeClass{}, fmt.Errorf("the class has no name; name it, such as \"docs\", and %s", classExample)
	case !className.MatchString(k.Name):
		return changeClass{}, fmt.Errorf("the name %q is not a class name; write it as letters, digits, dots, hyphens and underscores, at most 40, such as \"docs\"", k.Name)
	case k.Name == classFull:
		return changeClass{}, fmt.Errorf("the name %q is the built-in class that applies when no other does; name the class otherwise", classFull)
	case len(k.Paths) == 0:
		return changeClass{}, fmt.Errorf("the class %q has no paths; name the files it covers as patterns, such as [\"docs/**\", \"*.md\"]", k.Name)
	}
	for _, pattern := range k.Paths {
		if err := checkPattern(pattern); err != nil {
			return changeClass{}, fmt.Errorf("the class %q: %v", k.Name, err)
		}
	}
	gate, err := readGate(k.Name, k.Gate)
	if err != nil {
		return changeClass{}, err
	}
	c := changeClass{Name: k.Name, Paths: slices.Clone(k.Paths), Gate: gate}
	if k.Reviewers != nil {
		if c.Reviewers, err = readReviewers(*k.Reviewers); err != nil {
			return changeClass{}, fmt.Errorf("the class %q: %v, or leave reviewers out to ask the review's panel", k.Name, err)
		}
	}
	return c, nil
}

// readGate is a class's gate command, which the class has to name: one of the forms of gateCommand.
func readGate(name string, raw json.RawMessage) (gateCommand, error) {
	raw = bytes.TrimSpace(raw)
	if len(raw) == 0 || string(raw) == "null" {
		return gateCommand{}, fmt.Errorf("the class %q has no gate; write it as %s", name, gateForms)
	}
	var gate gateCommand
	if err := json.Unmarshal(raw, &gate); err != nil {
		return gateCommand{}, fmt.Errorf("the class %q: %v", name, err)
	}
	return gate, nil
}

// gateCommand is a gate command in one of the four forms the configuration writes it in, for a change
// class and for the repository (gate.command): a list of arguments, the program first, which the factory
// runs in the worktree without a shell; an empty list, which is no gate; "ci", which hands the gate to
// GitHub CI and reads every check of the pushed head; and {"ci": ["check", "browser"]}, which reads the
// named checks only (ciGate).
type gateCommand struct {
	Args   []string // the command run in the worktree; empty for no gate and for a gate on CI
	CI     bool
	Checks []string // the checks a gate on CI reads, and every check when empty
}

// gateForms is the four forms, which every refusal of a gate command names.
const gateForms = `a list of arguments such as ["make", "check"], [] for no gate, "ci" for every check on CI, or {"ci": ["check", "browser"]} for the named checks on CI`

// UnmarshalJSON reads a gate command in one of its forms and refuses anything else, naming the forms.
// A null is left as it is, which is the convention of the package; a class that names none is refused
// by readGate.
func (g *gateCommand) UnmarshalJSON(raw []byte) error {
	raw = bytes.TrimSpace(raw)
	if string(raw) == "null" {
		return nil
	}
	refused := fmt.Errorf("the gate %s is none of the forms of a gate; write it as %s", raw, gateForms)
	switch raw[0] {
	case '[':
		var args []string
		if json.Unmarshal(raw, &args) != nil {
			return refused
		}
		if len(args) > 0 && strings.TrimSpace(args[0]) == "" {
			return fmt.Errorf("the gate %s names no program; write it as %s", raw, gateForms)
		}
		*g = gateCommand{Args: args}
		return nil
	case '"':
		var word string
		if json.Unmarshal(raw, &word) != nil || word != "ci" {
			return refused
		}
		*g = gateCommand{CI: true}
		return nil
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	var named struct {
		CI []string `json:"ci"`
	}
	if decoder.Decode(&named) != nil || len(named.CI) == 0 {
		return refused
	}
	seen := map[string]bool{}
	for _, check := range named.CI {
		switch {
		case strings.TrimSpace(check) == "":
			return fmt.Errorf("the gate %s names a check without a name; name each check as its pull request shows it, such as \"check\"", raw)
		case seen[check]:
			return fmt.Errorf("the gate %s names the check %q twice; remove the duplicate", raw, check)
		}
		seen[check] = true
	}
	*g = gateCommand{CI: true, Checks: named.CI}
	return nil
}

// MarshalJSON writes a gate command in the form it was read in, which is how a run records it.
func (g gateCommand) MarshalJSON() ([]byte, error) {
	switch {
	case g.CI && len(g.Checks) > 0:
		return json.Marshal(map[string][]string{"ci": g.Checks})
	case g.CI:
		return json.Marshal("ci")
	case g.Args == nil:
		return []byte("[]"), nil
	}
	return json.Marshal(g.Args)
}

// none says that the command is no gate at all.
func (g gateCommand) none() bool { return !g.CI && len(g.Args) == 0 }

// String is a gate command as a person reads it: the command line, "ci", or "ci:" with the checks.
func (g gateCommand) String() string {
	switch {
	case g.CI && len(g.Checks) > 0:
		return "ci: " + strings.Join(g.Checks, ", ")
	case g.CI:
		return "ci"
	case len(g.Args) == 0:
		return "none"
	}
	return commandLine(g.Args)
}

// clone is a copy that shares no slice with the command it was made of.
func (g gateCommand) clone() gateCommand {
	return gateCommand{Args: slices.Clone(g.Args), CI: g.CI, Checks: slices.Clone(g.Checks)}
}

// readReviewers is a list of reviewers as the review has them: at least one, each known, none twice.
func readReviewers(names []string) ([]string, error) {
	if len(names) == 0 {
		return nil, fmt.Errorf("reviewers is empty; name at least one of %s", strings.Join(defaultReview.Reviewers, ", "))
	}
	seen := map[string]bool{}
	for _, name := range names {
		if _, known := reviewers[name]; !known {
			return nil, fmt.Errorf("reviewers carries %q, which is no reviewer; the reviewers are %s", name, strings.Join(defaultReview.Reviewers, ", "))
		}
		if seen[name] {
			return nil, fmt.Errorf("reviewers names %q twice; remove the duplicate", name)
		}
		seen[name] = true
	}
	return slices.Clone(names), nil
}

// checkPattern says why a path pattern is no pattern, or nil. A pattern is a path relative to the root
// of the repository, its parts between slashes each a pattern of path.Match or ** for any number of
// directories, such as docs/** or *.md; a pattern without a slash is a file at the root.
func checkPattern(pattern string) error {
	fix := `write it relative to the repository's root, such as "docs/**" for everything under docs or "*.md" for the Markdown files at the root`
	switch {
	case strings.TrimSpace(pattern) == "":
		return fmt.Errorf("a path pattern is empty; %s", fix)
	case strings.HasPrefix(pattern, "/"):
		return fmt.Errorf("the path pattern %q starts at /; %s", pattern, fix)
	case strings.HasSuffix(pattern, "/"):
		return fmt.Errorf("the path pattern %q ends in /, which no file does; write %q for everything under it", pattern, pattern+"**")
	}
	for _, part := range strings.Split(pattern, "/") {
		switch {
		case part == "":
			return fmt.Errorf("the path pattern %q has an empty part between two slashes; %s", pattern, fix)
		case part == "." || part == "..":
			return fmt.Errorf("the path pattern %q has the part %q, which no changed file has; %s", pattern, part, fix)
		case part != "**" && strings.Contains(part, "**"):
			return fmt.Errorf("the path pattern %q has ** inside a part; ** stands alone between slashes, as in \"docs/**/*.md\"", pattern)
		}
		if _, err := path.Match(part, ""); err != nil {
			return fmt.Errorf("the path pattern %q is not a pattern (%v); %s", pattern, err, fix)
		}
	}
	return nil
}

// matches says whether a pattern checked by checkPattern covers a file, both slash-separated paths
// relative to the repository's root.
func matches(pattern, file string) bool {
	return matchParts(strings.Split(pattern, "/"), strings.Split(file, "/"))
}

func matchParts(pattern, file []string) bool {
	if len(pattern) == 0 {
		return len(file) == 0
	}
	if pattern[0] == "**" {
		// Any number of parts, none included.
		for i := 0; i <= len(file); i++ {
			if matchParts(pattern[1:], file[i:]) {
				return true
			}
		}
		return false
	}
	if len(file) == 0 {
		return false
	}
	ok, _ := path.Match(pattern[0], file[0])
	return ok && matchParts(pattern[1:], file[1:])
}

// covers says whether a class covers a file: whether one of its patterns matches it.
func (c changeClass) covers(file string) bool {
	for _, pattern := range c.Paths {
		if matches(pattern, file) {
			return true
		}
	}
	return false
}

// Classed is one determination of the class, as the run records it: the class that applied, what it
// was determined for (the review or the gate), the commit and the files it was read from, and what the
// class decided: the gate command, in the form the configuration writes it, and the reviewers.
type Classed struct {
	Class     string      `json:"class"`
	For       string      `json:"for"`
	Head      string      `json:"head"`
	Files     int         `json:"files"`
	Gate      gateCommand `json:"gate"`
	Reviewers []string    `json:"reviewers"`
	// Why says what decided it: the class whose patterns cover every file, or the first file no class
	// covers.
	Why string `json:"why"`
}

// What a class is determined for.
const (
	classForReview = "review"
	classForGate   = "gate"
)

// classify is the class of a change: the first class that covers every changed file, and full when none
// does or no file changed. Reviewers is the panel a class that names none asks, and gate the
// repository's gate command, which the class full runs. The class full asks all five reviewers, since
// it is the change no class vouches for, unless the repository has no class at all, where it is the
// panel the repository configured.
func classify(classes []changeClass, files []string, panel []string, gate gateCommand) (changeClass, string) {
	if len(classes) == 0 {
		return changeClass{Name: classFull, Gate: gate.clone(), Reviewers: slices.Clone(panel)}, "the repository has no change class"
	}
	full := changeClass{Name: classFull, Gate: gate.clone(), Reviewers: slices.Clone(defaultReview.Reviewers)}
	if len(files) == 0 {
		return full, "no file changed"
	}
	for _, c := range classes {
		if !slices.ContainsFunc(files, func(file string) bool { return !c.covers(file) }) {
			if c.Reviewers == nil {
				c.Reviewers = slices.Clone(panel)
			}
			return c, fmt.Sprintf("the class %s covers all %d changed file(s)", c.Name, len(files))
		}
	}
	for _, file := range files {
		if !slices.ContainsFunc(classes, func(c changeClass) bool { return c.covers(file) }) {
			return full, fmt.Sprintf("no class covers every changed file, and %s is outside every class", file)
		}
	}
	return full, "no class covers every changed file"
}

// changedFiles is the files changed between the merge base of the branch's base and the head, the
// paths before and after a rename both, since either is a place the change touches. Fake mode reads its
// canned change (cannedFiles).
func (f *Factory) changedFiles(ctx context.Context, entry Entry, claim claimed, panel Panel) ([]string, error) {
	if f.fake {
		return cannedFiles(entry.scenario, panel), nil
	}
	out, reason, err := command(ctx, gitTimeout, "git", "-C", claim.worktree, "diff", "--name-only", "--no-renames", "-z", "origin/"+claim.base+"...HEAD")
	if err != nil {
		return nil, fmt.Errorf("git diff --name-only origin/%s...HEAD: %s", claim.base, reason)
	}
	files := []string{}
	for _, file := range strings.Split(string(out), "\x00") {
		if file != "" {
			files = append(files, file)
		}
	}
	return files, nil
}

// determine determines the class of the change at the head the worktree is at, for the review or the
// gate, records it on the panel and in the log, and answers with it.
func (f *Factory) determine(ctx context.Context, r *Run, entry Entry, claim claimed, panel *Panel, knobs reviewSettings, what string) (Classed, error) {
	head, err := f.head(ctx, claim)
	if err != nil {
		return Classed{}, err
	}
	if f.fake {
		head = fakeHead(*panel)
	}
	files, err := f.changedFiles(ctx, entry, claim, *panel)
	if err != nil {
		return Classed{}, err
	}
	class, why := classify(knobs.Classes, files, knobs.Reviewers, f.gateFor(entry.Repository).Command)
	classed := Classed{Class: class.Name, For: what, Head: head, Files: len(files), Gate: class.Gate, Reviewers: class.Reviewers, Why: why}
	panel.Classes = append(panel.Classes, classed)
	gate := "no gate"
	if !class.Gate.none() {
		gate = "the gate " + class.Gate.String()
	}
	f.runs.event(r, Event{Kind: "factory", Title: fmt.Sprintf("change class %s for the %s", class.Name, what),
		Body: fmt.Sprintf("%s at %s: %s, the reviewers %s", why, short(head), gate, strings.Join(class.Reviewers, ", "))})
	return classed, nil
}

// classedFor is the last determination a panel recorded for the review or the gate, and false when it
// recorded none.
func classedFor(panel Panel, what string) (Classed, bool) {
	for i := len(panel.Classes) - 1; i >= 0; i-- {
		if panel.Classes[i].For == what {
			return panel.Classes[i], true
		}
	}
	return Classed{}, false
}

// classLine is the determinations of a panel as the panel summary carries them, or "" when it has none.
func classLine(panel Panel) string {
	parts := []string{}
	for _, c := range panel.Classes {
		parts = append(parts, fmt.Sprintf("%s for the %s at %s", c.Class, c.For, short(c.Head)))
	}
	if len(parts) == 0 {
		return ""
	}
	return "change_class: " + strings.Join(parts, ", ")
}

// commandLine is a gate command as a person reads it: its arguments with a space between, each one
// that would not read as one argument quoted.
func commandLine(args []string) string {
	out := make([]string, 0, len(args))
	for _, arg := range args {
		if arg == "" || strings.ContainsAny(arg, " \t\n\"'\\") {
			arg = strconv.Quote(arg)
		}
		out = append(out, arg)
	}
	return strings.Join(out, " ")
}

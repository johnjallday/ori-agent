// Package projectinspect reads a small, fixed set of files from a software
// project the user has selected, so Ori can explain a setup and, later, give a
// model evidence to assess. It is the only place that opens a file in such a
// project, and it is called only when the user asks for an inspection or
// starts an assessment: recognizing a folder never calls it.
//
// What it will open is decided by name, at the project's root, from an
// allowlist. It never opens anything else, never follows a symbolic link,
// never runs a command, and never touches the network. Everything it returns
// is reference data: text copied out of a repository is not an instruction to
// Ori or to a model, whatever it says.
package projectinspect

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/johnjallday/ori-agent/internal/sensitive"
)

// Limits of one inspection. They are deliberately small: the point is the
// handful of files that say what a project is and how it is built.
const (
	DefaultMaxFiles      = 24
	DefaultMaxFileBytes  = 48 * 1024
	DefaultMaxTotalBytes = 256 * 1024
	DefaultBudget        = 2 * time.Second
	maxWorkflowFiles     = 4
	binarySniffBytes     = 4096
	// WithheldLine replaces a line that looks like a credential.
	WithheldLine = "[line withheld: it looks like a credential]"
)

// ErrNotDirectory reports a root that is not a real directory.
var ErrNotDirectory = errors.New("projectinspect: the project folder is not a directory")

// Class is why a file was read.
type Class string

const (
	ClassInstructions Class = "instructions"
	ClassReadme       Class = "readme"
	ClassManifest     Class = "manifest"
	ClassBuild        Class = "build"
	ClassWorkflow     Class = "workflow"
)

// Partial reasons.
const (
	PartialFiles = "files"
	PartialBytes = "bytes"
	PartialTime  = "time"
)

// Gap codes: things an honest report says were missing or not read.
const (
	GapNoReadme        = "no_readme"
	GapNoInstructions  = "no_instructions"
	GapNoCommands      = "no_declared_commands"
	GapSeveralGuides   = "several_instruction_files"
	GapSkippedLink     = "skipped_link"
	GapSkippedBinary   = "skipped_binary"
	GapUnreadable      = "unreadable"
	GapTruncated       = "truncated"
	GapWithheld        = "withheld_lines"
	GapGitUnverified   = "git_layout_unverified"
	GapRemoteUnread    = "remote_unreadable"
	GapUnsupportedFile = "unsupported_format"
	GapMoreWorkflows   = "more_workflows"
)

// Options tunes one inspection. Zero values take the defaults.
type Options struct {
	MaxFiles      int
	MaxFileBytes  int
	MaxTotalBytes int
	Budget        time.Duration
	Now           func() time.Time
}

func (o Options) withDefaults() Options {
	if o.MaxFiles <= 0 {
		o.MaxFiles = DefaultMaxFiles
	}
	if o.MaxFileBytes <= 0 {
		o.MaxFileBytes = DefaultMaxFileBytes
	}
	if o.MaxTotalBytes <= 0 {
		o.MaxTotalBytes = DefaultMaxTotalBytes
	}
	if o.Budget <= 0 {
		o.Budget = DefaultBudget
	}
	if o.Now == nil {
		o.Now = time.Now
	}
	return o
}

// Document is one file that was read: its path relative to the project root
// (always with forward slashes), why it was read, and its text after
// credential-shaped lines were withheld.
type Document struct {
	Path  string
	Class Class
	Text  string
	// Bytes is how much was read. Truncated says the file was longer.
	Bytes     int
	Truncated bool
	// Withheld counts lines replaced because they looked like credentials.
	Withheld int
}

// Command is a build, test or similar command found in the project. Declared
// commands are written in the project (a Makefile target, a package script).
// Inferred ones are only the usual command for a manifest that is present.
// None was run: Ori has no idea whether any of them works.
type Command struct {
	Kind     string // build, test, lint, run, other
	Command  string
	Source   string // relative path of the file it came from
	Declared bool
}

// Excerpt is a short piece of a document with its source.
type Excerpt struct {
	Source string
	Text   string
}

// Tool is evidence that a development tool is configured for the project. It
// is the presence of a configuration file, never proof that the tool is
// installed, signed in, or working.
type Tool struct {
	Name   string
	Source string
}

// Gap is something the inspection could not or did not establish.
type Gap struct {
	Code   string
	Source string // relative path when the gap is about one file
}

// Evidence is the result of one inspection.
type Evidence struct {
	// Revision changes whenever what was read changes.
	Revision    string
	InspectedAt time.Time
	Documents   []Document
	// Purpose is the opening of the README, when there is one.
	Purpose *Excerpt
	// Stack names the languages or platforms the manifests declare.
	Stack       []string
	Commands    []Command
	Conventions []Excerpt
	Git         Git
	Tools       []Tool
	Gaps        []Gap
	// Partial says a limit stopped the inspection before every allowed file was
	// considered; PartialReason says which.
	Partial       bool
	PartialReason string
	// Layout is the project's top level: the names in its root, folders first.
	// It comes from listing that one directory; no entry in it was opened
	// because it is listed here. LayoutMore counts entries left out of a long
	// listing. Neither is part of Revision: a file added beside the ones that
	// were read does not change what a review was made from.
	Layout     []LayoutEntry
	LayoutMore int
}

// LayoutEntry is one name at the top level of a project.
type LayoutEntry struct {
	Name string
	Dir  bool
}

// maxLayoutEntries bounds the top-level listing kept as evidence.
const maxLayoutEntries = 60

// Document returns the document read from a relative path, if any.
func (e Evidence) Document(path string) (Document, bool) {
	for _, doc := range e.Documents {
		if doc.Path == path {
			return doc, true
		}
	}
	return Document{}, false
}

// rootNames is the allowlist of root-level files, in reading order: the
// project's own instructions first, then what it is, then how it is built.
// Names are matched without regard to case. Nothing outside this list and the
// workflow allowlist below is ever opened.
var rootNames = []struct {
	name  string
	class Class
}{
	{"agents.md", ClassInstructions},
	{"claude.md", ClassInstructions},
	{"contributing.md", ClassInstructions},
	{"readme.md", ClassReadme},
	{"readme", ClassReadme},
	{"readme.rst", ClassReadme},
	{"readme.txt", ClassReadme},
	{"go.mod", ClassManifest},
	{"package.json", ClassManifest},
	{"cargo.toml", ClassManifest},
	{"pyproject.toml", ClassManifest},
	{"requirements.txt", ClassManifest},
	{"gemfile", ClassManifest},
	{"pom.xml", ClassManifest},
	{"build.gradle", ClassManifest},
	{"build.gradle.kts", ClassManifest},
	{"package.swift", ClassManifest},
	{"composer.json", ClassManifest},
	{"cmakelists.txt", ClassBuild},
	{"makefile", ClassBuild},
	{"justfile", ClassBuild},
	{"taskfile.yml", ClassBuild},
	{".gitlab-ci.yml", ClassWorkflow},
}

// toolMarkers are configuration files whose presence says a development tool
// is set up for the project. Only their existence is checked.
var toolMarkers = []struct {
	dir, file, name string
}{
	{".herdr", "devflow.toml", "Herdr devflow"},
}

// Inspect reads the allowlisted files at the root of a project.
//
// root must be the canonical directory of a selection the server holds. A file
// that cannot be read, is a link, or looks binary is skipped and reported as a
// gap; only a root that is not a directory is an error.
func Inspect(ctx context.Context, root string, opts Options) (Evidence, error) {
	opts = opts.withDefaults()
	root = filepath.Clean(strings.TrimSpace(root))
	info, err := os.Lstat(root) // #nosec G304 G703 -- root is a canonical server-held selection, never browser input
	if err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 || !filepath.IsAbs(root) {
		return Evidence{}, ErrNotDirectory
	}
	run := &inspection{ctx: ctx, root: root, opts: opts, start: opts.Now()}
	run.evidence.InspectedAt = run.start.UTC()

	entries := run.listRoot()
	for _, allowed := range rootNames {
		if run.stopped() {
			break
		}
		if name, ok := entries[allowed.name]; ok {
			run.read(name, allowed.class)
		}
	}
	run.readWorkflows()
	run.findTools()
	run.evidence.Git = inspectGit(root, &run.evidence.Gaps)
	describe(&run.evidence)
	run.evidence.Revision = revisionOf(run.evidence)
	return run.evidence, nil
}

type inspection struct {
	ctx      context.Context
	root     string
	opts     Options
	start    time.Time
	total    int
	evidence Evidence
}

// stopped reports whether a limit or the caller has ended the inspection.
func (r *inspection) stopped() bool {
	if r.evidence.Partial {
		return true
	}
	switch {
	case r.ctx.Err() != nil || r.opts.Now().Sub(r.start) > r.opts.Budget:
		r.evidence.Partial, r.evidence.PartialReason = true, PartialTime
	case len(r.evidence.Documents) >= r.opts.MaxFiles:
		r.evidence.Partial, r.evidence.PartialReason = true, PartialFiles
	case r.total >= r.opts.MaxTotalBytes:
		r.evidence.Partial, r.evidence.PartialReason = true, PartialBytes
	}
	return r.evidence.Partial
}

// listRoot maps each root entry's lower-case name to its real name. It lists
// one directory and opens nothing.
func (r *inspection) listRoot() map[string]string {
	names := map[string]string{}
	listed, err := os.ReadDir(r.root)
	if err != nil {
		return names
	}
	var layout []LayoutEntry
	for _, entry := range listed {
		lower := strings.ToLower(entry.Name())
		if _, taken := names[lower]; !taken {
			names[lower] = entry.Name()
		}
		// A link is listed as a file: it is not followed.
		if layoutName(entry.Name()) {
			layout = append(layout, LayoutEntry{Name: entry.Name(), Dir: entry.IsDir()})
		}
	}
	sort.SliceStable(layout, func(i, j int) bool {
		if layout[i].Dir != layout[j].Dir {
			return layout[i].Dir
		}
		return strings.ToLower(layout[i].Name) < strings.ToLower(layout[j].Name)
	})
	if len(layout) > maxLayoutEntries {
		r.evidence.LayoutMore = len(layout) - maxLayoutEntries
		layout = layout[:maxLayoutEntries]
	}
	r.evidence.Layout = layout
	return names
}

// layoutName reports whether a root entry's name is kept as evidence of the
// project's shape. Hidden entries are left out (settings, version control,
// environment files) apart from the CI folder; so are dependency trees and
// anything whose name says it holds keys or secrets. A name left out here is
// simply not mentioned: nothing about it is reported at all.
func layoutName(name string) bool {
	if !plainFileName(name) {
		return false
	}
	lower := strings.ToLower(name)
	if strings.HasPrefix(lower, ".") {
		return lower == ".github"
	}
	switch lower {
	case "node_modules", "id_rsa", "id_dsa", "id_ecdsa", "id_ed25519":
		return false
	}
	for _, mark := range []string{"secret", "credential", "password", "token"} {
		if strings.Contains(lower, mark) {
			return false
		}
	}
	switch filepath.Ext(lower) {
	case ".pem", ".key", ".p8", ".p12", ".pfx", ".keystore", ".jks", ".kdbx", ".gpg", ".asc", ".env":
		return false
	}
	return true
}

// plainFileName reports whether a file or folder name can be repeated as it
// is: valid text, not overlong, and with no character that would break a line
// or change how the text around it reads. A name is evidence only as a name;
// one that carries a line break could pose as a line of its own wherever it is
// repeated.
func plainFileName(name string) bool {
	if name == "" || !utf8.ValidString(name) || utf8.RuneCountInString(name) > 80 {
		return false
	}
	return !strings.ContainsFunc(name, func(r rune) bool {
		// Control characters, the Unicode line and paragraph separators, and
		// the characters that reorder the text around them.
		return unicode.IsControl(r) || r == 0x2028 || r == 0x2029 || unicode.Is(unicode.Bidi_Control, r)
	})
}

func (r *inspection) gap(code, source string) {
	r.evidence.Gaps = append(r.evidence.Gaps, Gap{Code: code, Source: source})
}

// read opens one allowlisted file below the root and records it. rel uses the
// operating system's separator and is always built by this package.
func (r *inspection) read(rel string, class Class) {
	slash := filepath.ToSlash(rel)
	budget := r.opts.MaxFileBytes
	if left := r.opts.MaxTotalBytes - r.total; left < budget {
		budget = left
	}
	if budget <= 0 {
		return
	}
	data, truncated, err := readRegularFile(filepath.Join(r.root, rel), budget)
	switch {
	case errors.Is(err, errLink):
		r.gap(GapSkippedLink, slash)
		return
	case err != nil:
		r.gap(GapUnreadable, slash)
		return
	}
	sniff := data
	if len(sniff) > binarySniffBytes {
		sniff = sniff[:binarySniffBytes]
	}
	if bytes.IndexByte(sniff, 0) >= 0 {
		r.gap(GapSkippedBinary, slash)
		return
	}
	text, withheld := withholdCredentials(strings.ToValidUTF8(string(data), string(utf8.RuneError)))
	r.total += len(data)
	r.evidence.Documents = append(r.evidence.Documents, Document{
		Path: slash, Class: class, Text: text, Bytes: len(data), Truncated: truncated, Withheld: withheld,
	})
	if truncated {
		r.gap(GapTruncated, slash)
	}
	if withheld > 0 {
		r.gap(GapWithheld, slash)
	}
}

// readWorkflows reads a few continuous-integration workflow files. It enters
// exactly one hidden directory, by its fixed name, and only when both it and
// the workflows directory inside it are real directories.
func (r *inspection) readWorkflows() {
	dir := filepath.Join(".github", "workflows")
	if !realDirectory(filepath.Join(r.root, ".github")) || !realDirectory(filepath.Join(r.root, dir)) {
		return
	}
	listed, err := os.ReadDir(filepath.Join(r.root, dir))
	if err != nil {
		return
	}
	names := make([]string, 0, len(listed))
	unread := false
	for _, entry := range listed {
		lower := strings.ToLower(entry.Name())
		if !strings.HasSuffix(lower, ".yml") && !strings.HasSuffix(lower, ".yaml") {
			continue
		}
		// A workflow's name is chosen by whoever wrote the project and is
		// repeated wherever its text is cited, so only a plain one is read.
		if !plainFileName(entry.Name()) {
			unread = true
			continue
		}
		names = append(names, entry.Name())
	}
	sort.Strings(names)
	for i, name := range names {
		if r.stopped() {
			return
		}
		if i >= maxWorkflowFiles {
			unread = true
			break
		}
		r.read(filepath.Join(dir, name), ClassWorkflow)
	}
	if unread {
		// More workflows than are read is a fact about the workflows, not
		// about the project's README, instructions or manifests.
		r.gap(GapMoreWorkflows, filepath.ToSlash(dir))
	}
}

// findTools records which tool configuration files exist. It checks for a
// name; it never opens the file.
func (r *inspection) findTools() {
	for _, marker := range toolMarkers {
		if !realDirectory(filepath.Join(r.root, marker.dir)) {
			continue
		}
		info, err := os.Lstat(filepath.Join(r.root, marker.dir, marker.file))
		if err == nil && info.Mode().IsRegular() {
			r.evidence.Tools = append(r.evidence.Tools, Tool{Name: marker.name, Source: marker.dir + "/" + marker.file})
		}
	}
}

var errLink = errors.New("projectinspect: not a regular file")

func realDirectory(path string) bool {
	info, err := os.Lstat(path) // #nosec G304 G703 -- path is the server-held root joined with a fixed name from this package
	return err == nil && info.IsDir() && info.Mode()&os.ModeSymlink == 0
}

// readRegularFile reads at most limit bytes of a regular file. It refuses a
// symbolic link or any other kind of entry, and it refuses a file that was
// swapped between the check and the open: the opened file must be the one that
// was checked.
func readRegularFile(path string, limit int) (data []byte, truncated bool, err error) {
	before, err := os.Lstat(path) // #nosec G304 G703 -- path is the server-held root joined with an allowlisted name
	if err != nil {
		return nil, false, err
	}
	if !before.Mode().IsRegular() {
		return nil, false, errLink
	}
	file, err := os.Open(path) // #nosec G304 G703 -- same allowlisted path, verified below to be the file that was checked
	if err != nil {
		return nil, false, err
	}
	defer func() { _ = file.Close() }()
	opened, err := file.Stat()
	if err != nil || !opened.Mode().IsRegular() || !os.SameFile(before, opened) {
		return nil, false, errLink
	}
	data, err = io.ReadAll(io.LimitReader(file, int64(limit)+1))
	if err != nil {
		return nil, false, err
	}
	if len(data) > limit {
		return data[:limit], true, nil
	}
	return data, false, nil
}

// The credential shapes a project's own files tend to carry, beyond the token
// formats the shared check knows. They are deliberately broad: a line withheld
// by mistake costs one line of a README, and a credential let through is sent
// to a model and stored in a report.
var (
	// A PEM block of any kind. The line that opens it is not the secret; the
	// lines between are, so the whole block is withheld.
	pemBlockStart = regexp.MustCompile(`-----BEGIN [A-Z0-9 ]+-----`)
	pemBlockEnd   = regexp.MustCompile(`-----END [A-Z0-9 ]+-----`)
	// scheme://user:password@host
	urlWithPassword = regexp.MustCompile(`[A-Za-z][A-Za-z0-9+.-]*://[^\s/@:]+:[^\s/@]+@`)
	// NAME_ENDING_IN_SECRET = value, in shell, YAML, JSON or TOML. A value that
	// is a reference to something kept elsewhere ($VAR, ${{ secrets.X }},
	// $(command), <placeholder>) is not a credential and is left alone.
	secretAssignment = regexp.MustCompile(`(?i)(password|passwd|secret|token|api[_-]?key|access[_-]?key|private[_-]?key)["']?\s*[:=]\s*["']?[^\s"'$({<\[][^\s"']{5,}`)
	bearerToken      = regexp.MustCompile(`(?i)\bbearer\s+[A-Za-z0-9._~+/=-]{16,}`)
	// Stripe, Google API, GitLab, Google OAuth, and three-part JSON web tokens.
	tokenShape = regexp.MustCompile(`\b(?:[srp]k_(?:live|test)_[0-9A-Za-z]{8,}|AIza[0-9A-Za-z_-]{20,}|glpat-[0-9A-Za-z_-]{10,}|ya29\.[0-9A-Za-z_-]{10,}|eyJ[A-Za-z0-9_-]{10,}\.[A-Za-z0-9_-]{10,}\.[A-Za-z0-9_-]{5,})`)
)

func credentialLine(line string) bool {
	return sensitive.ContainsSecretLikeText(line) || urlWithPassword.MatchString(line) || secretAssignment.MatchString(line) ||
		bearerToken.MatchString(line) || tokenShape.MatchString(line)
}

// withholdCredentials replaces every line that looks like a credential, and
// every line of a PEM block, so an excerpt can be shown, stored or given to a
// model without carrying one. A block cut off before its end is withheld to
// the end of the excerpt.
func withholdCredentials(text string) (string, int) {
	lines := strings.Split(text, "\n")
	withheld := 0
	inBlock := false
	for i, line := range lines {
		switch {
		case inBlock:
			inBlock = !pemBlockEnd.MatchString(line)
		case pemBlockStart.MatchString(line):
			// A block opened and closed on one line is over already.
			inBlock = !pemBlockEnd.MatchString(line)
		case credentialLine(line):
		default:
			continue
		}
		lines[i] = WithheldLine
		withheld++
	}
	if withheld == 0 {
		return text, 0
	}
	return strings.Join(lines, "\n"), withheld
}

// revisionOf digests what was read, so a review made on one set of evidence
// can tell when the project's files have changed since.
func revisionOf(e Evidence) string {
	sum := sha256.New()
	// A hash never fails to take a write.
	add := func(format string, args ...any) { _, _ = fmt.Fprintf(sum, format, args...) }
	for _, doc := range e.Documents {
		digest := sha256.Sum256([]byte(doc.Text))
		add("doc\x00%s\x00%s\x00%t\x00%s\n", doc.Path, doc.Class, doc.Truncated, hex.EncodeToString(digest[:]))
	}
	for _, tool := range e.Tools {
		add("tool\x00%s\x00%s\n", tool.Name, tool.Source)
	}
	add("git\x00%s\x00%s\x00%s\x00%s\n", e.Git.Layout, e.Git.Branch, e.Git.Remote, e.Git.RepositoryKey)
	add("partial\x00%t\x00%s\n", e.Partial, e.PartialReason)
	return hex.EncodeToString(sum.Sum(nil))
}

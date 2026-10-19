package externalmemory

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"unicode"

	"gopkg.in/yaml.v3"
)

// ClaudeCodeSource reads Claude Code's per-project memory (FR6): one fact per
// file under $CLAUDE_CONFIG_DIR/projects/<project-key>/memory/*.md, each with a
// YAML front matter naming it and typing it user, feedback, project, or
// reference. The MEMORY.md index in each folder is skipped; it only lists the
// other files.
type ClaudeCodeSource struct {
	root      string
	tempRoots []string
	exists    func(string) bool
}

// NewClaudeCodeSource reads Claude Code memory under root (a Claude config
// folder, the one holding projects/).
func NewClaudeCodeSource(root string) *ClaudeCodeSource {
	return &ClaudeCodeSource{root: root, tempRoots: defaultTempRoots(), exists: dirExists}
}

// WithTempRoots replaces the folders whose project keys are skipped as
// scratchpad and demo sessions. Tests use it because their fixtures live under
// the system temp folder.
func (s *ClaudeCodeSource) WithTempRoots(roots ...string) *ClaudeCodeSource {
	s.tempRoots = roots
	return s
}

// DefaultClaudeCodeRoot is $CLAUDE_CONFIG_DIR, else ~/.claude.
func DefaultClaudeCodeRoot() string {
	if root := strings.TrimSpace(os.Getenv("CLAUDE_CONFIG_DIR")); root != "" {
		return root
	}
	if home, err := os.UserHomeDir(); err == nil && home != "" {
		return filepath.Join(home, ".claude")
	}
	return ""
}

func (s *ClaudeCodeSource) ID() string { return SourceClaudeCode }

func (s *ClaudeCodeSource) projectsDir() string {
	if s.root == "" {
		return ""
	}
	return filepath.Join(s.root, "projects")
}

func (s *ClaudeCodeSource) Location() string { return s.projectsDir() }

func (s *ClaudeCodeSource) Available() bool { return pathExists(s.projectsDir()) }

func (s *ClaudeCodeSource) Read(ctx context.Context) ([]Entry, []ParseError, error) {
	dir := s.projectsDir()
	if !pathExists(dir) {
		return nil, nil, fmt.Errorf("claude code projects folder not found")
	}
	var entries []Entry
	var problems []ParseError
	folders := map[string]decodedFolder{}
	for _, path := range sortedGlob(filepath.Join(dir, "*", "memory", "*.md")) {
		if err := ctx.Err(); err != nil {
			return nil, nil, err
		}
		if filepath.Base(path) == "MEMORY.md" {
			continue
		}
		key := filepath.Base(filepath.Dir(filepath.Dir(path)))
		if isTempProjectKey(key, s.tempRoots) {
			continue
		}
		folder, ok := folders[key]
		if !ok {
			folder = s.decodeFolder(key)
			folders[key] = folder
		}
		entry, problem := readClaudeCodeMemory(path, folder)
		if problem != nil {
			problems = append(problems, *problem)
			continue
		}
		entries = append(entries, entry)
	}
	return entries, problems, nil
}

type decodedFolder struct {
	path       string
	unverified bool
}

func (s *ClaudeCodeSource) decodeFolder(key string) decodedFolder {
	if path, ok := decodeProjectKey(key, s.exists); ok {
		return decodedFolder{path: path}
	}
	return decodedFolder{path: key, unverified: true}
}

// claudeFrontMatter accepts both the current shape (type and modified under
// metadata) and the older flat one.
type claudeFrontMatter struct {
	Name        string `yaml:"name"`
	Description string `yaml:"description"`
	Type        string `yaml:"type"`
	Modified    string `yaml:"modified"`
	Metadata    struct {
		Type     string `yaml:"type"`
		Modified string `yaml:"modified"`
	} `yaml:"metadata"`
}

// readClaudeCodeMemory reads one memory file: front matter, then the markdown
// body. The description leads the body so search and excerpts see it.
func readClaudeCodeMemory(path string, folder decodedFolder) (Entry, *ParseError) {
	info, err := os.Stat(path)
	if err != nil || !info.Mode().IsRegular() {
		return Entry{}, &ParseError{Path: path, Reason: "unreadable"}
	}
	if info.Size() > maxSourceFileBytes {
		return Entry{}, &ParseError{Path: path, Reason: "file too large"}
	}
	// path is a glob match inside the Claude config folder's projects tree;
	// Ori only reads it.
	file, err := os.Open(path) // #nosec G304 -- read-only open of a documented memory file under the source root
	if err != nil {
		return Entry{}, &ParseError{Path: path, Reason: "unreadable"}
	}
	defer func() { _ = file.Close() }() // read-only: nothing to flush
	front, body, err := splitFrontMatter(io.LimitReader(file, maxSourceFileBytes))
	if err != nil {
		return Entry{}, &ParseError{Path: path, Reason: "could not read"}
	}
	var meta claudeFrontMatter
	if front != "" {
		if err := yaml.Unmarshal([]byte(front), &meta); err != nil {
			return Entry{}, &ParseError{Path: path, Reason: "invalid front matter"}
		}
	}

	title := strings.TrimSpace(meta.Name)
	if title == "" {
		title = strings.TrimSuffix(filepath.Base(path), filepath.Ext(path))
	}
	category := strings.ToLower(strings.TrimSpace(firstNonEmpty(meta.Metadata.Type, meta.Type)))
	if category == "" {
		category = "project"
	}
	date := normalizeDate(firstNonEmpty(meta.Metadata.Modified, meta.Modified))
	if date == "" {
		date = info.ModTime().UTC().Format("2006-01-02")
	}
	text := strings.TrimSpace(body)
	if description := strings.TrimSpace(meta.Description); description != "" {
		text = strings.TrimSpace(description + "\n\n" + text)
	}
	if text == "" && title == "" {
		return Entry{}, &ParseError{Path: path, Reason: "empty"}
	}
	entry := newEntry(SourceClaudeCode, path, "file", Entry{
		Folder:   folder.path,
		Category: Slug(category),
		Title:    titleFrom(title),
		Body:     text,
		Date:     date,
	})
	entry.FolderUnverified = folder.unverified
	return entry, nil
}

// splitFrontMatter returns the YAML between a leading "---" line and the next
// "---" line, and everything after it. A file without one is all body.
func splitFrontMatter(r io.Reader) (string, string, error) {
	scanner := bufio.NewScanner(r)
	scanner.Buffer(make([]byte, 0, 64*1024), maxMarkdownLine)
	var front, body []string
	state := 0 // 0 = first line, 1 = in front matter, 2 = body
	for scanner.Scan() {
		line := strings.TrimRight(scanner.Text(), "\r")
		switch state {
		case 0:
			if strings.TrimSpace(line) == "---" {
				state = 1
				continue
			}
			state = 2
			body = append(body, line)
		case 1:
			if strings.TrimSpace(line) == "---" {
				state = 2
				continue
			}
			front = append(front, line)
		default:
			body = append(body, line)
		}
	}
	if err := scanner.Err(); err != nil {
		return "", "", err
	}
	if state == 1 {
		// No closing marker: it was never front matter.
		return "", strings.Join(append([]string{"---"}, front...), "\n"), nil
	}
	return strings.Join(front, "\n"), strings.Join(body, "\n"), nil
}

func firstNonEmpty(values ...string) string {
	for _, v := range values {
		if strings.TrimSpace(v) != "" {
			return v
		}
	}
	return ""
}

func dirExists(path string) bool {
	info, err := os.Stat(path)
	return err == nil && info.IsDir()
}

// maxKeySegmentsPerName bounds how many dash-separated pieces one folder name
// may span while decoding a project key.
const maxKeySegmentsPerName = 8

// keyNameJoiners are the characters a folder name may have had where its
// project key shows a dash. Claude Code writes every character that is not a
// letter or digit as a dash.
var keyNameJoiners = []string{"-", "_", ".", " "}

// decodeProjectKey turns a project key back into the folder it was made from:
// "-Users-me-Projects-ori-agent" may be /Users/me/Projects/ori-agent or
// /Users/me/Projects/ori/agent. A dash is ambiguous, so each folder name is
// tried from the longest run of pieces down, and a candidate is kept only when
// that folder exists. A name that starts with a dot shows as an empty piece
// ("--claude" for "/.claude").
func decodeProjectKey(key string, exists func(string) bool) (string, bool) {
	if !strings.HasPrefix(key, "-") || exists == nil {
		return "", false
	}
	segments := strings.Split(key[1:], "-")
	var walk func(prefix string, rest []string) (string, bool)
	walk = func(prefix string, rest []string) (string, bool) {
		if len(rest) == 0 {
			return prefix, true
		}
		dot := ""
		if rest[0] == "" && len(rest) > 1 {
			dot, rest = ".", rest[1:]
		}
		for n := min(len(rest), maxKeySegmentsPerName); n >= 1; n-- {
			if slices.Contains(rest[:n], "") {
				continue
			}
			for _, joiner := range keyNameJoiners {
				if n == 1 && joiner != "-" {
					continue
				}
				candidate := prefix + "/" + dot + strings.Join(rest[:n], joiner)
				if !exists(candidate) {
					continue
				}
				if found, ok := walk(candidate, rest[n:]); ok {
					return found, true
				}
			}
		}
		return "", false
	}
	return walk("", segments)
}

// encodeProjectKey is Claude Code's folder-to-key rule: every character that
// is not a letter or digit becomes a dash.
func encodeProjectKey(folder string) string {
	return strings.Map(func(r rune) rune {
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			return r
		}
		return '-'
	}, folder)
}

// defaultTempRoots are the folders whose sessions are scratchpads and demos.
func defaultTempRoots() []string {
	roots := []string{"/private/tmp", "/private/var/folders", "/tmp", "/var/folders"}
	if tmp := strings.TrimSpace(os.TempDir()); tmp != "" {
		roots = append(roots, filepath.Clean(tmp))
	}
	return roots
}

func isTempProjectKey(key string, roots []string) bool {
	for _, root := range roots {
		encoded := encodeProjectKey(filepath.Clean(root))
		if encoded == "" || encoded == "-" {
			continue
		}
		if key == encoded || strings.HasPrefix(key, encoded+"-") {
			return true
		}
	}
	return false
}

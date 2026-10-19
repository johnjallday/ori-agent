package externalmemory

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
)

// GeminiSource reads the Gemini CLI's context files (FR7): the global
// ~/.gemini/GEMINI.md and the GEMINI.md of every project the CLI lists in
// ~/.gemini/projects.json. Each bullet or paragraph is an instruction entry.
// Ori's own Gemini provider talks to the API, so this only reflects the user's
// own Gemini CLI use.
type GeminiSource struct {
	root string
}

// NewGeminiSource reads Gemini CLI memory under root (normally ~/.gemini).
func NewGeminiSource(root string) *GeminiSource {
	return &GeminiSource{root: root}
}

// DefaultGeminiRoot is ~/.gemini.
func DefaultGeminiRoot() string {
	if home, err := os.UserHomeDir(); err == nil && home != "" {
		return filepath.Join(home, ".gemini")
	}
	return ""
}

func (s *GeminiSource) ID() string { return SourceGeminiCLI }

func (s *GeminiSource) Location() string { return s.root }

func (s *GeminiSource) globalFile() string { return filepath.Join(s.root, "GEMINI.md") }

func (s *GeminiSource) projectsFile() string { return filepath.Join(s.root, "projects.json") }

// Available reports whether the CLI has left either file behind.
func (s *GeminiSource) Available() bool {
	return s.root != "" && (pathExists(s.globalFile()) || pathExists(s.projectsFile()))
}

func (s *GeminiSource) Read(ctx context.Context) ([]Entry, []ParseError, error) {
	if !s.Available() {
		return nil, nil, fmt.Errorf("gemini cli folder not found")
	}
	var entries []Entry
	var problems []ParseError
	read := func(path, folder string) {
		sections, problem, ok := readMarkdownFile(path)
		if problem != nil {
			problems = append(problems, *problem)
		}
		if ok {
			entries = append(entries, geminiEntries(path, folder, sections)...)
		}
	}
	read(s.globalFile(), "")
	projects, problem := geminiProjects(s.projectsFile())
	if problem != nil {
		problems = append(problems, *problem)
	}
	for _, folder := range projects {
		if err := ctx.Err(); err != nil {
			return nil, nil, err
		}
		read(filepath.Join(folder, "GEMINI.md"), folder)
	}
	return entries, problems, nil
}

// geminiEntries splits one GEMINI.md into one entry per bullet or paragraph.
// Headings group the text but are not entries themselves.
func geminiEntries(path, folder string, sections []mdSection) []Entry {
	keys := sectionKeys{}
	var entries []Entry
	for _, section := range sections {
		for _, block := range splitBlocks(section.Lines) {
			title := titleFrom(block.Text)
			entries = append(entries, newEntry(SourceGeminiCLI, path, keys.next(keySlug(title)), Entry{
				Folder: folder, Category: "instruction", Title: title, Body: block.Text,
			}))
		}
	}
	return entries
}

// maxProjectsFileBytes bounds projects.json.
const maxProjectsFileBytes = 4 << 20

// geminiProjects lists the project folders in projects.json. The file's shape
// is the CLI's own and has changed between versions, so every absolute path
// in it is taken, whether it is an array item, an object key, or a value. A
// missing file is no problem.
func geminiProjects(path string) ([]string, *ParseError) {
	info, err := os.Stat(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, &ParseError{Path: path, Reason: "unreadable"}
	}
	if info.Size() > maxProjectsFileBytes {
		return nil, &ParseError{Path: path, Reason: "file too large"}
	}
	// path is the fixed projects.json under the Gemini root; Ori only reads it.
	data, err := os.ReadFile(path) // #nosec G304 -- read-only read of the CLI's documented project list
	if err != nil {
		return nil, &ParseError{Path: path, Reason: "unreadable"}
	}
	var doc any
	if err := json.Unmarshal(data, &doc); err != nil {
		return nil, &ParseError{Path: path, Reason: "invalid JSON"}
	}
	seen := map[string]bool{}
	var walk func(v any)
	add := func(s string) {
		if folder := cleanFolder(s); folder != "" && !seen[folder] {
			seen[folder] = true
		}
	}
	walk = func(v any) {
		switch value := v.(type) {
		case string:
			add(value)
		case []any:
			for _, item := range value {
				walk(item)
			}
		case map[string]any:
			for key, item := range value {
				add(key)
				walk(item)
			}
		}
	}
	walk(doc)
	folders := make([]string, 0, len(seen))
	for folder := range seen {
		folders = append(folders, folder)
	}
	sort.Strings(folders)
	return folders, nil
}

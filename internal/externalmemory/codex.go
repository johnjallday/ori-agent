package externalmemory

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
)

// CodexSource reads the memory Codex CLI builds under $CODEX_HOME/memories
// (FR5). Only the rendered markdown is read; Codex's SQLite working store, its
// derived skills, and every other file are skipped.
type CodexSource struct {
	root string
}

// NewCodexSource reads Codex memory under root (a Codex home, the folder
// holding memories/).
func NewCodexSource(root string) *CodexSource {
	return &CodexSource{root: root}
}

// DefaultCodexRoot is $CODEX_HOME, else ~/.codex.
func DefaultCodexRoot() string {
	if root := strings.TrimSpace(os.Getenv("CODEX_HOME")); root != "" {
		return root
	}
	if home, err := os.UserHomeDir(); err == nil && home != "" {
		return filepath.Join(home, ".codex")
	}
	return ""
}

func (s *CodexSource) ID() string { return SourceCodex }

func (s *CodexSource) memoriesDir() string {
	if s.root == "" {
		return ""
	}
	return filepath.Join(s.root, "memories")
}

func (s *CodexSource) Location() string { return s.memoriesDir() }

func (s *CodexSource) Available() bool { return pathExists(s.memoriesDir()) }

// codexFileParser turns one parsed file into entries.
type codexFileParser func(path string, sections []mdSection) []Entry

func (s *CodexSource) Read(ctx context.Context) ([]Entry, []ParseError, error) {
	dir := s.memoriesDir()
	if !pathExists(dir) {
		return nil, nil, fmt.Errorf("codex memory folder not found")
	}
	type job struct {
		path  string
		parse codexFileParser
	}
	jobs := []job{
		{filepath.Join(dir, "memory_summary.md"), parseCodexSummary},
		{filepath.Join(dir, "MEMORY.md"), parseCodexTaskGroups},
		{filepath.Join(dir, "raw_memories.md"), parseCodexThreads},
	}
	for _, path := range sortedGlob(filepath.Join(dir, "rollout_summaries", "*.md")) {
		jobs = append(jobs, job{path, parseCodexRollout})
	}
	for _, path := range sortedGlob(filepath.Join(dir, "extensions", "*", "notes", "*.md")) {
		jobs = append(jobs, job{path, parseCodexNote})
	}

	var entries []Entry
	var problems []ParseError
	for _, j := range jobs {
		if err := ctx.Err(); err != nil {
			return nil, nil, err
		}
		sections, problem, ok := readMarkdownFile(j.path)
		if problem != nil {
			problems = append(problems, *problem)
		}
		if !ok {
			continue
		}
		entries = append(entries, j.parse(j.path, sections)...)
	}
	return entries, problems, nil
}

// maxSourceFileBytes bounds one memory file. A larger file is skipped and
// reported rather than read.
const maxSourceFileBytes = 32 << 20

// readMarkdownFile streams one file into sections. A missing file is not a
// problem (every Codex file is optional); anything else that stops the read is
// reported with the path and a short reason.
func readMarkdownFile(path string) ([]mdSection, *ParseError, bool) {
	info, err := os.Stat(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil, false
		}
		return nil, &ParseError{Path: path, Reason: "unreadable"}, false
	}
	if !info.Mode().IsRegular() {
		return nil, &ParseError{Path: path, Reason: "not a regular file"}, false
	}
	if info.Size() > maxSourceFileBytes {
		return nil, &ParseError{Path: path, Reason: "file too large"}, false
	}
	// path is a fixed name or a glob match inside the source's own memory
	// folder; Ori only reads it.
	file, err := os.Open(path) // #nosec G304 -- read-only open of a documented memory file under the source root
	if err != nil {
		return nil, &ParseError{Path: path, Reason: "unreadable"}, false
	}
	defer func() { _ = file.Close() }() // read-only: nothing to flush
	sections, err := readSections(io.LimitReader(file, maxSourceFileBytes))
	if err != nil {
		return nil, &ParseError{Path: path, Reason: "could not parse markdown"}, false
	}
	return sections, nil, true
}

func sortedGlob(pattern string) []string {
	matches, err := filepath.Glob(pattern)
	if err != nil {
		return nil
	}
	sort.Strings(matches)
	return matches
}

var (
	isoDatePattern     = regexp.MustCompile(`^\d{4}-\d{2}-\d{2}$`)
	rolloutDatePattern = regexp.MustCompile(`rollout_summaries/(\d{4}-\d{2}-\d{2})T`)
	taskHeadingPattern = regexp.MustCompile(`^Task\s+(\d+)\s*:`)
	threadIDPattern    = regexp.MustCompile("`([^`]+)`")
	compactDatePattern = regexp.MustCompile(`^(\d{8}T\d{6}Z)`)
)

// parseCodexSummary reads memory_summary.md: one entry per bullet or paragraph
// under each "## " heading, the heading's slug as the category. Deeper headings
// that are absolute paths or dates set the entry's folder and date (the "What's
// in Memory" index groups its bullets that way).
func parseCodexSummary(path string, sections []mdSection) []Entry {
	keys := sectionKeys{}
	var entries []Entry
	for _, section := range sections {
		if section.Level < 2 || len(section.Path) < 2 {
			continue
		}
		category := Slug(section.Path[1])
		folder, date := "", ""
		for _, heading := range section.Path[2:] {
			switch {
			case filepath.IsAbs(heading):
				folder = filepath.Clean(heading)
			case isoDatePattern.MatchString(heading):
				date = heading
			}
		}
		for _, block := range splitBlocks(section.Lines) {
			title := titleFrom(block.Text)
			key := keys.next("summary/" + category + "/" + keySlug(title))
			entries = append(entries, newEntry(SourceCodex, path, key, Entry{
				Folder: folder, Category: category, Title: title, Body: block.Text, Date: date,
			}))
		}
	}
	return entries
}

// parseCodexTaskGroups reads MEMORY.md. Each "# Task Group:" section applies to
// the folder in its "applies_to: cwd=…" line. Each "## Task N:" inside it is
// one entry (category task) holding the section and its subsections. The
// group's other "## " sections (user preferences, reusable knowledge, failures)
// hold the actual lessons, so each of their bullets is an entry too, with that
// heading's slug as the category. Dates come from the rollout summary files a
// task cites; group lessons take the group's latest.
func parseCodexTaskGroups(path string, sections []mdSection) []Entry {
	keys := sectionKeys{}
	var entries []Entry
	for i := 0; i < len(sections); i++ {
		group := sections[i]
		if group.Level != 1 {
			continue
		}
		name := strings.TrimSpace(strings.TrimPrefix(group.Heading, "Task Group:"))
		groupKey := keySlug(name)
		folder := codexAppliesToFolder(group.Lines)

		end := i + 1
		for end < len(sections) && sections[end].Level > 1 {
			end++
		}
		groupDate := ""
		for j := i + 1; j < end; j++ {
			if sections[j].Level == 2 {
				if d := latestRolloutDate(sectionText(sections, j)); d > groupDate {
					groupDate = d
				}
			}
		}
		for j := i + 1; j < end; j++ {
			sub := sections[j]
			if sub.Level != 2 {
				continue
			}
			if m := taskHeadingPattern.FindStringSubmatch(sub.Heading); m != nil {
				body := sectionText(sections, j)
				entries = append(entries, newEntry(SourceCodex, path, keys.next(groupKey+"/task-"+m[1]), Entry{
					Folder: folder, Category: "task", Title: titleFrom(sub.Heading), Body: body, Date: latestRolloutDate(body),
				}))
				continue
			}
			category := Slug(sub.Heading)
			for _, block := range splitBlocks(sub.Lines) {
				title := titleFrom(block.Text)
				entries = append(entries, newEntry(SourceCodex, path, keys.next(groupKey+"/"+category+"/"+keySlug(title)), Entry{
					Folder: folder, Category: category, Title: title, Body: block.Text, Date: groupDate,
				}))
			}
		}
		i = end - 1
	}
	return entries
}

// codexAppliesToFolder reads "applies_to: cwd=/path; reuse_rule=…".
func codexAppliesToFolder(lines []string) string {
	value := headerFields(lines)["applies_to"]
	for part := range strings.SplitSeq(value, ";") {
		key, folder, ok := strings.Cut(strings.TrimSpace(part), "=")
		if ok && strings.TrimSpace(key) == "cwd" {
			return cleanFolder(folder)
		}
	}
	return ""
}

func latestRolloutDate(text string) string {
	latest := ""
	for _, m := range rolloutDatePattern.FindAllStringSubmatch(text, -1) {
		if m[1] > latest {
			latest = m[1]
		}
	}
	return latest
}

// parseCodexThreads reads raw_memories.md: one entry per "## Thread `id`"
// block, titled by its description and placed in its cwd.
func parseCodexThreads(path string, sections []mdSection) []Entry {
	keys := sectionKeys{}
	var entries []Entry
	for i, section := range sections {
		if section.Level != 2 || !strings.HasPrefix(section.Heading, "Thread") {
			continue
		}
		threadID := strings.TrimSpace(strings.TrimPrefix(section.Heading, "Thread"))
		if m := threadIDPattern.FindStringSubmatch(section.Heading); m != nil {
			threadID = m[1]
		}
		fields := headerFields(section.Lines)
		title := titleFrom(fields["description"])
		if title == "" {
			title = "Thread " + threadID
		}
		entries = append(entries, newEntry(SourceCodex, path, keys.next("thread/"+keySlug(threadID)), Entry{
			Folder:   cleanFolder(fields["cwd"]),
			Category: "thread",
			Title:    title,
			Body:     sectionText(sections, i),
			Date:     normalizeDate(fields["updated_at"]),
		}))
	}
	return entries
}

// parseCodexRollout reads one rollout_summaries/*.md file as one entry. The
// "key: value" header before the first heading gives its folder and date; the
// body starts at the first heading.
func parseCodexRollout(path string, sections []mdSection) []Entry {
	if len(sections) == 0 {
		return nil
	}
	fields := headerFields(sections[0].Lines)
	title, body := wholeFileTitleBody(path, sections)
	if body == "" {
		return nil
	}
	return []Entry{newEntry(SourceCodex, path, "file", Entry{
		Folder:   cleanFolder(fields["cwd"]),
		Category: "summary",
		Title:    title,
		Body:     body,
		Date:     normalizeDate(fields["updated_at"]),
	})}
}

// parseCodexNote reads one extensions/*/notes/*.md file as one entry, dated by
// its compact timestamp file name when it has one.
func parseCodexNote(path string, sections []mdSection) []Entry {
	title, body := wholeFileTitleBody(path, sections)
	if body == "" {
		return nil
	}
	date := ""
	if m := compactDatePattern.FindStringSubmatch(filepath.Base(path)); m != nil {
		date = normalizeDate(m[1])
	}
	if date == "" {
		if info, err := os.Stat(path); err == nil {
			date = info.ModTime().UTC().Format("2006-01-02")
		}
	}
	return []Entry{newEntry(SourceCodex, path, "file", Entry{
		Category: "note", Title: title, Body: body, Date: date,
	})}
}

// wholeFileTitleBody titles a file by its first heading (else its name) and
// returns everything from that heading on as the body.
func wholeFileTitleBody(path string, sections []mdSection) (string, string) {
	title := strings.TrimSuffix(filepath.Base(path), filepath.Ext(path))
	first := 0
	for i, section := range sections {
		if section.Level > 0 {
			title, first = section.Heading, i
			break
		}
	}
	var b strings.Builder
	if first == 0 && sections[0].Level == 0 {
		b.WriteString(strings.Join(sections[0].Lines, "\n"))
		first = 1
	}
	for _, section := range sections[first:] {
		b.WriteString("\n" + strings.Repeat("#", section.Level) + " " + section.Heading + "\n")
		b.WriteString(strings.Join(section.Lines, "\n"))
	}
	return titleFrom(title), strings.TrimSpace(b.String())
}

// cleanFolder accepts an absolute folder path and returns it cleaned; anything
// else becomes "".
func cleanFolder(raw string) string {
	folder := strings.Trim(strings.TrimSpace(raw), "`\"'")
	if folder == "" || !filepath.IsAbs(folder) {
		return ""
	}
	return filepath.Clean(folder)
}

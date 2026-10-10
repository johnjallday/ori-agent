package skills

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"unicode/utf8"
)

const (
	metadataFileBytes   = 64 << 10
	inventoryEntryLimit = 200
)

// SkillMetadata is the metadata supported by Ori's actual parser. There is no
// prompt, script body, raw YAML, local path, credential, or execution authority.
// Enabled/trusted state is intentionally absent: global availability isn't an
// agent/workspace grant, and inventory does not test operational readiness.
type SkillMetadata struct {
	SourceID           string   `json:"source_id"`
	Name               string   `json:"name"`
	Description        string   `json:"description,omitempty"`
	Source             string   `json:"source"`
	RequiredMCPServers []string `json:"required_mcp_servers,omitempty"`
	DeclaredTools      []string `json:"declared_tools,omitempty"`
	HasScripts         bool     `json:"has_scripts"`
	MetadataValid      bool     `json:"metadata_valid"`
	Truncated          bool     `json:"truncated"`
}

type SkillInventory struct {
	State     string          `json:"state"`
	Skills    []SkillMetadata `json:"skills"`
	Truncated bool            `json:"truncated"`
	Omitted   int             `json:"omitted"`
}

// MetadataInventory is shared with the UI's existing ListSkills result. It
// projects already parsed metadata, not a second package/frontmatter parser.
func MetadataInventory(entries []Skill, limit int) SkillInventory {
	if limit <= 0 || limit > inventoryEntryLimit {
		limit = inventoryEntryLimit
	}
	result := SkillInventory{State: "empty", Skills: []SkillMetadata{}}
	for _, skill := range entries {
		if skill.Source == SourceClaude || skill.Source == SourceCodex {
			continue // imported native-agent profiles are not installed skills
		}
		if len(result.Skills) == limit {
			result.Truncated = true
			break
		}
		digest := sha256.Sum256([]byte(skill.Source + "\x00" + skill.Path + "\x00" + skill.Name))
		meta := SkillMetadata{SourceID: hex.EncodeToString(digest[:16]), Source: skill.Source, HasScripts: skill.HasScripts, MetadataValid: len(skill.ValidationErrors) == 0}
		meta.Name = metadataText(skill.Name, 120, &meta.Truncated)
		meta.Description = metadataText(skill.Description, 800, &meta.Truncated)
		meta.RequiredMCPServers = metadataList(skill.RequiredMCPServers, &meta.Truncated)
		meta.DeclaredTools = metadataList(skill.AllowedTools, &meta.Truncated)
		result.Skills = append(result.Skills, meta)
		result.Truncated = result.Truncated || meta.Truncated
		if !meta.MetadataValid {
			result.State = "partial"
		}
	}
	sort.Slice(result.Skills, func(i, j int) bool { return result.Skills[i].Name < result.Skills[j].Name })
	if len(result.Skills) > 0 && result.State == "empty" {
		result.State = "available"
	}
	if result.Truncated {
		result.State = "partial"
	}
	return result
}

func metadataText(text string, limit int, truncated *bool) string {
	if !utf8.ValidString(text) {
		*truncated = true
		return ""
	}
	var out strings.Builder
	count := 0
	for _, ch := range text {
		if count == limit {
			*truncated = true
			break
		}
		out.WriteRune(ch)
		count++
	}
	return out.String()
}

func metadataList(entries []string, truncated *bool) []string {
	if len(entries) > 16 {
		entries = entries[:16]
		*truncated = true
	}
	out := make([]string, 0, len(entries))
	for _, entry := range entries {
		out = append(out, metadataText(entry, 120, truncated))
	}
	return out
}

// PersonalSkillInventory reads the installed Skills folder only. It does not
// load a hired agent's prompt/native skill loadout or infer bindings. Other
// locations are outside this inventory and must remain unknown, not absent.
func (m *Manager) PersonalSkillInventory(ctx context.Context, limit int) SkillInventory {
	if m == nil || m.PersonalSkillsDir() == "" {
		return SkillInventory{State: "unavailable", Skills: []SkillMetadata{}}
	}
	budget := &skillDirectoryBudget{ctx: ctx, remaining: inventoryEntryLimit}
	entries, err := m.readSkillsDirectory(m.PersonalSkillsDir(), SourcePersonal, false, false, true, budget)
	if err != nil || ctx.Err() != nil {
		return SkillInventory{State: "unavailable", Skills: []SkillMetadata{}}
	}
	result := MetadataInventory(entries, limit)
	result.Omitted = budget.omitted
	result.Truncated = result.Truncated || budget.truncated
	if result.Omitted > 0 || result.Truncated {
		result.State = "partial"
	}
	return result
}

type skillDirectoryBudget struct {
	ctx       context.Context
	remaining int
	truncated bool
	omitted   int
}

func (b *skillDirectoryBudget) readDir(path string) ([]os.DirEntry, error) {
	if b == nil {
		return os.ReadDir(path)
	}
	if b.ctx.Err() != nil {
		return nil, b.ctx.Err()
	}
	if b.remaining <= 0 {
		b.truncated = true
		return nil, nil
	}
	file, err := os.Open(path) // #nosec G304 -- caller composes a directory under the Manager-owned Skills root
	if err != nil {
		return nil, err
	}
	defer func() { _ = file.Close() }()
	entries, err := file.ReadDir(b.remaining + 1)
	if err != nil && !errors.Is(err, io.EOF) {
		return nil, err
	}
	if len(entries) > b.remaining {
		entries, b.truncated = entries[:b.remaining], true
	}
	b.remaining -= len(entries)
	sort.Slice(entries, func(i, j int) bool { return entries[i].Name() < entries[j].Name() })
	return entries, nil
}

func (m *Manager) readSkillsDirectory(root, source string, includePrompt, allowSingleFile, allowCategories bool, budget *skillDirectoryBudget) ([]Skill, error) {
	entries, err := budget.readDir(root)
	if err != nil {
		if os.IsNotExist(err) {
			return []Skill{}, nil
		}
		return nil, err
	}
	var result []Skill
	load := func(path, name, dir string) {
		if budget != nil && budget.ctx.Err() != nil {
			return
		}
		skill, err := m.loadSkillEntry(path, name, source, dir, includePrompt)
		if err == nil {
			result = append(result, skill)
		} else if budget != nil {
			budget.omitted++
		}
	}
	for _, entry := range entries {
		if entry.IsDir() {
			dir := filepath.Join(root, entry.Name())
			path := filepath.Join(dir, "SKILL.md")
			if _, err := os.Stat(path); err == nil {
				load(path, entry.Name(), dir)
				continue
			}
			if allowCategories {
				subs, err := budget.readDir(dir)
				if err != nil {
					if budget != nil {
						budget.omitted++
					}
					continue
				}
				for _, sub := range subs {
					if !sub.IsDir() {
						continue
					}
					subdir := filepath.Join(dir, sub.Name())
					path := filepath.Join(subdir, "SKILL.md")
					if _, err := os.Stat(path); err == nil {
						load(path, sub.Name(), subdir)
					}
				}
			}
		} else if allowSingleFile && strings.HasSuffix(entry.Name(), ".md") {
			load(filepath.Join(root, entry.Name()), strings.TrimSuffix(entry.Name(), filepath.Ext(entry.Name())), root)
		}
	}
	return result, nil
}

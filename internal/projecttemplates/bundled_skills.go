package projecttemplates

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/johnjallday/ori-agent/internal/skills"
)

const maxBundledSkills = 8

type BundledSkill struct {
	Name        string `json:"name"`
	Description string `json:"description"`
	Digest      string `json:"digest"`
	Text        string `json:"-"`
}

func loadBundledSkills(templatePath string, declared []string) ([]BundledSkill, error) {
	root := filepath.Join(templatePath, "skills")
	entries, err := os.ReadDir(root) // #nosec G304 -- root is a resolved template folder plus a fixed child
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read bundled skills: %w", err)
	}
	declaredSet := make(map[string]struct{}, len(declared))
	for _, name := range declared {
		declaredSet[name] = struct{}{}
	}
	bundled := make([]BundledSkill, 0, len(entries))
	found := 0
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		name := entry.Name()
		path := filepath.Join(root, name, "SKILL.md")
		if _, statErr := os.Stat(path); os.IsNotExist(statErr) {
			continue
		}
		found++
		if found > maxBundledSkills {
			return nil, fmt.Errorf("at most %d bundled skills are allowed", maxBundledSkills)
		}
		skill, readErr := skills.ReadValidatedSkillFile(path, name)
		if readErr != nil {
			return nil, fmt.Errorf("bundled skill %q: %w", name, readErr)
		}
		if _, ok := declaredSet[name]; !ok {
			continue
		}
		data, readErr := os.ReadFile(path) // #nosec G304 -- path is template/skills/<validated declared name>/SKILL.md
		if readErr != nil {
			return nil, fmt.Errorf("bundled skill %q: %w", name, readErr)
		}
		digest := sha256.Sum256(data)
		bundled = append(bundled, BundledSkill{Name: name, Description: skill.Description, Digest: hex.EncodeToString(digest[:]), Text: strings.TrimSpace(string(data))})
	}
	sort.Slice(bundled, func(i, j int) bool { return bundled[i].Name < bundled[j].Name })
	return bundled, nil
}

func copyTemplateEntryAllowed(relPath string, directory bool) bool {
	slash := filepath.ToSlash(relPath)
	parts := strings.Split(slash, "/")
	if parts[0] != "skills" {
		return true
	}
	if slash == "skills" {
		return directory
	}
	if len(parts) == 2 {
		return directory
	}
	return len(parts) == 3 && !directory && parts[2] == "SKILL.md"
}

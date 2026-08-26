package config

import (
	"bytes"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
)

// AgentDefaults is the pair of persistent launch fallbacks managed by the
// local agent-defaults command. Role-specific overrides are intentionally not
// part of this narrow editor.
type AgentDefaults struct {
	Primary      AgentSelection `json:"primary"`
	RoleFallback AgentSelection `json:"role_fallback"`
}

// ReadAgentDefaults validates the complete TOML file and returns only the two
// pairs managed by UpdateAgentDefaults. Environment overrides are deliberately
// excluded because this API reads checked-in persistent intent.
func ReadAgentDefaults(path string) (AgentDefaults, error) {
	if err := validateEditableConfigPath(path); err != nil {
		return AgentDefaults{}, err
	}
	cfg, err := Load(path, noConfigOverrides)
	if err != nil {
		return AgentDefaults{}, err
	}
	return AgentDefaults{
		Primary:      AgentSelection{Kind: cfg.Primary.Kind, Model: cfg.Primary.Model},
		RoleFallback: AgentSelection{Kind: cfg.Roles.DefaultKind, Model: cfg.Roles.DefaultModel},
	}, nil
}

// UpdateAgentDefaults validates and atomically changes exactly four existing
// TOML keys. It preserves unrelated bytes, comments, newline style, and mode.
func UpdateAgentDefaults(path string, proposed AgentDefaults) (AgentDefaults, error) {
	return updateAgentDefaults(path, proposed, defaultAgentDefaultsFileOps())
}

type agentDefaultsFileOps struct {
	createTemp func(string, string) (*os.File, error)
	writeAll   func(*os.File, []byte) error
	rename     func(string, string) error
}

func defaultAgentDefaultsFileOps() agentDefaultsFileOps {
	return agentDefaultsFileOps{
		createTemp: os.CreateTemp,
		writeAll: func(file *os.File, contents []byte) error {
			_, err := io.Copy(file, bytes.NewReader(contents))
			return err
		},
		rename: os.Rename,
	}
}

func updateAgentDefaults(path string, proposed AgentDefaults, fileOps agentDefaultsFileOps) (AgentDefaults, error) {
	if err := ValidateAgentSelection(proposed.Primary.Kind, proposed.Primary.Model); err != nil {
		return AgentDefaults{}, fmt.Errorf("primary defaults: %w", err)
	}
	if err := ValidateAgentSelection(proposed.RoleFallback.Kind, proposed.RoleFallback.Model); err != nil {
		return AgentDefaults{}, fmt.Errorf("role fallback defaults: %w", err)
	}
	info, err := validateEditableConfig(path)
	if err != nil {
		return AgentDefaults{}, err
	}
	// #nosec G304 -- path is the explicit repository config selected by the
	// caller and has been lstat-checked as a regular, non-symlink file.
	original, err := os.ReadFile(path)
	if err != nil {
		return AgentDefaults{}, fmt.Errorf("read devflow config for update: %w", err)
	}
	updated, err := replaceAgentDefaultKeys(original, proposed)
	if err != nil {
		return AgentDefaults{}, err
	}
	if bytes.Equal(original, updated) {
		return ReadAgentDefaults(path)
	}

	directory := filepath.Dir(path)
	temporary, err := fileOps.createTemp(directory, ".devflow-agent-defaults-*.tmp")
	if err != nil {
		return AgentDefaults{}, fmt.Errorf("create temporary devflow config: %w", err)
	}
	temporaryPath := temporary.Name()
	keepTemporary := false
	defer func() {
		_ = temporary.Close()
		if !keepTemporary {
			_ = os.Remove(temporaryPath)
		}
	}()
	if err := fileOps.writeAll(temporary, updated); err != nil {
		return AgentDefaults{}, fmt.Errorf("write temporary devflow config: %w", err)
	}
	if err := temporary.Sync(); err != nil {
		return AgentDefaults{}, fmt.Errorf("sync temporary devflow config: %w", err)
	}
	if err := temporary.Chmod(info.Mode().Perm()); err != nil {
		return AgentDefaults{}, fmt.Errorf("preserve devflow config mode: %w", err)
	}
	if err := temporary.Close(); err != nil {
		return AgentDefaults{}, fmt.Errorf("close temporary devflow config: %w", err)
	}
	if _, err := Load(temporaryPath, noConfigOverrides); err != nil {
		return AgentDefaults{}, fmt.Errorf("verify proposed devflow config: %w", err)
	}

	// Refuse a concurrent or symlink swap rather than replacing a path that no
	// longer represents the complete file validated above.
	if err := verifyUnchangedConfig(path, original); err != nil {
		return AgentDefaults{}, err
	}
	if err := fileOps.rename(temporaryPath, path); err != nil {
		return AgentDefaults{}, fmt.Errorf("replace devflow config: %w", err)
	}
	keepTemporary = true // rename consumed the temporary path
	defaults, err := ReadAgentDefaults(path)
	if err != nil {
		return AgentDefaults{}, fmt.Errorf("reload updated devflow config: %w", err)
	}
	if defaults != proposed {
		return AgentDefaults{}, fmt.Errorf("reload updated devflow config: persisted defaults do not match the proposal")
	}
	return defaults, nil
}

func validateEditableConfig(path string) (os.FileInfo, error) {
	if err := validateEditableConfigPath(path); err != nil {
		return nil, err
	}
	if _, err := Load(path, noConfigOverrides); err != nil {
		return nil, err
	}
	return os.Lstat(path)
}

func validateEditableConfigPath(path string) error {
	info, err := os.Lstat(path)
	if err != nil {
		return fmt.Errorf("inspect devflow config: %w", err)
	}
	if info.Mode()&os.ModeSymlink != 0 {
		return fmt.Errorf("devflow config must not be a symlink")
	}
	if !info.Mode().IsRegular() {
		return fmt.Errorf("devflow config must be a regular file")
	}
	return nil
}

func verifyUnchangedConfig(path string, original []byte) error {
	if err := validateEditableConfigPath(path); err != nil {
		return err
	}
	// #nosec G304 -- this is the same explicit, non-symlink config path already
	// validated above; the byte comparison is the concurrent-change guard.
	current, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("re-read devflow config before replace: %w", err)
	}
	if !bytes.Equal(current, original) {
		return fmt.Errorf("devflow config changed during update; no replacement was made")
	}
	return nil
}

var tomlSectionPattern = regexp.MustCompile(`^[ \t]*\[([A-Za-z0-9_.-]+)\][ \t]*(?:#.*)?$`)

func replaceAgentDefaultKeys(contents []byte, proposed AgentDefaults) ([]byte, error) {
	replacements := map[string]map[string]string{
		"primary": {
			"kind":  proposed.Primary.Kind,
			"model": proposed.Primary.Model,
		},
		"roles": {
			"default_kind":  proposed.RoleFallback.Kind,
			"default_model": proposed.RoleFallback.Model,
		},
	}
	counts := map[string]int{}
	lines := strings.SplitAfter(string(contents), "\n")
	section := ""
	for index, line := range lines {
		body, ending := splitLineEnding(line)
		if matches := tomlSectionPattern.FindStringSubmatch(body); matches != nil {
			section = matches[1]
			continue
		}
		sectionReplacements, ok := replacements[section]
		if !ok {
			continue
		}
		for key, value := range sectionReplacements {
			replaced, found, err := replaceTOMLStringAssignment(body, key, value)
			if err != nil {
				return nil, fmt.Errorf("update %s.%s: %w", section, key, err)
			}
			if found {
				counts[section+"."+key]++
				lines[index] = replaced + ending
			}
		}
	}
	for section, keys := range replacements {
		for key := range keys {
			target := section + "." + key
			switch counts[target] {
			case 1:
			case 0:
				return nil, fmt.Errorf("devflow config is missing required target %s", target)
			default:
				return nil, fmt.Errorf("devflow config contains duplicate target %s", target)
			}
		}
	}
	return []byte(strings.Join(lines, "")), nil
}

func splitLineEnding(line string) (string, string) {
	if strings.HasSuffix(line, "\r\n") {
		return strings.TrimSuffix(line, "\r\n"), "\r\n"
	}
	if strings.HasSuffix(line, "\n") {
		return strings.TrimSuffix(line, "\n"), "\n"
	}
	return line, ""
}

func replaceTOMLStringAssignment(line, key, value string) (string, bool, error) {
	pattern := regexp.MustCompile(`^([ \t]*` + regexp.QuoteMeta(key) + `[ \t]*=[ \t]*)(?:"(?:\\.|[^"\\])*"|'[^']*')([ \t]*(?:#.*)?)$`)
	matches := pattern.FindStringSubmatchIndex(line)
	if matches == nil {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, key) {
			separator := strings.Index(trimmed, "=")
			if separator >= 0 && strings.TrimSpace(trimmed[:separator]) == key {
				return "", false, fmt.Errorf("target must use a single-line TOML string")
			}
		}
		return line, false, nil
	}
	prefix := line[matches[2]:matches[3]]
	suffix := line[matches[4]:matches[5]]
	return prefix + strconv.Quote(value) + suffix, true, nil
}

func noConfigOverrides(string) (string, bool) { return "", false }

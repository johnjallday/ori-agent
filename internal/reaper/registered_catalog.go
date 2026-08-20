package reaper

import (
	"bufio"
	"bytes"
	"encoding/csv"
	"errors"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

const (
	maxKeyboardConfigBytes = 4 << 20
	maxKeyboardConfigLines = 32768
	maxKeyboardLineBytes   = 64 << 10
)

// Catalog combines Ori's curated actions with REAPER's read-only registered
// ReaScript index. The keyboard config path is constructed from the current
// user's fixed REAPER support directory; workspace and browser data cannot
// influence it.
type Catalog struct {
	keyboardConfigPath string
}

func NewCatalog() *Catalog {
	home, err := os.UserHomeDir()
	if err != nil || strings.TrimSpace(home) == "" {
		return &Catalog{}
	}
	return &Catalog{keyboardConfigPath: filepath.Join(home, "Library", "Application Support", "REAPER", "reaper-kb.ini")}
}

// NewCatalogWithKeyboardConfig supports deterministic tests and portable
// callers that have already resolved a trusted REAPER configuration path.
func NewCatalogWithKeyboardConfig(path string) *Catalog {
	return &Catalog{keyboardConfigPath: filepath.Clean(strings.TrimSpace(path))}
}

func (c *Catalog) List() ([]Action, error) {
	actions := BuiltinActions()
	registered, err := c.registeredActions()
	if err != nil {
		return nil, err
	}
	return append(actions, registered...), nil
}

func (c *Catalog) Find(id string) (Action, bool, error) {
	actions, err := c.List()
	if err != nil {
		return Action{}, false, err
	}
	id = strings.TrimSpace(id)
	for _, action := range actions {
		if strings.EqualFold(action.ID, id) {
			return action, true, nil
		}
	}
	return Action{}, false, nil
}

func (c *Catalog) registeredActions() ([]Action, error) {
	if c == nil || strings.TrimSpace(c.keyboardConfigPath) == "" || c.keyboardConfigPath == "." {
		return nil, nil
	}
	info, err := os.Lstat(c.keyboardConfigPath) // #nosec G304 -- path is fixed REAPER config or injected trusted test fixture
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil || info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() || info.Size() > maxKeyboardConfigBytes {
		return nil, errors.New("REAPER action catalog is unavailable")
	}
	file, err := os.Open(c.keyboardConfigPath) // #nosec G304 -- bounded regular file checked immediately above
	if err != nil {
		return nil, errors.New("REAPER action catalog is unavailable")
	}
	defer func() { _ = file.Close() }()
	return ParseRegisteredActions(file)
}

// ParseRegisteredActions parses SCR records without interpreting or writing
// any other reaper-kb.ini content. Malformed individual SCR rows are skipped;
// an oversized file or scanner failure rejects the read as a whole.
func ParseRegisteredActions(reader io.Reader) ([]Action, error) {
	if reader == nil {
		return nil, nil
	}
	data, err := io.ReadAll(io.LimitReader(reader, maxKeyboardConfigBytes+1))
	if err != nil || len(data) > maxKeyboardConfigBytes {
		return nil, errors.New("REAPER action catalog is too large")
	}
	scanner := bufio.NewScanner(bytes.NewReader(data))
	scanner.Buffer(make([]byte, 4096), maxKeyboardLineBytes)
	actions := make([]Action, 0)
	seen := make(map[string]struct{})
	lines := 0
	for scanner.Scan() {
		lines++
		if lines > maxKeyboardConfigLines {
			return nil, errors.New("REAPER action catalog is too large")
		}
		line := strings.TrimSpace(scanner.Text())
		if !strings.HasPrefix(line, "SCR ") {
			continue
		}
		fields, err := parseKeyboardFields(line)
		if err != nil || len(fields) < 5 || fields[0] != "SCR" {
			continue
		}
		id := "_" + strings.TrimSpace(fields[3])
		if !validExecutableCommandID(id) {
			continue
		}
		key := strings.ToLower(id)
		if _, exists := seen[key]; exists {
			continue
		}
		label := strings.TrimSpace(fields[4])
		if strings.HasPrefix(strings.ToLower(label), "custom:") {
			label = strings.TrimSpace(label[len("custom:"):])
		}
		if label == "" {
			label = id
		}
		seen[key] = struct{}{}
		actions = append(actions, Action{
			ID: id, Label: label, Description: "Registered ReaScript: " + label,
			Source: ActionSourceRegistered, Mutates: true, NeedsConfirmation: true,
		})
	}
	if err := scanner.Err(); err != nil {
		return nil, errors.New("REAPER action catalog could not be read")
	}
	sort.SliceStable(actions, func(i, j int) bool {
		if strings.EqualFold(actions[i].Label, actions[j].Label) {
			return actions[i].ID < actions[j].ID
		}
		return strings.ToLower(actions[i].Label) < strings.ToLower(actions[j].Label)
	})
	return actions, nil
}

func parseKeyboardFields(line string) ([]string, error) {
	parser := csv.NewReader(strings.NewReader(line))
	parser.Comma = ' '
	parser.FieldsPerRecord = -1
	parser.TrimLeadingSpace = true
	parser.LazyQuotes = true
	return parser.Read()
}

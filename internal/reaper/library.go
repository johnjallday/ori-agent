package reaper

import (
	"bufio"
	"bytes"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
)

const (
	maxLibraryEntries = 512
	maxScriptBytes    = 1 << 20
	maxFrontmatter    = 8 << 10
)

var (
	ErrLibraryUnavailable = errors.New("REAPER script library is unavailable")
	ErrLibraryUnsafe      = errors.New("REAPER script library path is unsafe")
	ErrScriptInvalid      = errors.New("REAPER script is invalid")
	ErrScriptExists       = errors.New("REAPER script already exists")
	ErrScriptNotFound     = errors.New("REAPER script was not found")
	scriptFilenamePattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,119}\.lua$`)
)

// Script is one globally shared Lua file. Code is present on individual reads
// and write responses, but omitted from list/catalog responses.
type Script struct {
	ID                string `json:"id"`
	Filename          string `json:"filename"`
	Name              string `json:"name"`
	Description       string `json:"description"`
	NeedsConfirmation bool   `json:"needs_confirmation"`
	MetadataValid     bool   `json:"metadata_valid"`
	Code              string `json:"code,omitempty"`
}

type ScriptInput struct {
	Filename          string `json:"filename"`
	Name              string `json:"name"`
	Description       string `json:"description"`
	NeedsConfirmation bool   `json:"needs_confirmation"`
	Code              string `json:"code"`
}

type Frontmatter struct {
	Name              string
	Description       string
	NeedsConfirmation bool
	Valid             bool
	Body              string
}

// Library stores user-visible scripts outside the runner exchange root. The
// production location is always ~/Ori Scripts/reaper/.
type Library struct {
	root    string
	homeDir func() (string, error)
}

func NewLibrary() *Library {
	return &Library{homeDir: os.UserHomeDir}
}

func NewLibraryAt(root string) *Library {
	return &Library{root: strings.TrimSpace(root), homeDir: os.UserHomeDir}
}

func (l *Library) Root() (string, error) {
	return l.resolveRoot()
}

func (l *Library) resolveRoot() (string, error) {
	if l == nil {
		return "", ErrLibraryUnavailable
	}
	root := strings.TrimSpace(l.root)
	if root == "" {
		if l.homeDir == nil {
			return "", ErrLibraryUnavailable
		}
		home, err := l.homeDir()
		if err != nil || strings.TrimSpace(home) == "" {
			return "", ErrLibraryUnavailable
		}
		root = filepath.Join(home, "Ori Scripts", "reaper")
	}
	absolute, err := filepath.Abs(filepath.Clean(root))
	if err != nil || libraryPathUsesRunnerRoot(absolute) {
		return "", ErrLibraryUnsafe
	}
	if err := os.MkdirAll(absolute, 0o750); err != nil {
		return "", ErrLibraryUnavailable
	}
	info, err := os.Lstat(absolute) // #nosec G304 -- fixed user library or injected trusted test root
	if err != nil || info.Mode()&os.ModeSymlink != 0 || !info.IsDir() {
		return "", ErrLibraryUnsafe
	}
	canonical, err := filepath.EvalSymlinks(absolute)
	if err != nil || libraryPathUsesRunnerRoot(canonical) {
		return "", ErrLibraryUnsafe
	}
	return filepath.Clean(canonical), nil
}

func libraryPathUsesRunnerRoot(path string) bool {
	for _, part := range strings.Split(filepath.Clean(path), string(filepath.Separator)) {
		if part == ".ori-reaper" {
			return true
		}
	}
	return false
}

func (l *Library) List() ([]Script, error) {
	root, err := l.resolveRoot()
	if err != nil {
		return nil, err
	}
	entries, err := os.ReadDir(root)
	if err != nil || len(entries) > maxLibraryEntries {
		return nil, ErrLibraryUnavailable
	}
	scripts := make([]Script, 0)
	for _, entry := range entries {
		name := entry.Name()
		if entry.IsDir() || !scriptFilenamePattern.MatchString(name) {
			continue
		}
		script, readErr := l.readFrom(root, name)
		if readErr != nil {
			if errors.Is(readErr, ErrScriptInvalid) {
				continue
			}
			return nil, readErr
		}
		script.Code = ""
		scripts = append(scripts, script)
	}
	sort.SliceStable(scripts, func(i, j int) bool {
		return strings.ToLower(scripts[i].Name) < strings.ToLower(scripts[j].Name)
	})
	return scripts, nil
}

func (l *Library) Read(identifier string) (Script, error) {
	root, err := l.resolveRoot()
	if err != nil {
		return Script{}, err
	}
	filename, err := scriptFilename(identifier)
	if err != nil {
		return Script{}, err
	}
	return l.readFrom(root, filename)
}

func (l *Library) readFrom(root, filename string) (Script, error) {
	path := filepath.Join(root, filename)
	if filepath.Dir(path) != root {
		return Script{}, ErrScriptInvalid
	}
	info, err := os.Lstat(path) // #nosec G304 -- filename passed strict local grammar and root is canonical
	if errors.Is(err, os.ErrNotExist) {
		return Script{}, ErrScriptNotFound
	}
	if err != nil || info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() || info.Size() > maxScriptBytes {
		return Script{}, ErrScriptInvalid
	}
	data, err := os.ReadFile(path) // #nosec G304 -- bounded regular script under canonical library root
	if err != nil {
		return Script{}, ErrLibraryUnavailable
	}
	metadata := ParseFrontmatter(data)
	name := metadata.Name
	if !metadata.Valid || strings.TrimSpace(name) == "" {
		name = filename
	}
	description := metadata.Description
	needsConfirmation := metadata.NeedsConfirmation
	code := metadata.Body
	if !metadata.Valid {
		description = "Script metadata is missing or malformed. Review the Lua before running it."
		needsConfirmation = true
		code = string(data)
	}
	return Script{
		ID: "custom:" + filename, Filename: filename, Name: name, Description: description,
		NeedsConfirmation: needsConfirmation, MetadataValid: metadata.Valid, Code: code,
	}, nil
}

func (l *Library) Create(input ScriptInput) (Script, error) {
	return l.write(input, true)
}

func (l *Library) Update(identifier string, input ScriptInput) (Script, error) {
	filename, err := scriptFilename(identifier)
	if err != nil {
		return Script{}, err
	}
	input.Filename = filename
	return l.write(input, false)
}

func (l *Library) write(input ScriptInput, create bool) (Script, error) {
	root, err := l.resolveRoot()
	if err != nil {
		return Script{}, err
	}
	filename, err := scriptFilename(input.Filename)
	if err != nil || ValidateScriptInput(input) != nil {
		return Script{}, ErrScriptInvalid
	}
	path := filepath.Join(root, filename)
	if filepath.Dir(path) != root {
		return Script{}, ErrScriptInvalid
	}
	info, statErr := os.Lstat(path) // #nosec G304 -- strict filename under canonical root
	if create {
		if statErr == nil {
			return Script{}, ErrScriptExists
		}
		if !errors.Is(statErr, os.ErrNotExist) {
			return Script{}, ErrLibraryUnavailable
		}
	} else if errors.Is(statErr, os.ErrNotExist) {
		return Script{}, ErrScriptNotFound
	} else if statErr != nil || info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() {
		return Script{}, ErrScriptInvalid
	}
	data := renderScript(input)
	if err := atomicLibraryWrite(root, path, data, create); err != nil {
		return Script{}, err
	}
	return l.readFrom(root, filename)
}

func (l *Library) Delete(identifier string) error {
	root, err := l.resolveRoot()
	if err != nil {
		return err
	}
	filename, err := scriptFilename(identifier)
	if err != nil {
		return err
	}
	path := filepath.Join(root, filename)
	info, err := os.Lstat(path) // #nosec G304 -- strict filename under canonical root
	if errors.Is(err, os.ErrNotExist) {
		return ErrScriptNotFound
	}
	if err != nil || info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() {
		return ErrScriptInvalid
	}
	if err := os.Remove(path); err != nil { // #nosec G304 -- exact checked library file; delete is intentionally permanent
		return ErrLibraryUnavailable
	}
	return nil
}

func ParseFrontmatter(data []byte) Frontmatter {
	text := string(data)
	if !strings.HasPrefix(text, "--[[ori\n") && !strings.HasPrefix(text, "--[[ori\r\n") {
		return Frontmatter{NeedsConfirmation: true, Body: text}
	}
	end := strings.Index(text, "]]--")
	if end < 0 || end > maxFrontmatter {
		return Frontmatter{NeedsConfirmation: true, Body: text}
	}
	header := strings.TrimPrefix(strings.TrimPrefix(text[:end], "--[[ori\r\n"), "--[[ori\n")
	metadata := Frontmatter{NeedsConfirmation: true, Body: strings.TrimPrefix(text[end+4:], "\r\n")}
	metadata.Body = strings.TrimPrefix(metadata.Body, "\n")
	seenName := false
	scanner := bufio.NewScanner(strings.NewReader(header))
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}
		key, value, ok := strings.Cut(line, ":")
		if !ok {
			return Frontmatter{NeedsConfirmation: true, Body: text}
		}
		value = strings.TrimSpace(value)
		switch strings.ToLower(strings.TrimSpace(key)) {
		case "name":
			if value == "" {
				return Frontmatter{NeedsConfirmation: true, Body: text}
			}
			metadata.Name = value
			seenName = true
		case "description":
			metadata.Description = value
		case "confirm":
			switch strings.ToLower(value) {
			case "true":
				metadata.NeedsConfirmation = true
			case "false":
				metadata.NeedsConfirmation = false
			default:
				return Frontmatter{NeedsConfirmation: true, Body: text}
			}
		default:
			// Preserve forward compatibility for future zero-argument metadata.
		}
	}
	if scanner.Err() != nil || !seenName {
		return Frontmatter{NeedsConfirmation: true, Body: text}
	}
	metadata.Valid = true
	return metadata
}

func ValidateScriptInput(input ScriptInput) error {
	if _, err := scriptFilename(input.Filename); err != nil || !validMetadataLine(input.Name) || !validMetadataLine(input.Description) || strings.TrimSpace(input.Name) == "" || len(input.Code) == 0 || len(input.Code) > maxScriptBytes-maxFrontmatter {
		return ErrScriptInvalid
	}
	return nil
}

func scriptFilename(identifier string) (string, error) {
	filename := strings.TrimSpace(strings.TrimPrefix(identifier, "custom:"))
	if !scriptFilenamePattern.MatchString(filename) || filepath.Base(filename) != filename {
		return "", ErrScriptInvalid
	}
	return filename, nil
}

func validMetadataLine(value string) bool {
	return len(value) <= 500 && !strings.ContainsAny(value, "\r\n")
}

func renderScript(input ScriptInput) []byte {
	confirm := "false"
	if input.NeedsConfirmation {
		confirm = "true"
	}
	return []byte(fmt.Sprintf("--[[ori\nname: %s\ndescription: %s\nconfirm: %s\n]]--\n%s", strings.TrimSpace(input.Name), strings.TrimSpace(input.Description), confirm, input.Code))
}

func atomicLibraryWrite(root, destination string, data []byte, create bool) error {
	if filepath.Dir(destination) != root {
		return ErrLibraryUnsafe
	}
	if create {
		file, err := os.OpenFile(destination, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600) // #nosec G304 -- strict destination under canonical library root
		if err != nil {
			if errors.Is(err, os.ErrExist) {
				return ErrScriptExists
			}
			return ErrLibraryUnavailable
		}
		if _, err := io.Copy(file, bytes.NewReader(data)); err != nil {
			_ = file.Close()
			_ = os.Remove(destination)
			return ErrLibraryUnavailable
		}
		if err := file.Sync(); err != nil {
			_ = file.Close()
			_ = os.Remove(destination)
			return ErrLibraryUnavailable
		}
		return file.Close()
	}
	temp, err := os.CreateTemp(root, ".ori-script-*")
	if err != nil {
		return ErrLibraryUnavailable
	}
	tempPath := temp.Name()
	defer func() { _ = os.Remove(tempPath) }()
	if err := temp.Chmod(0o600); err != nil {
		_ = temp.Close()
		return ErrLibraryUnavailable
	}
	if _, err := temp.Write(data); err != nil {
		_ = temp.Close()
		return ErrLibraryUnavailable
	}
	if err := temp.Sync(); err != nil {
		_ = temp.Close()
		return ErrLibraryUnavailable
	}
	if err := temp.Close(); err != nil {
		return ErrLibraryUnavailable
	}
	if err := os.Rename(tempPath, destination); err != nil {
		return ErrLibraryUnavailable
	}
	return nil
}

package blueprintintake

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/johnjallday/ori-agent/internal/fileparser"
	"github.com/johnjallday/ori-agent/internal/workspace"
)

const (
	StateDirName       = "blueprint-intake"
	UploadedFilesDir   = "Blueprint Intake"
	MaxFilesPerIntake  = 50
	sourceStateVersion = 1
)

const (
	SourceStatusParsed     = "parsed"
	SourceStatusSkipped    = "skipped"
	SourceStatusTooLarge   = "too_large"
	SourceStatusUnreadable = "unreadable"
)

var (
	ErrIntakeNotFound = errors.New("intake requirement not found")
	ErrFilesDisabled  = errors.New("file sources are not enabled for this intake")
	ErrFileLimit      = errors.New("intake file limit reached")
)

type WorkspaceReader interface {
	GetFolderWorkspace(id string) (*workspace.Workspace, error)
}

// SourceRecord is one durable source captured for an intake. ParsedTextFile is
// relative to the package state directory; FilePath is relative to the
// workspace root. Both are host-generated and never accepted from a request.
type SourceRecord struct {
	ID             string    `json:"id"`
	IntakeKey      string    `json:"intake_key"`
	Kind           string    `json:"kind"`
	Name           string    `json:"name"`
	FilePath       string    `json:"file_path,omitempty"`
	ParsedTextFile string    `json:"parsed_text_file,omitempty"`
	ContentHash    string    `json:"content_hash,omitempty"`
	Status         string    `json:"status"`
	Size           int64     `json:"size"`
	AddedAt        time.Time `json:"added_at"`
}

type FileResult struct {
	ID          string `json:"id,omitempty"`
	Name        string `json:"name"`
	Status      string `json:"status"`
	Size        int64  `json:"size,omitempty"`
	ContentHash string `json:"content_hash,omitempty"`
	Message     string `json:"message,omitempty"`
}

type sourceState struct {
	Version               int                      `json:"version"`
	WorkspaceID           string                   `json:"workspace_id"`
	Sources               []SourceRecord           `json:"sources,omitempty"`
	Consents              map[string]ConsentRecord `json:"consents,omitempty"`
	BundledSkillDecisions map[string]string        `json:"bundled_skill_decisions,omitempty"`
}

// SourceService owns workspace-scoped intake source storage.
type SourceService struct {
	workspaces       WorkspaceReader
	folders          workspace.FolderResolver
	providerResolver ProviderResolver
	now              func() time.Time

	mu    sync.Mutex
	locks map[string]*sync.Mutex
}

func NewSourceService(workspaces WorkspaceReader, folders workspace.FolderResolver) *SourceService {
	return &SourceService{workspaces: workspaces, folders: folders, now: time.Now, locks: make(map[string]*sync.Mutex)}
}

func (s *SourceService) SetProviderResolver(resolver ProviderResolver) {
	if s != nil {
		s.providerResolver = resolver
	}
}

func (s *SourceService) lockFor(workspaceID, intakeKey string) *sync.Mutex {
	s.mu.Lock()
	defer s.mu.Unlock()
	key := workspaceID + "\x00" + intakeKey
	lock := s.locks[key]
	if lock == nil {
		lock = &sync.Mutex{}
		s.locks[key] = lock
	}
	return lock
}

func (s *SourceService) Requirement(workspaceID, intakeKey string) (workspace.IntakeRequirement, error) {
	if s == nil || s.workspaces == nil {
		return workspace.IntakeRequirement{}, errors.New("blueprint intake workspace store is unavailable")
	}
	ws, err := s.workspaces.GetFolderWorkspace(strings.TrimSpace(workspaceID))
	if err != nil || ws == nil {
		return workspace.IntakeRequirement{}, fmt.Errorf("workspace not found")
	}
	requirement, ok := ws.TemplateIntakeRequirement(intakeKey)
	if !ok {
		return workspace.IntakeRequirement{}, ErrIntakeNotFound
	}
	return requirement, nil
}

// AddFile copies and parses one uploaded file. Expected per-file failures are
// returned as statuses and persisted independently, so one bad file never rolls
// back another file from the same request.
func (s *SourceService) AddFile(ctx context.Context, workspaceID, intakeKey, filename string, reader io.Reader) (FileResult, error) {
	if err := ctx.Err(); err != nil {
		return FileResult{}, err
	}
	requirement, err := s.Requirement(workspaceID, intakeKey)
	if err != nil {
		return FileResult{}, err
	}
	if !requirement.Sources.Files {
		return FileResult{}, ErrFilesDisabled
	}
	intakeKey = requirement.Key
	lock := s.lockFor(workspaceID, intakeKey)
	lock.Lock()
	defer lock.Unlock()

	state, stateDir, root, err := s.loadState(workspaceID)
	if err != nil {
		return FileResult{}, err
	}
	if countFiles(state.Sources, intakeKey) >= MaxFilesPerIntake {
		return FileResult{Name: displayFilename(filename), Status: SourceStatusSkipped, Message: "This intake already has 50 files."}, ErrFileLimit
	}

	name, nameErr := safeFilename(filename)
	if nameErr != nil {
		return s.persistStatusOnly(state, stateDir, intakeKey, displayFilename(filename), SourceStatusUnreadable, 0, "The filename could not be used.")
	}
	data, readErr := io.ReadAll(io.LimitReader(reader, fileparser.MaxFileSize+1))
	size := int64(len(data))
	if readErr != nil {
		return s.persistStatusOnly(state, stateDir, intakeKey, name, SourceStatusUnreadable, size, "The file could not be read.")
	}
	if err := fileparser.ValidateFileSize(size); err != nil {
		return s.persistStatusOnly(state, stateDir, intakeKey, name, SourceStatusTooLarge, size, "The file is larger than 10 MB.")
	}

	id, err := newSourceID()
	if err != nil {
		return FileResult{}, err
	}
	digest := sha256.Sum256(data)
	contentHash := hex.EncodeToString(digest[:])
	destinationDir := filepath.Join(root, workspace.FilesDir, UploadedFilesDir, intakeFolderName(intakeKey))
	if err := os.MkdirAll(destinationDir, 0o750); err != nil {
		return FileResult{}, fmt.Errorf("create intake files directory: %w", err)
	}
	destination := filepath.Join(destinationDir, id+"-"+name)
	if err := os.WriteFile(destination, data, 0o600); err != nil {
		return FileResult{}, fmt.Errorf("copy intake file: %w", err)
	}

	record := SourceRecord{
		ID:          id,
		IntakeKey:   intakeKey,
		Kind:        "file",
		Name:        name,
		FilePath:    relativeSlash(root, destination),
		ContentHash: contentHash,
		Status:      SourceStatusSkipped,
		Size:        size,
		AddedAt:     s.now().UTC(),
	}
	message := "This file type is not supported by this intake."
	if extensionAllowed(name, requirement.AcceptedExtensions) {
		parsed, parseErr := fileparser.ParseFile(name, data)
		if parseErr != nil {
			record.Status = SourceStatusUnreadable
			message = "Ori could not read this file."
		} else {
			record.Status = SourceStatusParsed
			message = ""
			textName := id + ".txt"
			if err := os.MkdirAll(filepath.Join(stateDir, "text"), 0o750); err != nil {
				return FileResult{}, fmt.Errorf("create parsed-text directory: %w", err)
			}
			if err := os.WriteFile(filepath.Join(stateDir, "text", textName), []byte(parsed), 0o600); err != nil {
				return FileResult{}, fmt.Errorf("store parsed intake text: %w", err)
			}
			record.ParsedTextFile = filepath.ToSlash(filepath.Join("text", textName))
		}
	}

	state.Sources = append(state.Sources, record)
	if err := saveSourceState(stateDir, state); err != nil {
		return FileResult{}, err
	}
	return fileResult(record, message), nil
}

func (s *SourceService) persistStatusOnly(state sourceState, stateDir, intakeKey, name, status string, size int64, message string) (FileResult, error) {
	id, err := newSourceID()
	if err != nil {
		return FileResult{}, err
	}
	record := SourceRecord{ID: id, IntakeKey: intakeKey, Kind: "file", Name: name, Status: status, Size: size, AddedAt: s.now().UTC()}
	state.Sources = append(state.Sources, record)
	if err := saveSourceState(stateDir, state); err != nil {
		return FileResult{}, err
	}
	return fileResult(record, message), nil
}

func (s *SourceService) ListSources(workspaceID, intakeKey string) ([]SourceRecord, error) {
	requirement, err := s.Requirement(workspaceID, intakeKey)
	if err != nil {
		return nil, err
	}
	state, _, _, err := s.loadState(workspaceID)
	if err != nil {
		return nil, err
	}
	out := make([]SourceRecord, 0)
	for _, source := range state.Sources {
		if source.IntakeKey == requirement.Key {
			out = append(out, source)
		}
	}
	return out, nil
}

// ReadParsedText returns host-stored text for a record that still belongs to
// this workspace and intake state. Request data can never choose the path.
func (s *SourceService) ReadParsedText(workspaceID string, requested SourceRecord) (string, error) {
	state, stateDir, _, err := s.loadState(workspaceID)
	if err != nil {
		return "", err
	}
	for _, source := range state.Sources {
		if source.ID != requested.ID || source.IntakeKey != requested.IntakeKey {
			continue
		}
		if source.Status != SourceStatusParsed || source.ParsedTextFile == "" {
			return "", errors.New("source has no parsed text")
		}
		path := filepath.Clean(filepath.Join(stateDir, filepath.FromSlash(source.ParsedTextFile)))
		textRoot := filepath.Clean(filepath.Join(stateDir, "text"))
		if path != textRoot && !strings.HasPrefix(path, textRoot+string(filepath.Separator)) {
			return "", errors.New("parsed text path leaves the intake state directory")
		}
		data, err := os.ReadFile(path) // #nosec G304 -- path comes only from host-generated state and is contained above
		if err != nil {
			return "", fmt.Errorf("read parsed intake text: %w", err)
		}
		return string(data), nil
	}
	return "", errors.New("intake source not found")
}

func (s *SourceService) BundledSkillDecision(workspaceID, skillName string) (string, error) {
	state, _, _, err := s.loadState(workspaceID)
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(state.BundledSkillDecisions[strings.TrimSpace(skillName)]), nil
}

func (s *SourceService) SetBundledSkillDecision(workspaceID, skillName, choice string) error {
	skillName = strings.TrimSpace(skillName)
	choice = strings.TrimSpace(choice)
	if skillName == "" || (choice != "existing" && choice != "bundled") {
		return errors.New("invalid bundled skill decision")
	}
	lock := s.lockFor(workspaceID, "bundled-skill-"+skillName)
	lock.Lock()
	defer lock.Unlock()
	state, stateDir, _, err := s.loadState(workspaceID)
	if err != nil {
		return err
	}
	if state.BundledSkillDecisions == nil {
		state.BundledSkillDecisions = make(map[string]string)
	}
	state.BundledSkillDecisions[skillName] = choice
	return saveSourceState(stateDir, state)
}

func (s *SourceService) loadState(workspaceID string) (sourceState, string, string, error) {
	if s == nil || s.folders == nil {
		return sourceState{}, "", "", errors.New("blueprint intake folder store is unavailable")
	}
	root, err := s.folders.GetFolderPath(strings.TrimSpace(workspaceID))
	if err != nil || strings.TrimSpace(root) == "" {
		return sourceState{}, "", "", fmt.Errorf("resolve workspace folder: %w", err)
	}
	root = filepath.Clean(root)
	stateDir := filepath.Join(root, StateDirName)
	path := filepath.Join(stateDir, "sources.json")
	data, err := os.ReadFile(path) // #nosec G304 -- path is a resolved workspace folder plus fixed package constants
	if err != nil {
		if os.IsNotExist(err) {
			return sourceState{Version: sourceStateVersion, WorkspaceID: workspaceID}, stateDir, root, nil
		}
		return sourceState{}, "", "", fmt.Errorf("read intake source state: %w", err)
	}
	var state sourceState
	if err := json.Unmarshal(data, &state); err != nil {
		return sourceState{}, "", "", fmt.Errorf("read intake source state: %w", err)
	}
	if state.WorkspaceID != "" && state.WorkspaceID != workspaceID {
		return sourceState{}, "", "", errors.New("intake source state belongs to another workspace")
	}
	state.Version = sourceStateVersion
	state.WorkspaceID = workspaceID
	return state, stateDir, root, nil
}

func saveSourceState(stateDir string, state sourceState) error {
	if err := os.MkdirAll(stateDir, 0o750); err != nil {
		return fmt.Errorf("create intake state directory: %w", err)
	}
	data, err := json.MarshalIndent(state, "", "  ")
	if err != nil {
		return fmt.Errorf("encode intake source state: %w", err)
	}
	data = append(data, '\n')
	tmp, err := os.CreateTemp(stateDir, ".sources-*.tmp")
	if err != nil {
		return fmt.Errorf("create intake source state: %w", err)
	}
	tmpName := tmp.Name()
	defer func() { _ = os.Remove(tmpName) }()
	if err := tmp.Chmod(0o600); err != nil {
		_ = tmp.Close()
		return err
	}
	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("write intake source state: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("close intake source state: %w", err)
	}
	if err := os.Rename(tmpName, filepath.Join(stateDir, "sources.json")); err != nil {
		return fmt.Errorf("replace intake source state: %w", err)
	}
	return nil
}

func countFiles(sources []SourceRecord, intakeKey string) int {
	count := 0
	for _, source := range sources {
		if source.IntakeKey == intakeKey && source.Kind == "file" {
			count++
		}
	}
	return count
}

func extensionAllowed(filename string, accepted []string) bool {
	extension := strings.ToLower(filepath.Ext(filename))
	if len(accepted) == 0 {
		return fileparser.SupportsExtension(extension)
	}
	for _, allowed := range accepted {
		if extension == strings.ToLower(strings.TrimSpace(allowed)) {
			return true
		}
	}
	return false
}

func safeFilename(filename string) (string, error) {
	filename = strings.TrimSpace(filename)
	if filename == "" || strings.ContainsRune(filename, 0) {
		return "", errors.New("invalid filename")
	}
	normalized := strings.ReplaceAll(filename, "\\", "/")
	if filepath.Base(normalized) != normalized || normalized == "." || normalized == ".." {
		return "", errors.New("invalid filename")
	}
	return normalized, nil
}

func displayFilename(filename string) string {
	filename = strings.TrimSpace(strings.ReplaceAll(filename, "\\", "/"))
	name := filepath.Base(filename)
	if name == "." || name == "" {
		return "unnamed file"
	}
	return name
}

func intakeFolderName(key string) string {
	slug := workspace.Slugify(key)
	if slug == "" {
		slug = "intake"
	}
	digest := sha256.Sum256([]byte(key))
	return slug + "-" + hex.EncodeToString(digest[:4])
}

func relativeSlash(root, path string) string {
	relative, err := filepath.Rel(root, path)
	if err != nil {
		return ""
	}
	return filepath.ToSlash(relative)
}

func newSourceID() (string, error) {
	var id [16]byte
	if _, err := rand.Read(id[:]); err != nil {
		return "", fmt.Errorf("create intake source id: %w", err)
	}
	return hex.EncodeToString(id[:]), nil
}

func fileResult(record SourceRecord, message string) FileResult {
	return FileResult{ID: record.ID, Name: record.Name, Status: record.Status, Size: record.Size, ContentHash: record.ContentHash, Message: message}
}

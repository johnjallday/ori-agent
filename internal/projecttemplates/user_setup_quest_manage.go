package projecttemplates

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
)

// UserSetupQuestMutationGuard is the server-owned durable lock seam. Grouped
// manifest mutations call it with fully normalized before/after templates
// before publishing bytes. A nil guard is suitable only while runtime launch is
// not wired (and in focused package tests).
type UserSetupQuestMutationGuard func(before, after Template) error

// UserSetupQuestContentGuard protects skeleton file and lifecycle mutations.
// Every non-manifest template file participates in the execution digest, so a
// durable binding makes these operations all-or-nothing refusals.
type UserSetupQuestContentGuard func(current Template) error

// UserSetupQuestEdit is tri-state at the HTTP boundary: callers omit the edit
// to preserve, pass Remove for explicit null, or pass Draft for replacement.
type UserSetupQuestEdit struct {
	ExpectedRevision string
	Draft            *UserSetupQuestDraft
	Remove           bool
}

// WithLibraryMutationLock serializes one user-quest manifest/SQLite boundary
// with all supported library manifest mutations across processes.
func WithLibraryMutationLock(libDir string, operation func() error) error {
	if operation == nil {
		return errors.New("template mutation operation is required")
	}
	release, err := acquireManifestMutationLock(libDir)
	if err != nil {
		return err
	}
	defer release()
	return operation()
}

func DefaultUserSetupQuestDraft() UserSetupQuestDraft {
	return defaultUserSetupQuestDraft()
}

func UserSetupQuestDraftFor(quest *UserSetupQuest) UserSetupQuestDraft {
	return userSetupQuestDraft(quest)
}

// PreviewUserSetupQuest applies the same host construction and strict
// normalizer as save, without reading or writing progress or canonical owners.
func PreviewUserSetupQuest(template Template, draft UserSetupQuestDraft) (*UserSetupQuest, error) {
	return NewUserSetupQuest(template, template.UserSetupQuest, draft)
}

// UpdateUserSetupQuest atomically changes only user_setup_quest while
// preserving every unrelated top-level manifest field. It performs optimistic
// comparison against the normalized current attachment and validates the whole
// effective template before rename.
func UpdateUserSetupQuest(libDir, id string, edit UserSetupQuestEdit, guard UserSetupQuestMutationGuard) (Template, error) {
	return UpdateUserSetupQuestWithCatalog(libDir, id, edit, guard, defaultRuntimeCatalog())
}

func UpdateUserSetupQuestWithCatalog(libDir, id string, edit UserSetupQuestEdit, guard UserSetupQuestMutationGuard, catalog RuntimeCatalog) (Template, error) {
	if catalog == nil {
		catalog = defaultRuntimeCatalog()
	}
	release, err := acquireManifestMutationLock(libDir)
	if err != nil {
		return Template{}, err
	}
	defer release()
	return updateUserSetupQuestUnlocked(libDir, id, edit, guard, catalog)
}

func updateUserSetupQuestUnlocked(libDir, id string, edit UserSetupQuestEdit, guard UserSetupQuestMutationGuard, catalog RuntimeCatalog) (Template, error) {
	current, err := FindLibraryTemplateWithCatalog(libDir, id, catalog)
	if err != nil {
		return Template{}, err
	}
	if current.Builtin || current.PluginOwner != nil {
		return Template{}, fmt.Errorf("%w: %q", ErrTemplateReadOnly, id)
	}
	if edit.ExpectedRevision == "" || edit.ExpectedRevision != current.UserSetupQuestRevision {
		return Template{}, ErrUserSetupQuestStale
	}
	if edit.Remove && edit.Draft != nil {
		return Template{}, fmt.Errorf("%w: removal and replacement are mutually exclusive", ErrInvalidUserSetupQuest)
	}

	manifestPath := filepath.Join(current.Path, ManifestFileName)
	data, err := os.ReadFile(manifestPath) // #nosec G304 -- current.Path is a resolved library template and the filename is fixed
	if err != nil {
		return Template{}, fmt.Errorf("read template manifest: %w", err)
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.UseNumber()
	var raw map[string]any
	if err := decoder.Decode(&raw); err != nil || raw == nil {
		return Template{}, fmt.Errorf("%w: template.json must contain one object", ErrInvalidUserSetupQuest)
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return Template{}, fmt.Errorf("%w: template.json contains trailing data", ErrInvalidUserSetupQuest)
	}

	switch {
	case edit.Remove:
		delete(raw, "user_setup_quest")
	case edit.Draft != nil:
		quest, buildErr := NewUserSetupQuest(current, current.UserSetupQuest, *edit.Draft)
		if buildErr != nil {
			return Template{}, buildErr
		}
		encoded, marshalErr := json.Marshal(quest)
		if marshalErr != nil {
			return Template{}, fmt.Errorf("%w: encode declaration", ErrInvalidUserSetupQuest)
		}
		var value any
		if unmarshalErr := json.Unmarshal(encoded, &value); unmarshalErr != nil {
			return Template{}, fmt.Errorf("%w: encode declaration", ErrInvalidUserSetupQuest)
		}
		raw["user_setup_quest"] = value
	default:
		return current, nil
	}

	candidateBytes, err := json.MarshalIndent(raw, "", "  ")
	if err != nil {
		return Template{}, fmt.Errorf("encode template manifest: %w", err)
	}
	var candidateManifest manifest
	if err := json.Unmarshal(candidateBytes, &candidateManifest); err != nil {
		return Template{}, fmt.Errorf("%w: effective template manifest is invalid", ErrInvalidUserSetupQuest)
	}
	candidate := newTemplateWithManifest(current.Path, candidateManifest, catalog)
	if candidate.UserSetupQuestError != "" {
		return Template{}, fmt.Errorf("%w: %s", ErrInvalidUserSetupQuest, candidate.UserSetupQuestError)
	}
	if err := ensureUserSetupQuestLibraryIdentity(libDir, candidate, catalog); err != nil {
		return Template{}, err
	}
	if guard != nil {
		if err := guard(current, candidate); err != nil {
			return Template{}, err
		}
	}
	if err := writeManifestAtomic(manifestPath, append(candidateBytes, '\n')); err != nil {
		return Template{}, err
	}
	return newTemplateWithManifest(current.Path, readManifest(current.Path), catalog), nil
}

func ensureUserSetupQuestLibraryIdentity(libDir string, candidate Template, catalog RuntimeCatalog) error {
	if candidate.UserSetupQuest == nil || candidate.UserSetupQuest.Declaration == nil {
		return nil
	}
	templates, err := ListLibraryWithCatalog(libDir, catalog)
	if err != nil {
		return err
	}
	for _, other := range templates {
		if other.ID == candidate.ID || other.UserSetupQuest == nil || other.UserSetupQuest.Declaration == nil {
			continue
		}
		if other.UserSetupQuest.AttachmentID == candidate.UserSetupQuest.AttachmentID ||
			other.UserSetupQuest.Declaration.ID == candidate.UserSetupQuest.Declaration.ID {
			return fmt.Errorf("%w: attachment or quest identity already exists in the template library", ErrInvalidUserSetupQuest)
		}
	}
	return nil
}

func writeManifestAtomic(path string, data []byte) error {
	dir := filepath.Dir(path)
	temporary, err := os.CreateTemp(dir, ".template-manifest-*.tmp") // #nosec G304 -- dir is the resolved template directory
	if err != nil {
		return fmt.Errorf("create temporary template manifest: %w", err)
	}
	temporaryPath := temporary.Name()
	clean := func() { _ = os.Remove(temporaryPath) }
	defer clean()
	if err := temporary.Chmod(0o600); err != nil {
		_ = temporary.Close()
		return fmt.Errorf("prepare temporary template manifest: %w", err)
	}
	if _, err := temporary.Write(data); err != nil {
		_ = temporary.Close()
		return fmt.Errorf("write temporary template manifest: %w", err)
	}
	if err := temporary.Sync(); err != nil {
		_ = temporary.Close()
		return fmt.Errorf("flush temporary template manifest: %w", err)
	}
	if err := temporary.Close(); err != nil {
		return fmt.Errorf("close temporary template manifest: %w", err)
	}
	if err := os.Rename(temporaryPath, path); err != nil {
		return fmt.Errorf("publish template manifest: %w", err)
	}
	directory, err := os.Open(dir) // #nosec G304 -- dir is the resolved template directory
	if err == nil {
		_ = directory.Sync()
		_ = directory.Close()
	}
	return nil
}

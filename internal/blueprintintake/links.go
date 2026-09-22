package blueprintintake

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strings"
)

func (s *SourceService) AddLink(ctx context.Context, workspaceID, intakeKey, rawURL string) (FileResult, error) {
	requirement, err := s.Requirement(workspaceID, intakeKey)
	if err != nil {
		return FileResult{}, err
	}
	if !requirement.Sources.URL {
		return FileResult{}, ErrLinksDisabled
	}
	consent, err := s.ConsentStatus(ctx, workspaceID, requirement.Key)
	if err != nil || !consent.Accepted {
		return FileResult{}, ErrConsentNeeded
	}
	if s.fetcher == nil {
		return FileResult{}, fmt.Errorf("link fetcher is unavailable")
	}

	lock := s.lockFor(workspaceID, requirement.Key)
	lock.Lock()
	defer lock.Unlock()
	state, stateDir, _, err := s.loadState(workspaceID)
	if err != nil {
		return FileResult{}, err
	}
	if countLinks(state.Sources, requirement.Key) >= MaxLinksPerIntake {
		return FileResult{}, ErrLinkLimit
	}
	snapshot, err := s.fetcher.Fetch(ctx, rawURL)
	if err != nil {
		return FileResult{}, err
	}
	id, err := newSourceID()
	if err != nil {
		return FileResult{}, err
	}
	text := strings.TrimSpace(snapshot.Content)
	digest := sha256.Sum256([]byte(text))
	now := s.now().UTC()
	name := strings.TrimSpace(snapshot.Title)
	if name == "" {
		if parsed, parseErr := url.Parse(snapshot.URL); parseErr == nil {
			name = parsed.Hostname()
		}
	}
	if name == "" {
		name = "Linked page"
	}
	if err := os.MkdirAll(filepath.Join(stateDir, "text"), 0o750); err != nil {
		return FileResult{}, fmt.Errorf("create parsed-text directory: %w", err)
	}
	if err := os.MkdirAll(filepath.Join(stateDir, "pages"), 0o750); err != nil {
		return FileResult{}, fmt.Errorf("create page snapshot directory: %w", err)
	}
	textName := id + ".txt"
	if err := os.WriteFile(filepath.Join(stateDir, "text", textName), []byte(text), 0o600); err != nil {
		return FileResult{}, fmt.Errorf("store linked page text: %w", err)
	}
	pageName := id + ".page"
	if err := os.WriteFile(filepath.Join(stateDir, "pages", pageName), snapshot.Body, 0o600); err != nil {
		return FileResult{}, fmt.Errorf("store linked page snapshot: %w", err)
	}
	record := SourceRecord{ID: id, IntakeKey: requirement.Key, Kind: "link", Name: name, Title: snapshot.Title, URL: snapshot.URL, ParsedTextFile: filepath.ToSlash(filepath.Join("text", textName)), SnapshotFile: filepath.ToSlash(filepath.Join("pages", pageName)), ContentType: snapshot.ContentType, ContentHash: hex.EncodeToString(digest[:]), Status: SourceStatusParsed, Size: int64(len(snapshot.Body)), AddedAt: now, FetchedAt: &now}
	state.Sources = append(state.Sources, record)
	if err := saveSourceState(stateDir, state); err != nil {
		return FileResult{}, err
	}
	return fileResult(record, ""), nil
}

func countLinks(sources []SourceRecord, intakeKey string) int {
	count := 0
	for _, source := range sources {
		if source.IntakeKey == intakeKey && source.Kind == "link" {
			count++
		}
	}
	return count
}

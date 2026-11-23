package folderagent

import (
	"context"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/johnjallday/ori-agent/internal/filejanitor"
	"github.com/johnjallday/ori-agent/internal/foldermap"
	"github.com/johnjallday/ori-agent/internal/secondbrain"
)

func (s *Service) SetupDocumentsDraft(ctx context.Context, owner, id string, in DocumentsDraftInput, confirm bool) (DocumentsDraftView, error) {
	var out DocumentsDraftView
	_, root, location, proposal, err := s.resolveDraft(ctx, owner, id, in)
	if err != nil {
		return out, err
	}
	if s.deps.DocumentsSetup == nil {
		return out, ErrUnavailable
	}
	name := strings.TrimSpace(in.Name)
	body, rel, err := reviewedBody(name, in.Answer, location)
	if err != nil {
		return out, err
	}
	setup, err := s.deps.DocumentsSetup(ctx, owner, in.HQ, root, documentsRef(proposal, rel, body), in.Review, confirm)
	if err != nil {
		return out, err
	}
	return DocumentsDraftView{Name: name, Rel: rel, Body: body, Review: setup.Ref, SetupPath: setup.Path, Generation: setup.Generation, Notebook: secondbrain.DefaultFolderName}, nil
}

func documentsVaults(rec *Record, root, report string, guards []string) []DocumentsCandidate {
	var found []foldermap.Folder
	for _, f := range rec.Map.Folders {
		if f.HasVault && safeMarkedFolder(f) {
			found = append(found, f)
		}
	}
	sort.Slice(found, func(i, j int) bool { return found[i].Path < found[j].Path })
	var out []DocumentsCandidate
	for i, f := range found {
		if i == 64 || len(out) == 4 {
			break
		}
		path := filepath.Join(root, filepath.FromSlash(f.Path))
		skip := false
		for _, g := range guards {
			if filejanitor.RootsOverlap(path, g) {
				skip = true
				break
			}
		}
		if skip || !explicitMember(root, rec.Map, f) || !secondbrain.IsObsidianVault(path) {
			continue
		}
		out = append(out, DocumentsCandidate{Ref: documentsRef("vault", report, f.Path, f.Witness), Location: "Documents/" + f.Path, Name: f.Name(), Kind: "Obsidian vault", Evidence: ".obsidian folder"})
	}
	return out
}

type vaultChoice struct {
	Owner, Workspace string
	Input            DocumentsDraftInput
	Until            time.Time
}

// Only the Notes connector can resolve a Documents vault selection. The token
// cannot be used as a generic folder picker or turned into a hire or grant.
func (s *Service) chooseVaultPath(ctx context.Context, c vaultChoice) (string, error) {
	rec, root, err := s.documentsReport(ctx, c.Owner, c.Input.HQ, c.Input.Attempt, c.Workspace)
	if err != nil {
		return "", err
	}
	report := documentsRef(rec.MapWitness, holderReportKey(rec))
	if report != c.Input.Report || s.deps.DocumentsScope == nil {
		return "", ErrWrongState
	}
	scope, err := s.deps.DocumentsScope(ctx, c.Owner)
	if err != nil || scope.Notebook != "" {
		return "", ErrWrongState
	}
	root, err = filepath.EvalSymlinks(root)
	if err != nil {
		return "", ErrWrongState
	}
	guards := append([]string(nil), scope.Protected...)
	for i, p := range guards {
		if real, e := filepath.EvalSymlinks(p); e == nil {
			guards[i] = real
		}
	}
	for _, v := range documentsVaults(rec, root, report, guards) {
		if v.Ref == c.Input.Candidate {
			return filepath.Join(root, filepath.FromSlash(strings.TrimPrefix(v.Location, "Documents/"))), nil
		}
	}
	return "", ErrWrongState
}
func (s *Service) ChooseDocumentsVault(ctx context.Context, owner, id string, in DocumentsDraftInput) (string, error) {
	c := vaultChoice{Owner: owner, Workspace: id, Input: DocumentsDraftInput{HQ: in.HQ, Attempt: in.Attempt, Report: in.Report, Candidate: in.Candidate}, Until: s.deps.Now().Add(choiceTTL)}
	if _, err := s.chooseVaultPath(ctx, c); err != nil {
		return "", err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.vaultChoices == nil {
		s.vaultChoices = map[string]vaultChoice{}
	}
	for k, v := range s.vaultChoices {
		if !s.deps.Now().Before(v.Until) {
			delete(s.vaultChoices, k)
		}
	}
	if len(s.vaultChoices) >= 64 {
		return "", ErrUnavailable
	}
	token := "documents-vault-" + newToken()
	s.vaultChoices[token] = c
	return token, nil
}
func (s *Service) ResolveDocumentsVault(ctx context.Context, owner, token string) (string, error) {
	s.mu.Lock()
	c, ok := s.vaultChoices[token]
	s.mu.Unlock()
	if !ok || c.Owner != owner || !s.deps.Now().Before(c.Until) {
		return "", ErrWrongState
	}
	return s.chooseVaultPath(ctx, c)
}

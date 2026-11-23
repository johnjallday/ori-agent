package folderagent

import (
	"context"
	"encoding/json"
	"path/filepath"
	"slices"
	"strings"

	"github.com/johnjallday/ori-agent/internal/filejanitor"
	"github.com/johnjallday/ori-agent/internal/folderdigest"
	"github.com/johnjallday/ori-agent/internal/llm"
	"github.com/johnjallday/ori-agent/internal/secondbrain"
	"github.com/johnjallday/ori-agent/internal/sensitive"
	"github.com/johnjallday/ori-agent/internal/workspace"
)

const reviewBatchSize = 8

type ReviewSelection struct {
	Examples []string `json:"examples,omitempty"`
	Exclude  []string `json:"exclude,omitempty"`
}
type ReviewOption struct {
	Ref      string `json:"ref"`
	Name     string `json:"name"`
	Included bool   `json:"included"`
	Example  bool   `json:"example,omitempty"`
}
type ReviewChat interface {
	Chat(context.Context, llm.ChatRequest) (*llm.ChatResponse, error)
}
type ReviewProcessor struct {
	Name  string `json:"name,omitempty"`
	Model string `json:"model,omitempty"`
	Pin   string `json:"-"`
}
type ReviewSource struct {
	Ref     string                  `json:"ref"`
	Path    string                  `json:"path"`
	Version workspace.SourceVersion `json:"version"`
	Example bool                    `json:"example,omitempty"`
}
type OrganizationPreview struct {
	Files     []string        `json:"files"`
	Options   []ReviewOption  `json:"options"`
	Folders   []ReviewOption  `json:"folders"`
	Remaining int             `json:"remaining"`
	Partial   bool            `json:"partial"`
	Processor ReviewProcessor `json:"processor"`
	Approval  string          `json:"approval,omitempty"`
}
type organizationScope struct {
	OrganizationPreview
	sources                 []ReviewSource
	root, rootID, authority string
}

func reviewFormat(name string) bool {
	switch strings.ToLower(filepath.Ext(name)) {
	case ".pdf", ".docx", ".txt", ".md", ".csv":
		return true
	}
	return false
}
func scopeHash(v any) string { b, _ := json.Marshal(v); return documentsRef(string(b)) }

func (s *Service) reviewScope(ctx context.Context, owner string, rec *Record, sel ReviewSelection) (organizationScope, error) {
	var out organizationScope
	a, err := s.organizationAccess(owner, rec)
	if err != nil {
		return out, err
	}
	if len(sel.Examples) > 2 || len(sel.Exclude) > reviewBatchSize {
		return out, ErrWrongState
	}
	rootFolder, _ := rec.Map.Folder("")
	out.rootID = rootFolder.BranchWitness
	out.root = a.Root
	out.authority = documentsRef(owner, rec.WorkspaceID, a.Root, out.rootID, a.Lead, a.Binding)
	var protected []string
	if s.deps.DocumentsProtected != nil {
		protected = s.deps.DocumentsProtected()
	}
	allowed := func(rel string) bool {
		for _, p := range protected {
			if filejanitor.RootsOverlap(filepath.Join(a.Root, rel), p) {
				return false
			}
		}
		return true
	}
	if s.deps.ReviewModel != nil {
		out.Processor, _ = s.deps.ReviewModel(rec.WorkspaceID, a.Lead)
	}
	listing, err := workspace.ListContainedEntries(a.Root, "", 1)
	if err != nil {
		return out, ErrWrongState
	}
	out.Partial = listing.Truncated
	out.Files = []string{}
	var loose, examples []ReviewSource
	matchedFolders := 0
	collect := func(list workspace.DirectoryListing, example bool) []ReviewSource {
		var rows []ReviewSource
		for _, e := range list.Entries {
			if e.Kind != "file" || !reviewFormat(e.Name) || !secondbrain.ValidFolderName(e.Name) || !allowed(e.RelativePath) || sensitive.ContainsSecretLikeText(e.Name) {
				continue
			}
			src := ReviewSource{Path: e.RelativePath, Version: e.Version, Example: example}
			src.Ref = documentsRef(out.authority, src.Path, scopeHash(src.Version))
			rows = append(rows, src)
		}
		return rows
	}
	seen := map[string]bool{}
	for _, run := range rec.OrganizationRuns {
		if run.Authority != out.authority || run.Result.Status != "reviewed" {
			continue
		}
		for _, item := range run.Result.Files {
			if !item.Source.Example {
				seen[item.Source.Ref] = true
			}
		}
	}
	loose = slices.DeleteFunc(collect(listing, false), func(src ReviewSource) bool { return seen[src.Ref] })
	for _, dir := range listing.Entries {
		if dir.Kind != "folder" || !secondbrain.ValidFolderName(dir.Name) || folderdigest.IsSkippedFolder(dir.Name) || !allowed(dir.Name) || sensitive.ContainsSecretLikeText(dir.Name) {
			continue
		}
		ref := documentsRef(out.authority, dir.Name, dir.Version.Identity)
		picked := slices.Contains(sel.Examples, ref)
		out.Folders = append(out.Folders, ReviewOption{Ref: ref, Name: dir.Name, Included: picked})
		if !picked {
			continue
		}
		matchedFolders++
		ls, err := workspace.ListContainedEntries(a.Root, dir.Name, 1)
		if err != nil {
			return out, ErrWrongState
		}
		out.Partial = out.Partial || ls.Truncated
		rows := collect(ls, true)
		if len(rows) > 0 {
			examples = append(examples, rows[0])
		}
	}
	if matchedFolders != len(sel.Examples) {
		return out, ErrWrongState
	}
	selectedLoose := min(len(loose), reviewBatchSize-len(examples))
	out.Remaining = len(loose) - selectedLoose
	matchedExclude := 0
	for _, src := range append(loose[:selectedLoose:selectedLoose], examples...) {
		picked := !slices.Contains(sel.Exclude, src.Ref)
		out.Options = append(out.Options, ReviewOption{src.Ref, src.Path, picked, src.Example})
		if !picked {
			matchedExclude++
			if !src.Example {
				out.Remaining++
			}
			continue
		}
		out.Files = append(out.Files, src.Path)
		out.sources = append(out.sources, src)
	}
	if matchedExclude != len(sel.Exclude) {
		return out, ErrWrongState
	}
	current, err := s.organizationAccess(owner, rec)
	if err != nil || current != a {
		return out, ErrWrongState
	}
	if len(out.sources) > 0 && out.Processor.Pin != "" {
		out.Approval = scopeHash([]any{"organization-v1", out.authority, out.Processor, out.Processor.Pin, out.sources, out.Folders, out.Partial, out.Remaining, rec.Revision})
	}
	return out, nil
}
func (s *Service) PreviewOrganization(ctx context.Context, owner, id string, sel ReviewSelection) (OrganizationPreview, error) {
	rec, err := s.deps.Store.Read(id)
	if err != nil {
		return OrganizationPreview{}, err
	}
	scope, err := s.reviewScope(ctx, owner, rec, sel)
	if err != nil {
		return OrganizationPreview{}, err
	}
	return scope.OrganizationPreview, nil
}

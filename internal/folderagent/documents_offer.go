package folderagent

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"github.com/johnjallday/ori-agent/internal/directoryid"
	"github.com/johnjallday/ori-agent/internal/filejanitor"
	"github.com/johnjallday/ori-agent/internal/folderdigest"
	"github.com/johnjallday/ori-agent/internal/foldermap"
	"github.com/johnjallday/ori-agent/internal/secondbrain"
	"github.com/johnjallday/ori-agent/internal/sensitive"
)

// DocumentsScope is trusted host state. Paths are never returned to the UI.
type DocumentsScope struct {
	Protected []string
	Notebook  string
	Writable  bool
	Setup     bool
}
type DocumentsCandidate struct {
	Ref      string `json:"ref"`
	Name     string `json:"name"`
	Location string `json:"location"`
	Kind     string `json:"kind"`
	Evidence string `json:"evidence"`
}
type DocumentsOffer struct {
	NotesOnly bool                 `json:"notes_only,omitempty"`
	Vaults    []DocumentsCandidate `json:"vaults,omitempty"`
	Setup     bool                 `json:"setup,omitempty"`
	Writable  bool                 `json:"writable,omitempty"`
	Report    string               `json:"report"`
	Projects  []DocumentsCandidate `json:"projects"`
}

var projectLabel = regexp.MustCompile(`^[\pL\pN][\pL\pN _.()-]*$`)

func safeMember(f foldermap.Folder) bool {
	if !safeMarkedFolder(f) || f.HasVault {
		return false
	}
	for _, marker := range folderdigest.Markers {
		if marker.Name == f.Marker && marker.Shape == f.Shape && marker.Name != ".obsidian" {
			return true
		}
	}
	return false
}

func safeMarkedFolder(f foldermap.Folder) bool {
	if f.Path == "" || len(f.Path) > 256 || f.Depth() > 12 || f.State != foldermap.StateClosed || f.Witness == "" || f.MarkerFiles < 1 {
		return false
	}
	for segment := range strings.SplitSeq(f.Path, "/") {
		if !secondbrain.ValidFolderName(segment) || !projectLabel.MatchString(segment) || sensitive.ContainsSecretLikeText(segment) || strings.EqualFold(segment, "My Notes") || strings.EqualFold(segment, "Ori Workspaces") {
			return false
		}
	}
	return true
}

// explicitMember checks only the components named by the report. Never list a
// directory or open a source file to generate the offer.
func explicitMember(root string, m *foldermap.Map, f foldermap.Folder) bool {
	parent := ""
	for segment := range strings.SplitSeq(f.Path, "/") {
		p, ok := m.Folder(parent)
		if !ok || p.State != foldermap.StateWalked || p.HasVault {
			return false
		}
		if parent != "" {
			parent += "/"
		}
		parent += segment
		info, err := os.Lstat(filepath.Join(root, filepath.FromSlash(parent))) // #nosec G304 G703 -- relative path components validated from the saved walk
		if err != nil || !info.IsDir() || (parent == f.Path && f.Witness != directoryid.Witness(info)) {
			return false
		}
	}
	return true
}

func projectOccupied(notebook, name string) bool {
	// Check the project folder, not just its README. A link or inaccessible
	// parent is not a vacancy, and no note body is needed for ordering.
	for _, path := range []string{notebook, filepath.Join(notebook, "Projects")} {
		info, err := os.Lstat(path)
		if err != nil || !info.IsDir() {
			return true
		}
	}
	_, err := os.Lstat(filepath.Join(notebook, "Projects", name))
	return !errors.Is(err, os.ErrNotExist)
}

func (s *Service) DocumentsOffer(ctx context.Context, owner, hq, attempt, id string) (*DocumentsOffer, error) {
	rec, root, err := s.documentsReport(ctx, owner, hq, attempt, id)
	if errors.Is(err, ErrWrongState) || errors.Is(err, ErrNoRecord) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	if s.deps.DocumentsScope == nil {
		return nil, nil
	}
	scope, err := s.deps.DocumentsScope(ctx, owner)
	if err != nil {
		return nil, err
	}
	root, err = filepath.EvalSymlinks(root)
	if err != nil {
		return nil, nil
	}
	guards := append([]string{scope.Notebook}, scope.Protected...)
	for i, p := range guards {
		if real, err := filepath.EvalSymlinks(p); p != "" && err == nil {
			guards[i] = real
		}
	}
	var folders []foldermap.Folder
	for _, f := range rec.Map.Folders {
		if safeMember(f) {
			folders = append(folders, f)
		}
	}
	sort.Slice(folders, func(i, j int) bool {
		a, b := folders[i], folders[j]
		if !a.Newest.Equal(b.Newest) {
			return a.Newest.After(b.Newest)
		}
		return a.Path < b.Path
	})
	report := documentsRef(rec.MapWitness, holderReportKey(rec))
	out := &DocumentsOffer{Report: report, Setup: scope.Setup, Writable: scope.Writable && !filejanitor.RootsOverlap(root, scope.Notebook)}
	var taken []DocumentsCandidate
	// Cap live metadata probes even on an unusually large or badly stale map.
	for i, f := range folders {
		if i == 64 {
			break
		}
		path := filepath.Join(root, filepath.FromSlash(f.Path))
		blocked := false
		for _, p := range guards {
			if filejanitor.RootsOverlap(path, p) {
				blocked = true
				break
			}
		}
		if blocked || !explicitMember(root, rec.Map, f) {
			continue
		}
		for _, marker := range folderdigest.Markers {
			if marker.Name != f.Marker || marker.Shape != f.Shape || marker.Name == ".obsidian" {
				continue
			}
			c := DocumentsCandidate{Ref: documentsRef(report, f.Path, f.Witness), Name: f.Name(), Location: "Documents/" + f.Path, Kind: foldermap.ShapeNoun(f.Shape, 1), Evidence: marker.Label + " (" + marker.Name + ")"}
			if scope.Writable && projectOccupied(scope.Notebook, f.Name()) {
				taken = append(taken, c)
			} else {
				out.Projects = append(out.Projects, c)
			}
			break
		}
		if len(out.Projects) == 4 {
			break
		}
	}
	out.Projects = append(out.Projects, taken...)
	if len(out.Projects) > 4 {
		out.Projects = out.Projects[:4]
	}
	if scope.Notebook == "" {
		out.Vaults = documentsVaults(rec, root, report, guards)
	} else if !scope.Writable && len(out.Projects) == 0 {
		// A vault report still has a voluntary Notes door, never a competing
		// connection or a pitch to write into an existing vault.
		out.NotesOnly = len(documentsVaults(rec, root, report, guards[1:])) > 0
	}
	if len(out.Projects) == 0 && len(out.Vaults) == 0 && !out.NotesOnly {
		return nil, nil
	}
	return out, nil
}

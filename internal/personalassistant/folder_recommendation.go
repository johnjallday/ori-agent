package personalassistant

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"
	"time"

	"github.com/johnjallday/ori-agent/internal/foldercontext"
	"github.com/johnjallday/ori-agent/internal/folderdigest"
)

// FolderRecommendation is discovery, never a plan or a grant. Readiness and
// pending review ownership are separate from the observed collection's shape.
type FolderRecommendation struct {
	ObservationID     string                          `json:"observation_id,omitempty"`
	Operation         string                          `json:"operation"`
	Evidence          string                          `json:"evidence"`
	Projects          int                             `json:"projects"`
	SupportedProjects int                             `json:"supported_projects"`
	Coverage          foldercontext.Coverage          `json:"coverage"`
	Destination       FolderRecommendationDestination `json:"destination"`
	Alternative       string                          `json:"alternative,omitempty"`
	Limitation        string                          `json:"limitation,omitempty"`
}

// Existing folder links are canonical metadata, not permission to open files.
// Discovery and explicit review share this exclusion so reattachment cannot
// suggest creating another workspace for an already linked folder.
func (s *FolderDigestService) readStructureLinks(ctx context.Context, userID string, result folderdigest.Result, structure *FolderStructure) {
	for i := range structure.Members {
		member := &structure.Members[i]
		path := filepath.Join(result.Root, member.RelPath)
		if identity, err := portfolioDirectoryIdentity(path); err == nil {
			member.Identity = identity
		}
		if s.deps.ExistingProjects != nil {
			if existing, err := s.deps.ExistingProjects(ctx, userID, path, member.Identity); err == nil && len(existing) > 0 {
				member.ExistingWorkspace = foldercontext.DisplayName(existing[0].Name)
				member.Included = false
			}
		}
	}
}

// Destination is only display data. Exact destination identity is resolved
// again by explicit Review and bound into its canonical plan digest.
type FolderRecommendationDestination struct {
	State     string `json:"state"` // existing, absent, unknown
	Name      string `json:"name,omitempty"`
	Readiness string `json:"readiness"` // review, provider_missing/disabled/outdated, unavailable
}

// FolderRecommendationReader must use local canonical records only: no
// prerequisite execution, release lookup, model call or offer allocation.
type FolderRecommendationReader interface {
	ReadFolderRecommendation(context.Context, string, string) FolderRecommendationDestination
}

func (r *FolderRecommendation) valid() bool {
	if r == nil {
		return true
	}
	if r.Operation != FolderGroupPlain && r.Operation != FolderGroupLibrary {
		return false
	}
	if r.Projects < 0 || r.Projects > foldercontext.MaxEntries || r.SupportedProjects < 0 || r.SupportedProjects > r.Projects {
		return false
	}
	if len(r.ObservationID) > 96 || strings.ContainsAny(r.ObservationID, "/\\\x00\r\n") {
		return false
	}
	switch r.Destination.State {
	case "existing", "absent", "unknown":
	default:
		return false
	}
	switch r.Destination.Readiness {
	case "review", "provider_missing", "provider_disabled", "provider_outdated", "unavailable", "destination_ambiguous", "destination_incompatible":
	default:
		return false
	}
	for _, value := range []string{r.Evidence, r.Alternative, r.Limitation, r.Destination.Name} {
		if len(value) > 512 || strings.ContainsAny(value, "/\\\x00\r\n") {
			return false
		}
	}
	return true
}

func collectionRecommendation(result folderdigest.Result, verdict folderdigest.Verdict, now time.Time) *FolderRecommendation {
	if !folderdigest.IsCollection(result, now) {
		return nil
	}
	r := &FolderRecommendation{Operation: FolderGroupPlain,
		Destination: FolderRecommendationDestination{State: "absent", Name: foldercontext.DisplayName(result.Name), Readiness: "review"},
		Alternative: "These observed folders do not meet a specialized library's recognition rules. A plain group keeps their workspaces together."}
	counts := map[string]int{}
	var order []string
	for _, candidate := range result.Subfolders() {
		if !folderdigest.IsProject(candidate, now) {
			continue
		}
		r.Projects++
		label := "other project"
		if candidate.Marker != nil {
			label = candidate.Marker.Label
		}
		if counts[label] == 0 {
			order = append(order, label)
		}
		counts[label]++
	}
	var evidence []string
	for _, label := range order {
		suffix := "folders"
		if counts[label] == 1 {
			suffix = "folder"
		}
		evidence = append(evidence, fmt.Sprintf("%d %s %s", counts[label], label, suffix))
	}
	r.Evidence = "Observed " + strings.Join(evidence, ", ") + "."
	if result.Partial {
		r.Evidence += " This scan is partial."
	}
	if verdict.Portfolio != nil {
		if row, ok := folderdigest.CapabilityForShape(verdict.Portfolio.Shape); ok && row.Offer != nil && row.Offer.HomeProviderKey != "" {
			r.Operation = FolderGroupLibrary
			r.Destination = FolderRecommendationDestination{State: "unknown", Name: row.Offer.HomeName, Readiness: "unavailable"}
			_, r.SupportedProjects = folderdigest.IntegrationProjects(result, verdict.Portfolio.Shape, now)
			r.Alternative = "A plain group creates a workspace for each selected folder."
			if r.SupportedProjects < verdict.Portfolio.Projects {
				r.Limitation = "Recognized formats can be listed in the library; only supported integrations can open project workspaces."
			}
		}
	}
	return r
}

func (s *FolderDigestService) recommendCollection(ctx context.Context, userID string, result folderdigest.Result, verdict folderdigest.Verdict) *FolderRecommendation {
	r := collectionRecommendation(result, verdict, s.now())
	if r == nil || r.Operation != FolderGroupLibrary {
		return r
	}
	row, _ := folderdigest.CapabilityForShape(verdict.Portfolio.Shape)
	if reader, ok := s.deps.Setup.(FolderRecommendationReader); ok {
		destination := reader.ReadFolderRecommendation(ctx, userID, row.Offer.HomeProviderKey)
		// Unknown state never becomes evidence that a new Home is needed.
		switch destination.State {
		case "existing", "absent", "unknown":
			if destination.Name == "" {
				destination.Name = row.Offer.HomeName
			}
			destination.Name = foldercontext.DisplayName(destination.Name)
			r.Destination = destination
		}
	}
	return r
}

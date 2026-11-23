package folderagent

import (
	"context"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/johnjallday/ori-agent/internal/filejanitor"
	"github.com/johnjallday/ori-agent/internal/workspace"
)

// A saved view and its decisions live in the sidecar, not the physical root.
type OrganizingDecision struct {
	Property    string    `json:"property,omitempty"`
	FileKey     string    `json:"file_key,omitempty"`
	Intentional bool      `json:"intentional,omitempty"`
	ID          string    `json:"id"`
	Kind        string    `json:"kind"`
	Time        time.Time `json:"time"`
	Group       string    `json:"group,omitempty"`
	Sources     []string  `json:"sources"`
	Proposed    string    `json:"proposed,omitempty"`
	Choice      string    `json:"choice"`
	Outcome     string    `json:"outcome"`
	Exception   bool      `json:"exception,omitempty"`
	// Filing is the File Janitor filing this decision belongs to (Kind "file").
	// One filing has exactly one decision; the journal, not this field, is the
	// evidence of what moved.
	Filing string `json:"filing,omitempty"`
}
type ViewChange struct {
	ToReview   string `json:"to_review,omitempty"`
	ToRevision int    `json:"to_revision,omitempty"`
	Review     string `json:"review"`
	Revision   int    `json:"revision"`
	Action     string `json:"action"`
	Group      string `json:"group"`
	Source     string `json:"source,omitempty"`
	To         string `json:"to,omitempty"`
	Name       string `json:"name,omitempty"`
	Decision   string `json:"decision,omitempty"`
}

// Inspect opens an original only on a fresh press, after matching the opaque
// reference to an admitted source and its current, contained version. A native
// viewer is never replaced with an executable association or a file:// URL.
func (s *Service) InspectOrganizationSource(ctx context.Context, owner, id, review, ref string) error {
	rec, auth, err := s.latestReview(owner, id)
	if err != nil {
		return err
	}
	for _, run := range rec.OrganizationRuns {
		if run.Authority != auth || run.Result.ID != review {
			continue
		}
		for _, f := range run.Result.Files {
			if f.Source.Ref != ref {
				continue
			}
			src := f.Source
			if src.Version.Placeholder || src.Version.Identity == "" {
				return ErrWrongState
			}
			a, err := s.organizationAccess(owner, rec)
			if err != nil {
				return err
			}
			rel, err := workspace.ContainedRelativePath(src.Path)
			if err != nil || rel != src.Path {
				return ErrWrongState
			}
			abs := filepath.Join(a.Root, filepath.FromSlash(rel))
			if s.deps.DocumentsProtected != nil {
				for _, p := range s.deps.DocumentsProtected() {
					if filejanitor.RootsOverlap(abs, p) {
						return ErrWrongState
					}
				}
			}
			listing, err := workspace.ListContainedEntries(a.Root, filepath.Dir(rel), 1)
			if err != nil {
				return ErrWrongState
			}
			if !slices.ContainsFunc(listing.Entries, func(entry workspace.DirectoryEntry) bool {
				return entry.Kind == "file" && entry.RelativePath == rel && entry.Version == src.Version
			}) {
				return ErrWrongState
			}
			if _, actual, err := s.latestReview(owner, id); err != nil || actual != auth || ctx.Err() != nil {
				return ErrWrongState
			}
			if s.deps.ReviewOpen == nil || s.deps.ReviewOpen(abs) != nil {
				return ErrUnavailable
			}
			return nil
		}
	}
	return ErrWrongState
}
func (s *Service) ChangeOrganizationView(owner, id string, c ViewChange) (ReviewResult, error) {
	var out ReviewResult
	_, auth, err := s.latestReview(owner, id)
	if err != nil {
		return out, err
	}
	key := scopeHash(c)
	_, err = s.deps.Store.Update(id, func(r *Record) error {
		a, err := s.organizationAccess(owner, r)
		if err != nil {
			return err
		}
		root, _ := r.Map.Folder("")
		if auth != documentsRef(owner, id, a.Root, root.BranchWitness, a.Lead, a.Binding) {
			return ErrWrongState
		}
		for i := range r.OrganizationRuns {
			run := &r.OrganizationRuns[i]
			if run.Authority != auth || run.Result.ID != c.Review {
				continue
			}
			v := &run.Result
			for _, d := range v.Decisions {
				if d.ID == key {
					out = *v
					return errReviewReplay
				}
			}
			if v.Status == reviewRunning || v.Status == "scope changed" || v.Revision != c.Revision {
				return ErrWrongState
			}
			if c.Action == "confirm_observation" || c.Action == "exception" {
				for j := range v.Decisions {
					d := &v.Decisions[j]
					if d.ID != c.Decision {
						continue
					}
					if c.Action == "confirm_observation" {
						if d.Kind != "observed" || d.Intentional {
							return ErrWrongState
						}
						d.Intentional = true
						d.Outcome = "Observed on disk · confirmed as your placement"
					} else {
						if d.Exception {
							return ErrWrongState
						}
						d.Exception = true
					}
					v.Revision++
					out = *v
					return nil
				}
				return ErrWrongState
			}
			idx := slices.IndexFunc(v.Groups, func(g DocumentGroup) bool { return g.ID == c.Group })
			if idx < 0 || v.Status != "reviewed" || len(v.Decisions) >= 256 {
				return ErrWrongState
			}
			g := &v.Groups[idx]
			d := OrganizingDecision{ID: key, Kind: c.Action, Time: s.deps.Now().UTC(), Group: g.ID, Sources: slices.Clone(g.Members), Proposed: g.Title, Choice: g.Title, Outcome: "View saved. No files moved."}
			switch c.Action {
			case "not_now":
				if g.Kept || g.Dismissed {
					return ErrWrongState
				}
				g.Dismissed = true
				d.Choice = "Not now"
				d.Outcome = "Suggestion dismissed. No files moved."
			case "keep":
				if g.Kept || g.Dismissed {
					return ErrWrongState
				}
				g.Kept = true
			case "rename":
				name := strings.TrimSpace(c.Name)
				if !g.Kept || !projectWords.MatchString(name) || name == g.Title {
					return ErrWrongState
				}
				g.Title = name
				d.Choice = name
			case "loose", "move_member":
				if !g.Kept || !slices.Contains(g.Members, c.Source) {
					return ErrWrongState
				}
				g.Members = slices.DeleteFunc(g.Members, func(ref string) bool { return ref == c.Source })
				delete(g.Reasons, c.Source)
				d.Sources = []string{c.Source}
				d.Choice = "Leave loose"
				if c.Action == "move_member" {
					target := v
					if c.ToReview != "" && c.ToReview != v.ID {
						other := slices.IndexFunc(r.OrganizationRuns, func(other OrganizationRun) bool {
							return other.Authority == auth && other.Result.ID == c.ToReview && other.Result.Status == "reviewed"
						})
						if other < 0 {
							return ErrWrongState
						}
						target = &r.OrganizationRuns[other].Result
						if target.Revision != c.ToRevision {
							return ErrWrongState
						}
						target.Revision++
					}
					to := slices.IndexFunc(target.Groups, func(dest DocumentGroup) bool {
						return dest.Kept && dest.ID == c.To && (target != v || dest.ID != g.ID) && !slices.Contains(dest.Members, c.Source) && len(dest.Members) < 64
					})
					if to < 0 {
						return ErrWrongState
					}
					dest := &target.Groups[to]
					dest.Members = append(dest.Members, c.Source)
					dest.Reasons[c.Source] = "Included by you."
					d.Choice = dest.Title
				}
			default:
				return ErrWrongState
			}
			v.Decisions = append(v.Decisions, d)
			v.Revision++
			out = *v
			return nil
		}
		return ErrWrongState
	})
	if err == errReviewReplay {
		err = nil
	}
	return out, err
}

package folderagent

import (
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"reflect"
	"strings"

	"github.com/johnjallday/ori-agent/internal/folderlook"
	"github.com/johnjallday/ori-agent/internal/personalassistant"
)

// RootIdentity is ephemeral host context. Binding is a digest, never a path.
// No identity flag in a saved report authorizes mutations.
type RootIdentity struct {
	Policy  folderlook.RootPolicy
	Binding string
}

// ResolveRootIdentity compares directories using filesystem identity (including
// symlink aliases and the actual volume's case rules). It never scans contents.
func ResolveRootIdentity(root string, known func(string) (string, error)) RootIdentity {
	unknown := RootIdentity{Policy: folderlook.RootUnresolved}
	if !filepath.IsAbs(root) {
		return unknown
	}
	root, err := filepath.EvalSymlinks(root)
	if err != nil {
		return unknown
	}
	r, err := os.Stat(root) // #nosec G304 G703 -- trusted workspace anchor, not an HTTP path
	if err != nil || !r.IsDir() {
		return unknown
	}
	policy := folderlook.RootOrdinary
	parts := []string{"folder-root-policy-v2", root}
	for _, candidate := range []struct {
		id     string
		policy folderlook.RootPolicy
	}{{"downloads", folderlook.RootDownloads}, {"desktop", folderlook.RootDesktop}, {"documents", folderlook.RootDocuments}} {
		parts = append(parts, candidate.id)
		path := ""
		if known != nil {
			path, err = known(candidate.id)
		}
		if known == nil || err != nil || !filepath.IsAbs(path) {
			parts = append(parts, "absent")
			continue
		}
		path, err = filepath.EvalSymlinks(path)
		if err != nil {
			parts = append(parts, "absent")
			continue
		}
		d, err := os.Stat(path) // #nosec G304 G703 -- server-known folder resolver
		if err != nil || !d.IsDir() {
			parts = append(parts, "absent")
			continue
		}
		parts = append(parts, path)
		if policy == folderlook.RootOrdinary && os.SameFile(r, d) {
			policy = candidate.policy
		}
	}
	parts = append(parts, string(policy))
	sum := sha256.Sum256([]byte(strings.Join(parts, "\x00")))
	return RootIdentity{Policy: policy, Binding: hex.EncodeToString(sum[:])}
}

func completed(rec *Record) bool {
	return rec.State == StateSetUp && rec.Setup != nil && rec.Setup.Status == personalassistant.FolderSetupDone
}

func (s *Service) rootIdentity(workspaceID string) RootIdentity {
	if s.deps.Workspaces == nil {
		return RootIdentity{Policy: folderlook.RootUnresolved}
	}
	root, err := s.deps.Workspaces.AnchorRoot(workspaceID)
	if err != nil {
		return RootIdentity{Policy: folderlook.RootUnresolved}
	}
	return ResolveRootIdentity(root, s.deps.KnownFolderRoot)
}

// effectiveRecord is a read-only projection, including legacy pending reports.
// Historical completed receipts and the underlying evidence are never rewritten.
func (s *Service) effectiveRecord(rec *Record) *Record {
	if completed(rec) {
		return rec
	}
	copy := *rec
	// Read on the copy only, on every plan build, so the card, the digest it
	// carries and the check just before the run all see the same model.
	if modeler, ok := s.deps.Setups.(HomeHireModeler); ok {
		copy.HomeHireModel = modeler.HomeHireModel()
	}
	identity := s.rootIdentity(rec.WorkspaceID)
	copy.RootPolicy, copy.PolicyBinding = identity.Policy, identity.Binding
	if identity.Policy != folderlook.RootOrdinary {
		copy.Candidates = folderlook.CandidatesForPolicy(identity.Policy, rec.Facts, folderlook.Readiness{})
		copy.Stage = "folder"
		if rec.State == StateSetUp && rec.Setup != nil && rec.Setup.Status == personalassistant.FolderSetupStopped {
			// An incomplete run is not a historical completed receipt. Project
			// current choices rather than presenting old lines as a retry.
			copy.Setup, copy.State = nil, StateReported
		}
		if rec.Sections != nil {
			sections := *rec.Sections
			// Keep factual narration and provenance, not old executable prose or
			// project-specific goal/ticket recommendations for the entire inbox.
			sections.Plan, sections.PlanSteps = "", nil
			sections.Goal, sections.Ticket, sections.TicketWhy, sections.Next = "", "", "", ""
			copy.Sections = &sections
		}
	}
	return &copy
}

// CheckPlan rebinds just before dispatch. Production uses it before any install,
// Home/group mutation, or capability write; the service uses it for fake runners.
func (s *Service) CheckPlan(workspaceID string, plan Plan) error {
	if s == nil || s.deps.Store == nil {
		return ErrUnavailable
	}
	rec, err := s.deps.Store.Read(workspaceID)
	if err != nil {
		return err
	}
	// Even a completed receipt cannot authorize a new direct mutation.
	copy := *rec
	copy.State = StateReported
	current := BuildPlan(*s.effectiveRecord(&copy), plan.Selected)
	if !current.Runnable || !reflect.DeepEqual(current, plan) {
		return personalassistant.ErrFolderPlanChanged
	}
	return nil
}

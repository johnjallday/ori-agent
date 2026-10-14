package projectlibrary

import (
	"context"
	"strings"

	"github.com/johnjallday/ori-agent/internal/llm"
	"github.com/johnjallday/ori-agent/internal/workspacearrangement"
)

// ArrangementRuntime is resolved by the host for the exact local Manager.
// Resolution/revalidation must be read-only: no global-agent fallback, native
// tool materialization, installation, skill binding or provider substitution.
// The fingerprints cover the configured runtime and verified installed guidance.
type ArrangementRuntime struct {
	Provider                                          llm.Provider
	Model, Guidance, ProviderFingerprint, SkillDigest string
	Revalidate                                        func(context.Context) error
}

type ArrangementRuntimeResolver func(context.Context, ManagerAuthority) (ArrangementRuntime, error)

// ArrangementPlanningRequest contains authenticated host context, never model
// tool arguments. Goal is the user's complete current goal, not an approval.
type ArrangementPlanningRequest struct {
	Owner, HomeID, Goal                string
	ExpectedContext                    string
	ExpectedRevision                   int64
	ShareCatalogMetadata, AllowUpgrade bool
}

type ArrangementPlanningResult struct {
	History                   *ArrangementHistory
	ModelCalls, ChargedTokens int
}

// PlanArrangement is an explicit on-demand operation, not a scan subscriber.
// It requires the application's shared Planner and a real host runtime resolver.
// Both proposals and discussion-only replies must commit before a result escapes.
// No library is initialized and no source directory is read by this operation.
func (s *Store) PlanArrangement(ctx context.Context, planner *workspacearrangement.Planner, resolve ArrangementRuntimeResolver, in ArrangementPlanningRequest) (ArrangementPlanningResult, error) {
	fail := ArrangementPlanningResult{}
	if err := ctx.Err(); err != nil {
		return fail, err
	}
	ctx, cancel := context.WithTimeout(ctx, workspacearrangement.TurnTimeout)
	defer cancel()
	if s == nil || s.workspaces == nil || planner == nil || resolve == nil || !in.ShareCatalogMetadata {
		return fail, ErrUnavailable
	}
	if !workspacearrangement.ValidGoal(in.Goal) || in.ExpectedRevision < 1 || in.Owner == "" || !validText(in.Owner, 160) || !collectionLabel(in.HomeID, 160) {
		return fail, ErrCorrupt
	}
	home, err := s.workspaces.Get(in.HomeID)
	if err != nil || home == nil || home.OwnerUserID != in.Owner {
		return fail, ErrUnavailable
	}
	authority, ok := boundHomeManager(home.GetAssistantProgramState(), home)
	if !ok {
		return fail, ErrUnavailable
	}
	scope, err := s.authorizeManager(authority)
	if err != nil || scope.OwnerUserID != in.Owner {
		if err != nil {
			return fail, err
		}
		return fail, ErrUnavailable
	}
	doc, err := s.Read(scope)
	if err != nil {
		return fail, err
	}
	if doc.Revision != in.ExpectedRevision {
		return fail, ErrConflict
	}
	if doc.SchemaVersion == SchemaVersion && !in.AllowUpgrade {
		return fail, ErrArrangementUpgradeRequired
	}
	if len(doc.Arrangements) >= workspacearrangement.MaxContextRecords {
		return fail, ErrLimit
	}
	runtime, err := resolve(ctx, authority)
	if err != nil {
		return fail, err
	}
	if runtime.Revalidate == nil || !workspacearrangement.SupportsMetadataPlanning(runtime.Provider) || strings.TrimSpace(runtime.Model) == "" || !validText(runtime.Model, 512) {
		return fail, ErrNoManagerModel
	}
	if !validDigest(runtime.ProviderFingerprint) || (runtime.SkillDigest != "" && !validDigest(runtime.SkillDigest)) || (runtime.Guidance != "" && runtime.SkillDigest == "") {
		return fail, ErrUnavailable
	}
	if in.ExpectedContext != "" && in.ExpectedContext != arrangementContextPin(authority, doc.Revision, runtime) {
		return fail, workspacearrangement.ErrStale
	}
	check := func(ctx context.Context) error {
		if err := ctx.Err(); err != nil {
			return err
		}
		if err := runtime.Revalidate(ctx); err != nil {
			return err
		}
		current, err := s.authorizeManager(authority)
		if err != nil {
			return err
		}
		if current != scope {
			return workspacearrangement.ErrStale
		}
		latest, err := s.Read(scope)
		if err != nil {
			return err
		}
		if latest.Revision != in.ExpectedRevision {
			return ErrConflict
		}
		return ctx.Err()
	}
	if err := check(ctx); err != nil {
		return fail, err
	}
	evidence, err := workspacearrangement.NewEvidence(ctx, func(ctx context.Context) (workspacearrangement.Snapshot, error) {
		if err := check(ctx); err != nil {
			return workspacearrangement.Snapshot{}, err
		}
		return s.ArrangementEvidence(ctx, in.Owner, authority)
	})
	if err != nil {
		return fail, err
	}
	provenance := workspacearrangement.Provenance{Origin: "model", SourceID: in.HomeID, Provider: runtime.Provider.Name(), Model: runtime.Model, ProviderFingerprint: runtime.ProviderFingerprint, SkillDigest: runtime.SkillDigest}
	var history *ArrangementHistory
	result, err := planner.Run(ctx, workspacearrangement.PlanInput{Owner: in.Owner, ContextKey: "home-" + in.HomeID, Provider: runtime.Provider, Model: runtime.Model, Goal: in.Goal, Guidance: runtime.Guidance, Evidence: evidence, Revalidate: check}, func(ctx context.Context, result workspacearrangement.PlanResult) error {
		var record workspacearrangement.Record
		var err error
		if result.Draft != nil && result.Draft.Goal != in.Goal {
			return workspacearrangement.ErrInvalid
		}
		if result.Draft == nil {
			record, err = evidence.NewClarificationRecord(ctx, in.Goal, provenance)
		} else {
			record, err = evidence.NewRecord(ctx, *result.Draft, provenance)
		}
		if err != nil {
			return err
		}
		if err := check(ctx); err != nil {
			return err
		}
		var saved Arrangement
		if result.Draft == nil {
			saved, _, err = s.SaveArrangementClarification(ctx, in.Owner, authority, in.ExpectedRevision, in.AllowUpgrade, record, result.Answer)
		} else {
			saved, _, err = s.SaveArrangement(ctx, in.Owner, authority, in.ExpectedRevision, in.AllowUpgrade, record)
		}
		if err != nil {
			return err
		}
		history, err = arrangementHistory(saved, saved.CatalogRevision)
		return err
	})
	if err != nil {
		return fail, err
	}
	return ArrangementPlanningResult{History: history, ModelCalls: result.ModelCalls, ChargedTokens: result.ChargedTokens}, nil
}

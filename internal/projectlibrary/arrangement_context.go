package projectlibrary

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"strings"

	"github.com/johnjallday/ori-agent/internal/workspacearrangement"
)

// ArrangementPlanningContext is a read-only disclosure, never approval. ContextPin
// compares the disclosed runtime/catalog with Send; knowing it grants no access.
type ArrangementPlanningContext struct {
	HomeID        string `json:"home_id"`
	Revision      int64  `json:"revision"`
	SchemaVersion int    `json:"schema_version"`
	AgentName     string `json:"agent_name"`
	Provider      string `json:"provider"`
	Model         string `json:"model"`
	ContextPin    string `json:"context_pin"`
}

func arrangementContextPin(authority ManagerAuthority, revision int64, runtime ArrangementRuntime) string {
	raw, _ := json.Marshal(struct {
		Authority             ManagerAuthority
		Revision              int64
		Runtime, Skill, Model string
	}{authority, revision, runtime.ProviderFingerprint, runtime.SkillDigest, runtime.Model})
	sum := sha256.Sum256(append([]byte("arrangement-disclosure-v1\x00"), raw...))
	return hex.EncodeToString(sum[:])
}

// ReadArrangementPlanningContext never initializes/upgrades a library, reads a
// catalog source directory, invokes a model or materializes native tools.
func (s *Store) ReadArrangementPlanningContext(ctx context.Context, owner, homeID string, resolve ArrangementRuntimeResolver) (ArrangementPlanningContext, error) {
	fail := ArrangementPlanningContext{}
	if s == nil || s.workspaces == nil || resolve == nil || owner == "" {
		return fail, ErrUnavailable
	}
	if err := ctx.Err(); err != nil {
		return fail, err
	}
	home, err := s.workspaces.Get(homeID)
	if err != nil || home == nil || home.OwnerUserID != owner {
		return fail, ErrUnavailable
	}
	authority, ok := boundHomeManager(home.GetAssistantProgramState(), home)
	if !ok {
		return fail, ErrUnavailable
	}
	scope, err := s.authorizeManager(authority)
	if err != nil || scope.OwnerUserID != owner {
		return fail, ErrUnavailable
	}
	doc, err := s.Read(scope)
	if err != nil {
		return fail, err
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
	if err := runtime.Revalidate(ctx); err != nil {
		return fail, err
	}
	current, err := s.authorizeManager(authority)
	if err != nil || current != scope {
		return fail, workspacearrangement.ErrStale
	}
	latest, err := s.Read(scope)
	if err != nil {
		return fail, err
	}
	if latest.Revision != doc.Revision {
		return fail, ErrConflict
	}
	if err := ctx.Err(); err != nil {
		return fail, err
	}
	return ArrangementPlanningContext{HomeID: homeID, Revision: doc.Revision, SchemaVersion: doc.SchemaVersion,
		AgentName: authority.AgentName, Provider: runtime.Provider.Name(), Model: runtime.Model,
		ContextPin: arrangementContextPin(authority, doc.Revision, runtime)}, nil
}

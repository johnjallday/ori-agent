package personalassistant

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"

	"github.com/johnjallday/ori-agent/internal/workspacecontinuity"
)

// KnowledgeSidecarPath is the workspace-relative knowledge metadata file. It
// travels as an ordinary folder file; the knowledge component only describes
// it so review and the import report can count remembered items.
const KnowledgeSidecarPath = ".ori/" + knowledgeFileName

// ContinuityKnowledge summarizes a workspace's knowledge metadata. It carries
// counts and owner identity, never remembered text: that stays in the sidecar
// and the canonical MEMORY.md, which are both folder files.
type ContinuityKnowledge struct {
	Version         int    `json:"version"`
	WorkspaceID     string `json:"workspace_id"`
	AssistantID     string `json:"assistant_id"`
	EntryInstanceID string `json:"entry_instance_id"`
	Items           int    `json:"items"`
	Approved        int    `json:"approved"`
	Candidates      int    `json:"candidates"`
	NeedsReview     int    `json:"needs_review"`
	Rejected        int    `json:"rejected"`
	Forgotten       int    `json:"forgotten"`
	Tombstones      int    `json:"tombstones"`
	ProfileTargets  int    `json:"profile_targets"`
	Unfinished      int    `json:"unfinished"`
}

func decodeKnowledgeSidecar(data []byte) (KnowledgeDocument, error) {
	var doc KnowledgeDocument
	if len(data) > knowledgeMaxBytes {
		return doc, workspacecontinuity.ErrLimit
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&doc); err != nil {
		return doc, workspacecontinuity.ErrInvalid
	}
	if err := decoder.Decode(&struct{}{}); err != io.EOF || doc.SchemaVersion != KnowledgeSchemaVersion || doc.Version < 1 {
		return doc, workspacecontinuity.ErrInvalid
	}
	doc.Present = true
	if err := validateKnowledge(doc); err != nil {
		return doc, workspacecontinuity.ErrInvalid
	}
	return doc, nil
}

func summarizeKnowledge(workspaceID string, doc KnowledgeDocument) ContinuityKnowledge {
	summary := ContinuityKnowledge{Version: 1, WorkspaceID: workspaceID, AssistantID: doc.Owner.AssistantID,
		EntryInstanceID: doc.Owner.EntryAgentInstanceID, Items: len(doc.Items), Tombstones: len(doc.Tombstones)}
	for _, item := range doc.Items {
		switch item.State {
		case KnowledgeApproved:
			summary.Approved++
		case KnowledgeCandidate:
			summary.Candidates++
		case KnowledgeNeedsReview:
			summary.NeedsReview++
		case KnowledgeRejected:
			summary.Rejected++
		case KnowledgeForgotten:
			summary.Forgotten++
		}
		if item.Target != nil && item.Target.Kind == "profile" {
			summary.ProfileTargets++
		}
		if item.Prepared != nil {
			summary.Unfinished++
		}
	}
	return summary
}

// CollectContinuityKnowledge describes the workspace's knowledge sidecar, or
// declares the component empty when the workspace has none. A sidecar owned
// by another workspace is reported unavailable rather than guessed at.
func CollectContinuityKnowledge(ctx context.Context, folder, workspaceID string, spool *workspacecontinuity.Spool) error {
	if spool == nil || !workspacecontinuity.ValidID(workspaceID) {
		return workspacecontinuity.ErrInvalid
	}
	data, err := workspacecontinuity.ReadCanonicalFile(ctx, folder, KnowledgeSidecarPath, knowledgeMaxBytes)
	if errors.Is(err, workspacecontinuity.ErrIncomplete) {
		return spool.SetAvailability(ctx, "knowledge", workspacecontinuity.Empty, "")
	}
	if err != nil {
		return err
	}
	doc, err := decodeKnowledgeSidecar(data)
	if err != nil {
		return spool.SetAvailability(ctx, "knowledge", workspacecontinuity.Unavailable, "knowledge_unreadable")
	}
	if doc.Owner.HQWorkspaceID != workspaceID {
		return spool.SetAvailability(ctx, "knowledge", workspacecontinuity.Unavailable, "knowledge_owner_mismatch")
	}
	record, err := workspacecontinuity.EncodeRecord(workspaceID, summarizeKnowledge(workspaceID, doc))
	if err != nil {
		return err
	}
	return spool.AddRecord(ctx, "knowledge", "summaries", record)
}

func DecodeContinuityKnowledge(record workspacecontinuity.Record, owner string) (ContinuityKnowledge, error) {
	var value ContinuityKnowledge
	if err := workspacecontinuity.DecodeRecord(record, &value); err != nil {
		return value, err
	}
	if value.Version != 1 {
		return value, workspacecontinuity.ErrVersion
	}
	if record.ID != owner || value.WorkspaceID != owner || value.Items < 0 || value.Items > knowledgeMaxItems {
		return value, workspacecontinuity.ErrInvalid
	}
	return value, nil
}

// ProjectContinuityKnowledge rebinds an adopted HQ's copied knowledge sidecar
// to this installation's folder name. Forgotten and rejected items and their
// tombstones are kept exactly, so nothing forgotten comes back. An item whose
// value lives in the source's global profile, or that was mid-operation when
// the checkpoint was taken, needs review here: its canonical value did not
// travel, and a copied approval is not local authority.
func ProjectContinuityKnowledge(data []byte, binding KnowledgeBinding) ([]byte, ContinuityKnowledge, error) {
	doc, err := decodeKnowledgeSidecar(data)
	if err != nil {
		return nil, ContinuityKnowledge{}, err
	}
	want := binding.owner()
	source := doc.Owner
	source.HQFolderSlug = want.HQFolderSlug
	if source != want {
		return nil, ContinuityKnowledge{}, workspacecontinuity.ErrConflict
	}
	doc.Owner = want
	for i := range doc.Items {
		item := &doc.Items[i]
		unresolved := item.Prepared != nil || item.Target != nil && item.Target.Kind == "profile"
		item.Prepared = nil
		if unresolved && (item.State == KnowledgeApproved || item.State == KnowledgeCandidate) {
			item.State = KnowledgeNeedsReview
		}
	}
	if doc.Interview != nil {
		// An interrupted profile write is a source-side operation, not a
		// pending action for this installation.
		doc.Interview.ProfileWrite = nil
	}
	out, err := encodeKnowledge(doc)
	if err != nil {
		return nil, ContinuityKnowledge{}, err
	}
	return out, summarizeKnowledge(binding.HQWorkspaceID, doc), nil
}

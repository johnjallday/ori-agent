package sessionhttp

import (
	"bytes"
	"context"
	"encoding/json"
	"image"
	"image/color"
	"image/png"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/johnjallday/ori-agent/internal/agent"
	"github.com/johnjallday/ori-agent/internal/personalassistant"
	"github.com/johnjallday/ori-agent/internal/types"
	agentworkspace "github.com/johnjallday/ori-agent/internal/workspace"
	"github.com/johnjallday/ori-agent/internal/workspacecontinuity"
)

func publishAssistantReviewFixture(t *testing.T, dir, mode string) {
	t.Helper()
	ws := exportedWorkspaceFixture("reviewed-hq", "Copied HQ", "personal_hq")
	ws.OwnerUserID, ws.Version = "local", 8
	ws.AgentInstances = []agentworkspace.AgentInstance{{ID: "entry-1", Name: "Ada", EntryPoint: true}}
	ws.SharedData = map[string]any{"personal_assistant_presentation": map[string]any{"version": 1, "assistant_id": "assistant-1", "request_id": "hq-request"}}
	writeExportedWorkspaceFixture(t, dir, ws)
	canonical, err := workspacecontinuity.ReadCanonicalFile(t.Context(), dir, agentworkspace.WorkspaceConfigFile, workspacecontinuity.MaxChunkBytes)
	if err != nil {
		t.Fatal(err)
	}
	record, err := agentworkspace.SnapshotContinuityWorkspace(canonical)
	if err != nil {
		t.Fatal(err)
	}
	var descriptor agentworkspace.ContinuityWorkspace
	if err := workspacecontinuity.DecodeRecord(record, &descriptor); err != nil {
		t.Fatal(err)
	}
	descriptor.SQLMetadata = &agentworkspace.ContinuityWorkspaceSQLMetadata{Color: ""}
	record, err = workspacecontinuity.EncodeRecord(ws.ID, descriptor)
	if err != nil {
		t.Fatal(err)
	}
	denied := false
	profile := &agent.Agent{Role: types.RoleOrchestrator, Appearance: types.NewAgentAppearance(), Metadata: &types.AgentMetadata{
		Tags: []string{personalassistant.ProfileAssistantMarker("assistant-1"), personalassistant.ProfileHireMarker("hire-request")}},
		WorkspaceLocalConfigID: "opaque-source-slot"}
	profile.Settings.AllowWebSearch = &denied
	profile.Settings.AllowNativeMCPTools = &denied
	profile.Settings.FallbackAllowCloud = &denied
	if mode == "image" || mode == "missing_image" {
		profile.Appearance.Uploaded = &types.UploadedAppearance{Image: "ada.png"}
	}
	profileRecord, err := agentworkspace.SnapshotContinuityProfile(ws, "Ada", profile)
	if err != nil {
		t.Fatal(err)
	}
	if mode == "inline_secret" {
		profile.Settings.APIKey = "synthetic-copied-secret"
	}
	profileBytes, err := json.Marshal(profile)
	if err != nil {
		t.Fatal(err)
	}
	profilePath := filepath.ToSlash(filepath.Join(agentworkspace.WorkspaceAgentsDir, "ada", agentworkspace.WorkspaceAgentConfigFile))
	if err := workspacecontinuity.ReplaceCanonicalFile(t.Context(), dir, profilePath, "", profileBytes); err != nil {
		t.Fatal(err)
	}
	if mode == "extra_asset" {
		if err := workspacecontinuity.ReplaceCanonicalFile(t.Context(), dir, "agents/ada/unclaimed.txt", "", []byte("private unclaimed source file")); err != nil {
			t.Fatal(err)
		}
	}
	at := time.Date(2026, 9, 1, 12, 0, 0, 0, time.UTC)
	agreement := personalassistant.ContinuityAgreement{Version: 1, SourceUserID: "local", AssistantID: "assistant-1", WorkspaceID: ws.ID,
		EntryInstanceID: "entry-1", ProfileName: "Ada", HireRequestID: "hire-request", HQRequestID: "hq-request",
		DisplayName: "Ada", Appearance: profile.Appearance.Clone(), Mandate: "Keep the important work visible.",
		FocusAreas:           []personalassistant.FocusArea{personalassistant.FocusPlanMyDay},
		SpecialistOfferState: personalassistant.SpecialistOfferDeclined, SourceStatus: personalassistant.StatusActive,
		FirstAssignmentStatus: personalassistant.FirstAssignmentNotStarted, SourceStateVersion: 4, HiredAt: at, CreatedAt: at, UpdatedAt: at.Add(time.Hour)}
	if mode == "wrong_entry" {
		agreement.EntryInstanceID = "unrelated-entry"
	}
	agreementRecord, err := workspacecontinuity.EncodeRecord(agreement.AssistantID, agreement)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := personalassistant.DecodeContinuityAgreement(agreementRecord); err != nil {
		t.Fatal("invalid fixture agreement", err)
	}
	objects := map[string][]byte{}
	makeComponent := func(domain, family string, records []workspacecontinuity.Record) workspacecontinuity.Component {
		chunk, ref, err := workspacecontinuity.EncodeChunk(workspacecontinuity.Chunk{Version: 1, WorkspaceID: ws.ID, Domain: domain, Family: family, Records: records})
		if err != nil {
			t.Fatal(err)
		}
		objects[ref.Digest] = chunk
		return workspacecontinuity.Component{Domain: domain, Version: 1, Availability: workspacecontinuity.Present,
			Counts: map[string]int64{family: int64(len(records))}, Chunks: []workspacecontinuity.ChunkRef{ref}}
	}
	manifest := workspacecontinuity.Manifest{Version: 1, Generation: uuid.NewString(), WorkspaceID: ws.ID, CheckpointAt: at,
		SourceRevision: 8, Files: []workspacecontinuity.Fingerprint{
			{Path: profilePath, Digest: workspacecontinuity.Digest(profileBytes), Bytes: int64(len(profileBytes))},
			{Path: agentworkspace.WorkspaceConfigFile, Digest: workspacecontinuity.Digest(canonical), Bytes: int64(len(canonical))},
		}}
	var imageRecord *workspacecontinuity.Record
	var imageBytes []byte
	if mode == "image" || mode == "missing_image" {
		if mode == "image" {
			img := image.NewRGBA(image.Rect(0, 0, 1, 1))
			img.Set(0, 0, color.RGBA{R: 200, A: 255})
			var output bytes.Buffer
			if err := png.Encode(&output, img); err != nil {
				t.Fatal(err)
			}
			imageBytes = output.Bytes()
			if err := workspacecontinuity.ReplaceCanonicalFile(t.Context(), dir, "agents/ada/appearance/ada.png", "", imageBytes); err != nil {
				t.Fatal(err)
			}
			manifest.Files = append(manifest.Files, workspacecontinuity.Fingerprint{Path: "agents/ada/appearance/ada.png", Digest: workspacecontinuity.Digest(imageBytes), Bytes: int64(len(imageBytes))})
		}
		var value agentworkspace.ContinuityProfile
		if err := workspacecontinuity.DecodeRecord(profileRecord, &value); err != nil {
			t.Fatal(err)
		}
		imageRecord, _, err = agentworkspace.SnapshotContinuityAppearance(t.Context(), dir, value)
		if err != nil || imageRecord == nil {
			t.Fatal(err)
		}
	}
	sort.Slice(manifest.Files, func(i, j int) bool { return manifest.Files[i].Path < manifest.Files[j].Path })
	manifest.SourceFingerprint = workspacecontinuity.FilesDigest(manifest.Files)
	for _, domain := range workspacecontinuity.DomainNames() {
		component := workspacecontinuity.Component{Domain: domain, Version: 1, Availability: workspacecontinuity.Empty}
		switch domain {
		case "workspace":
			component = makeComponent(domain, "workspaces", []workspacecontinuity.Record{record})
		case "agents":
			component = makeComponent(domain, "profiles", []workspacecontinuity.Record{profileRecord})
			if imageRecord != nil {
				appearance := makeComponent(domain, "appearances", []workspacecontinuity.Record{*imageRecord})
				component.Chunks = append(component.Chunks, appearance.Chunks...)
				component.Counts["appearances"] = 1
				if len(imageBytes) > 0 {
					ref := workspacecontinuity.BlobRef{Digest: workspacecontinuity.Digest(imageBytes), Bytes: int64(len(imageBytes))}
					component.Blobs = []workspacecontinuity.BlobRef{ref}
					objects[ref.Digest] = imageBytes
				}
			}
		case "assistant":
			if mode != "empty_agreement" {
				component = makeComponent(domain, "agreements", []workspacecontinuity.Record{agreementRecord})
			}
		}
		manifest.Components = append(manifest.Components, component)
	}
	if err := workspacecontinuity.Publish(t.Context(), dir, manifest, func(_ context.Context, digest string) (io.ReadCloser, error) {
		data, ok := objects[digest]
		if !ok {
			return nil, workspacecontinuity.ErrIncomplete
		}
		return io.NopCloser(bytes.NewReader(data)), nil
	}, func(ctx context.Context) error { return ctx.Err() }); err != nil {
		t.Fatal(err)
	}
}

func TestContinuityImportCheckRequiresExactAssistantProfileAndAgreement(t *testing.T) {
	for _, mode := range []string{"valid", "image", "missing_image", "inline_secret", "extra_asset", "wrong_entry", "empty_agreement"} {
		t.Run(mode, func(t *testing.T) {
			h, cleanup := createTestHandler(t)
			defer cleanup()
			root := filepath.Join(t.TempDir(), "copied-hq")
			publishAssistantReviewFixture(t, root, mode)
			code, response := requestImportReview(t, h, root, true, false)
			if code != http.StatusOK {
				t.Fatal(code, response)
			}
			review := response["continuity"].(map[string]any)
			// A native inline key is reviewable: only the denied definition is
			// ever staged (asserted below), so the key never travels.
			if mode != "valid" && mode != "image" && mode != "missing_image" && mode != "inline_secret" {
				if review["status"] != "unavailable" || review["assistant_candidates"] != nil || review["tree_digest"] != nil {
					t.Fatal("unbound identity was offered for review", review)
				}
			} else {
				if review["status"] != "review_required" || review["import_supported"] != false {
					t.Fatal(review)
				}
				candidates, ok := review["assistant_candidates"].([]any)
				if !ok || len(candidates) != 1 {
					t.Fatal("exact assistant omitted", review)
				}
				candidate := candidates[0].(map[string]any)
				appearanceStatus := "no_upload"
				if mode == "image" {
					appearanceStatus = "present"
				}
				if mode == "missing_image" {
					appearanceStatus = "missing"
				}
				if candidate["assistant_id"] != "assistant-1" || candidate["entry_instance_id"] != "entry-1" || candidate["profile_name"] != "Ada" || candidate["appearance_status"] != appearanceStatus {
					t.Fatal("wrong assistant identity", candidate)
				}
				inspected, err := workspacecontinuity.Inspect(t.Context(), root)
				if err != nil {
					t.Fatal(err)
				}
				stage := t.TempDir()
				if err := os.Chmod(stage, 0700); err != nil {
					t.Fatal(err)
				}
				value, err := agentworkspace.StageContinuityProfile(t.Context(), root, stage, inspected, "Ada")
				if err != nil || value.WorkspaceID != "reviewed-hq" {
					t.Fatal("reviewed scoped profile could not stage", err)
				}
				data, err := workspacecontinuity.ReadCanonicalFile(t.Context(), stage, "agents/ada/config.json", workspacecontinuity.MaxRecordBytes)
				if err != nil {
					t.Fatal(err)
				}
				var staged agent.Agent
				if err := json.Unmarshal(data, &staged); err != nil {
					t.Fatal(err)
				}
				if staged.WorkspaceLocalConfigID != "" || staged.Settings.APIKey != "" || staged.Settings.IsWebSearchAllowed() || staged.Settings.IsNativeMCPToolsAllowed() || staged.Metadata == nil {
					t.Fatal("profile staging installed source authority or lost identity")
				}
				if _, err := agentworkspace.StageContinuityProfile(t.Context(), root, stage, inspected, "Ada"); err == nil {
					t.Fatal("staging silently adopted an existing profile")
				}
			}
			encoded, err := json.Marshal(response)
			if err != nil {
				t.Fatal(err)
			}
			if bytes.Contains(encoded, []byte("synthetic-copied-secret")) || bytes.Contains(encoded, []byte("opaque-source-slot")) || bytes.Contains(encoded, []byte("Keep the important work visible")) {
				t.Fatal("review exposed private source agreement/settings")
			}
			post, _ := requestImportReview(t, h, root, false, true)
			if post != http.StatusConflict {
				t.Fatal("legacy import accepted a modern assistant", post)
			}
		})
	}
}

package workspace

import (
	"bytes"
	"encoding/json"
	"errors"
	"image"
	"image/color"
	"image/png"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/johnjallday/ori-agent/internal/agent"
	"github.com/johnjallday/ori-agent/internal/types"
	"github.com/johnjallday/ori-agent/internal/workspacecontinuity"
)

func portableProfileFixture(t *testing.T) (*Workspace, *agent.Agent, workspacecontinuity.Record) {
	t.Helper()
	ws := NewWorkspace(CreateWorkspaceParams{Name: "Scoped", Agents: []string{"Guide"}})
	ws.OwnerUserID = "local"
	profile := syntheticPrivateAgent()
	profile.Settings.SystemPrompt = "User-authored <instructions> remain exact."
	profile.Metadata = &types.AgentMetadata{Description: "Authored description", Tags: []string{"assistant:fixture"}}
	profile.Statistics = types.NewAgentStatistics()
	profile.Statistics.TokenUsage = 8472
	profile.Evolution = types.NewAgentEvolution()
	profile.Evolution.Experience = 99
	profile.WorkspaceLocalConfigID = "local-only-reference"
	record, err := SnapshotContinuityProfile(ws, "Guide", profile)
	localConfigMust(t, err)
	return ws, profile, record
}

func TestVerifyContinuityProfileEvidenceBindsDeniedScopedCanonicalFile(t *testing.T) {
	ws, source, record := portableProfileFixture(t)
	portable, _, err := splitAgentLocalConfig(source)
	localConfigMust(t, err)
	portable.WorkspaceLocalConfigID = "source-local-slot" // opaque source reference, not imported authority
	data, err := json.Marshal(portable)
	localConfigMust(t, err)
	root := t.TempDir()
	path, _, err := localAgentPath("Guide")
	localConfigMust(t, err)
	localConfigMust(t, workspacecontinuity.ReplaceCanonicalFile(t.Context(), root, path, "", data))
	fp := workspacecontinuity.Fingerprint{Path: path, Bytes: int64(len(data)), Digest: workspacecontinuity.Digest(data)}
	_, reconstructed, err := VerifyContinuityProfileEvidence(t.Context(), root, ws, record, fp)
	localConfigMust(t, err)
	if reconstructed.Settings.APIKey != "" || reconstructed.WorkspaceLocalConfigID != "" || reconstructed.Settings.IsNativeMCPToolsAllowed() {
		t.Fatal("review hydrated a copied installation-local slot")
	}
	bad := fp
	bad.Digest = workspacecontinuity.Digest([]byte("other source"))
	if _, _, err := VerifyContinuityProfileEvidence(t.Context(), root, ws, record, bad); !errors.Is(err, workspacecontinuity.ErrChanged) {
		t.Fatal("unreviewed profile bytes were accepted", err)
	}
	badRecord := record
	badRecord.Data = bytes.Replace(record.Data, []byte("Authored description"), []byte("Changed description"), 1)
	if _, _, err := VerifyContinuityProfileEvidence(t.Context(), root, ws, badRecord, fp); !errors.Is(err, workspacecontinuity.ErrChanged) {
		t.Fatal("typed profile did not match its canonical owner", err)
	}
	// A native source file may still hold an agent-specific key. Review reads
	// it, but only the denied definition comes back: the key never reaches
	// this installation.
	withKey := *portable
	withKey.Settings.APIKey = "synthetic-copied-key"
	inline, err := json.Marshal(&withKey)
	localConfigMust(t, err)
	localConfigMust(t, workspacecontinuity.ReplaceCanonicalFile(t.Context(), root, path, fp.Digest, inline))
	fp = workspacecontinuity.Fingerprint{Path: path, Bytes: int64(len(inline)), Digest: workspacecontinuity.Digest(inline)}
	_, reconstructed, err = VerifyContinuityProfileEvidence(t.Context(), root, ws, record, fp)
	localConfigMust(t, err)
	if reconstructed.Settings.APIKey != "" {
		t.Fatal("a copied inline key was hydrated into the reviewed definition")
	}
}

func TestPortableProfilePreservesDefinitionWithoutLocalAuthority(t *testing.T) {
	ws, source, record := portableProfileFixture(t)
	before, err := json.Marshal(source)
	localConfigMust(t, err)
	value, restored, err := DecodeContinuityProfile(record, ws)
	localConfigMust(t, err)
	for _, excluded := range []string{"synthetic-agent-secret", "local-only-reference", "api_key", "allow_native_mcp_tools", "statistics", "evolution", "8472"} {
		if bytes.Contains(record.Data, []byte(excluded)) {
			t.Fatalf("profile contains excluded local field: %s", excluded)
		}
	}
	if restored.Settings.SystemPrompt != source.Settings.SystemPrompt || !reflect.DeepEqual(restored.Metadata, source.Metadata) ||
		!reflect.DeepEqual(restored.Appearance, source.Appearance) || value.WorkspaceID != ws.ID || len(value.InstanceIDs) != 1 {
		t.Fatal("definition or exact workspace-instance ownership changed")
	}
	if restored.Settings.APIKey != "" || restored.WorkspaceLocalConfigID != "" || restored.Settings.IsWebSearchAllowed() ||
		restored.Settings.IsNativeMCPToolsAllowed() || restored.Statistics != nil || restored.Evolution != nil || restored.Status != "" {
		t.Fatal("restored profile contains execution authority or machine state")
	}
	after, err := json.Marshal(source)
	localConfigMust(t, err)
	if !bytes.Equal(before, after) {
		t.Fatal("profile export mutated canonical source")
	}
	// Same spelling elsewhere is not ownership.
	other := NewWorkspace(CreateWorkspaceParams{Name: "Other", Agents: []string{"Guide"}})
	other.OwnerUserID = "local"
	otherRecord, err := SnapshotContinuityProfile(other, "Guide", source)
	localConfigMust(t, err)
	if otherRecord.ID == record.ID {
		t.Fatal("same-name profiles in different workspaces collide in receipt ownership")
	}
	if _, _, err := DecodeContinuityProfile(record, other); err == nil {
		t.Fatal("same-name profile was rebound to a foreign workspace")
	}
}

func TestPortableProfilePreservesManualDisableWithoutRuntimeState(t *testing.T) {
	ws, source, _ := portableProfileFixture(t)
	source.Status = types.AgentStatusDisabled
	record, err := SnapshotContinuityProfile(ws, "Guide", source)
	localConfigMust(t, err)
	value, restored, err := DecodeContinuityProfile(record, ws)
	localConfigMust(t, err)
	if !value.Disabled || restored.Status != types.AgentStatusDisabled || restored.Statistics != nil || restored.Settings.APIKey != "" {
		t.Fatal("manual opt-out was lost or runtime authority was restored")
	}
}

func TestPortableProfileRejectsAmbiguousOrFabricatedEvidence(t *testing.T) {
	ws, source, record := portableProfileFixture(t)
	for _, transform := range []func(string) string{
		func(s string) string { return strings.Replace(s, `"version":1`, `"version":2`, 1) },
		func(s string) string { return strings.Replace(s, `"temperature":0,`, ``, 1) },
		func(s string) string { return strings.Replace(s, `"temperature":0`, `"temperature":null`, 1) },
		func(s string) string {
			return strings.Replace(s, `"settings":{`, `"settings":{"api_key":"foreign",`, 1)
		},
		func(s string) string { return strings.Replace(s, `"role":`, `"Role":`, 1) },
	} {
		changed := transform(string(record.Data))
		if changed == string(record.Data) {
			t.Fatal("malformed evidence fixture did not change")
		}
		bad := record
		bad.Data = []byte(changed)
		if _, _, err := DecodeContinuityProfile(bad, ws); err == nil {
			t.Fatal("malformed profile evidence was accepted")
		}
	}
	ws.AgentInstances = append(ws.AgentInstances, AgentInstance{ID: "different", Name: "guide"})
	if _, err := SnapshotContinuityProfile(ws, "Guide", source); err == nil {
		t.Fatal("two names aliasing one canonical profile path were accepted")
	}
}

func tinyContinuityImage(t *testing.T) []byte {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, 2, 2))
	img.Set(0, 0, color.RGBA{R: 200, A: 255})
	var buffer bytes.Buffer
	localConfigMust(t, png.Encode(&buffer, img))
	return buffer.Bytes()
}

func TestPortableAppearanceCopiesScopedInactiveImageWithoutGlobalFallback(t *testing.T) {
	ws, source, _ := portableProfileFixture(t)
	source.Appearance.Uploaded = &types.UploadedAppearance{Image: "guide.png"}
	// Retained inactive data is part of the user's appearance choices too.
	source.Appearance.Mode = types.AppearanceModeGenerated
	record, err := SnapshotContinuityProfile(ws, "Guide", source)
	localConfigMust(t, err)
	profile, _, err := DecodeContinuityProfile(record, ws)
	localConfigMust(t, err)
	root := t.TempDir()
	missing, data, err := SnapshotContinuityAppearance(t.Context(), root, profile)
	localConfigMust(t, err)
	value, err := DecodeContinuityAppearance(*missing, profile, data)
	localConfigMust(t, err)
	if value.State != "missing" || len(data) != 0 {
		t.Fatal("missing scoped image was replaced or reported empty")
	}
	location, err := profileAppearancePath(profile.Name, "guide.png")
	localConfigMust(t, err)
	imageBytes := tinyContinuityImage(t)
	localConfigMust(t, workspacecontinuity.ReplaceCanonicalFile(t.Context(), root, location, "", imageBytes))
	asset, data, err := SnapshotContinuityAppearance(t.Context(), root, profile)
	localConfigMust(t, err)
	value, err = DecodeContinuityAppearance(*asset, profile, data)
	localConfigMust(t, err)
	if value.State != "present" || !bytes.Equal(imageBytes, data) {
		t.Fatal("scoped appearance bytes changed")
	}
	destination := t.TempDir()
	localConfigMust(t, MaterializeContinuityAppearance(t.Context(), destination, *asset, profile, data))
	copied, err := workspacecontinuity.ReadCanonicalFile(t.Context(), destination, location, maxContinuityAppearanceBytes)
	localConfigMust(t, err)
	if !bytes.Equal(copied, imageBytes) {
		t.Fatal("materialized asset did not preserve exact image bytes")
	}
	if err := MaterializeContinuityAppearance(t.Context(), destination, *asset, profile, data); err == nil {
		t.Fatal("unowned existing asset was silently claimed")
	}
	data[len(data)-1] ^= 1
	if _, err := DecodeContinuityAppearance(*asset, profile, data); !errors.Is(err, workspacecontinuity.ErrDigest) {
		t.Fatal("asset corruption escaped its descriptor")
	}
}

func TestPortableAppearanceRejectsUnsafeImages(t *testing.T) {
	for _, filename := range []string{"../outside.png", "nested/guide.png", "guide.svg", "guide.html", "https:evil.png"} {
		if _, err := profileAppearancePath("Guide", filename); err == nil {
			t.Fatal("unsafe appearance reference accepted")
		}
	}
	if err := validateContinuityImage("guide.png", []byte(`<svg onload="alert(1)"></svg>`)); err == nil {
		t.Fatal("active content disguised as an image was accepted")
	}
	if err := validateContinuityImage("guide.jpg", tinyContinuityImage(t)); err == nil {
		t.Fatal("image type disagreed with filename")
	}
	ws, source, _ := portableProfileFixture(t)
	source.Appearance.Uploaded = &types.UploadedAppearance{Image: "guide.png"}
	record, err := SnapshotContinuityProfile(ws, "Guide", source)
	localConfigMust(t, err)
	profile, _, err := DecodeContinuityProfile(record, ws)
	localConfigMust(t, err)
	root, outside := t.TempDir(), t.TempDir()
	localConfigMust(t, os.WriteFile(filepath.Join(outside, "guide.png"), tinyContinuityImage(t), 0600))
	localConfigMust(t, os.MkdirAll(filepath.Join(root, "agents", "guide", "appearance"), 0750))
	if err := os.Symlink(filepath.Join(outside, "guide.png"), filepath.Join(root, "agents", "guide", "appearance", "guide.png")); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	if _, _, err := SnapshotContinuityAppearance(t.Context(), root, profile); !errors.Is(err, workspacecontinuity.ErrUnsafe) {
		t.Fatalf("image symlink did not fail closed: %v", err)
	}
}

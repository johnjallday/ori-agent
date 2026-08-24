package plugin

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func writeCanonicalSurfaceContribution(t *testing.T, root string) {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("testdata", "workspace-surface-v1", "valid-contribution.json"))
	if err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(root, OriManifestDir, OriManifestFile), string(data))
}

func TestDetectManifestMergesOptionalOriContributionWithMatchingPortableIdentity(t *testing.T) {
	root := t.TempDir()
	writeFile(t, filepath.Join(root, claudeManifestDir, manifestFile), `{"name":"workspace-surface-demo","version":"0.1.0"}`)
	writeCanonicalSurfaceContribution(t, root)

	located, err := DetectManifest(root, "")
	if err != nil {
		t.Fatalf("DetectManifest() error = %v", err)
	}
	if located.contribution == nil || len(located.identities) != 1 {
		t.Fatalf("located manifest = %+v", located)
	}
	descriptor, err := Normalize(located, root)
	if err != nil {
		t.Fatal(err)
	}
	if descriptor.WorkspaceSurfaces == nil || descriptor.WorkspaceSurfaces.Capabilities[0].ID != "demo-tools" {
		t.Fatalf("descriptor contribution = %+v", descriptor.WorkspaceSurfaces)
	}
}

func TestDetectManifestRequiresEveryPresentIdentityToMatchOri(t *testing.T) {
	root := t.TempDir()
	writeFile(t, filepath.Join(root, claudeManifestDir, manifestFile), `{"name":"workspace-surface-demo","version":"0.1.0"}`)
	writeFile(t, filepath.Join(root, codexManifestDir, manifestFile), `{"name":"workspace-surface-demo","version":"0.2.0"}`)
	writeCanonicalSurfaceContribution(t, root)

	if _, err := DetectManifest(root, FormatClaude); !ContributionErrorIs(err, CodeIdentityMismatch) {
		t.Fatalf("identity mismatch error = %v", err)
	}
}

func TestDetectManifestFindsOriContributionBesideVersionedCodexManifest(t *testing.T) {
	root := t.TempDir()
	versionRoot := filepath.Join(root, "0.1.0")
	writeFile(t, filepath.Join(versionRoot, codexManifestDir, manifestFile), `{"name":"workspace-surface-demo","version":"0.1.0"}`)
	writeCanonicalSurfaceContribution(t, versionRoot)

	located, err := DetectManifest(root, FormatCodex)
	if err != nil {
		t.Fatal(err)
	}
	if located.root != versionRoot || located.contribution == nil {
		t.Fatalf("versioned located manifest = %+v", located)
	}
}

func TestMCPAndSkillOnlyPluginJSONRemainsFreeOfSurfaceFields(t *testing.T) {
	root := t.TempDir()
	writeFile(t, filepath.Join(root, claudeManifestDir, manifestFile), `{"name":"portable","version":"1.0.0"}`)
	writeFile(t, filepath.Join(root, ".mcp.json"), `{"echo":{"command":"echo","args":["ok"]}}`)
	writeFile(t, filepath.Join(root, "skills", "portable", "SKILL.md"), "---\nname: portable\n---\n")

	descriptor, err := Load(root, "", "")
	if err != nil {
		t.Fatal(err)
	}
	if descriptor.WorkspaceSurfaces != nil {
		t.Fatalf("surface contribution = %+v, want nil", descriptor.WorkspaceSurfaces)
	}
	data, err := json.Marshal(descriptor)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(data), "workspace_surfaces") {
		t.Fatalf("legacy descriptor JSON changed: %s", data)
	}
	if len(descriptor.MCPServers) != 1 || len(descriptor.Skills) != 1 {
		t.Fatalf("legacy components changed: %+v", descriptor)
	}
}

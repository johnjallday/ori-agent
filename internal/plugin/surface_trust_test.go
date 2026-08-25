package plugin

import (
	"strings"
	"testing"
	"time"
)

func canonicalSurfaceDescriptor(t *testing.T) PluginDescriptor {
	t.Helper()
	contribution, err := ParseSurfaceContribution(canonicalSurfaceFixture(t))
	if err != nil {
		t.Fatal(err)
	}
	return PluginDescriptor{
		Name: "workspace-surface-demo", Version: "0.1.0", SourceFormat: FormatClaude,
		InstallDir: "/managed/plugins/workspace-surface-demo", WorkspaceSurfaces: contribution,
	}
}

func TestBuildTrustReportDisclosesCompleteWorkspaceSurfaceFootprint(t *testing.T) {
	report := BuildTrustReport(canonicalSurfaceDescriptor(t))
	if len(report.SurfaceCapabilities) != 1 || len(report.Surfaces) != 1 || !report.Surfaces[0].BrowserUI {
		t.Fatalf("surface disclosure = %+v", report)
	}
	if len(report.Services) != 1 || report.Services[0].Transport != "mcp_stdio" || !strings.Contains(report.Services[0].Executable, "demo-darwin-arm64") {
		t.Fatalf("service disclosure = %+v", report.Services)
	}
	if len(report.Operations) != 8 || len(report.Artifacts) != 1 || report.Artifacts[0].SHA256 == "" {
		t.Fatalf("operation/artifact disclosure = operations %d artifacts %+v", len(report.Operations), report.Artifacts)
	}
	if len(report.SymbolicScopes) != 1 || report.SymbolicScopes[0] != "plugin_data_write" || len(report.Blueprints) != 1 {
		t.Fatalf("scope/blueprint disclosure = %+v / %+v", report.SymbolicScopes, report.Blueprints)
	}
	rendered := report.String()
	for _, required := range []string{"executable UI code", "trusted local code execution", "confirmation_required", "sha256", "demo-workspace"} {
		if !strings.Contains(rendered, required) {
			t.Fatalf("trust report omits %q:\n%s", required, rendered)
		}
	}
}

func TestTrustedFingerprintChangesForSurfacePolicyAndArtifactFootprint(t *testing.T) {
	original := canonicalSurfaceDescriptor(t)
	changed := canonicalSurfaceDescriptor(t)
	changed.WorkspaceSurfaces.Services[0].Operations[0].Policy = "confirmation_required"
	if trustedComponentFingerprint(original) == trustedComponentFingerprint(changed) {
		t.Fatal("operation policy change did not change trusted fingerprint")
	}
	changed = canonicalSurfaceDescriptor(t)
	changed.WorkspaceSurfaces.Services[0].Artifacts[0].SHA256 = strings.Repeat("b", 64)
	if trustedComponentFingerprint(original) == trustedComponentFingerprint(changed) {
		t.Fatal("artifact digest change did not change trusted fingerprint")
	}
}

func TestInstalledStoreRoundTripsSurfaceTrustAndGeneration(t *testing.T) {
	descriptor := canonicalSurfaceDescriptor(t)
	store := NewStore(t.TempDir())
	installed := InstalledPlugin{
		Name: descriptor.Name, Version: descriptor.Version, Source: "fixture", Format: FormatClaude,
		InstallDir: descriptor.InstallDir, WorkspaceSurfaces: descriptor.WorkspaceSurfaces,
		ComponentFingerprint: trustedComponentFingerprint(descriptor), Generation: 4,
		InstalledAt: time.Now().UTC(),
	}
	if err := store.Put(installed); err != nil {
		t.Fatal(err)
	}
	got, ok, err := store.Get(installed.Name)
	if err != nil || !ok {
		t.Fatalf("Get() = %+v, %v, %v", got, ok, err)
	}
	if got.Generation != 4 || got.ComponentFingerprint != installed.ComponentFingerprint || got.WorkspaceSurfaces == nil || got.WorkspaceSurfaces.Services[0].ID != "demo-service" {
		t.Fatalf("stored plugin lost trusted surface metadata: %+v", got)
	}
}

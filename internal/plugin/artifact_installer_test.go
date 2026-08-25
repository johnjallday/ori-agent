package plugin

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func artifactDescriptor(t *testing.T, content []byte) PluginDescriptor {
	t.Helper()
	descriptor := canonicalSurfaceDescriptor(t)
	descriptor.InstallDir = t.TempDir()
	artifact := &descriptor.WorkspaceSurfaces.Services[0].Artifacts[0]
	artifact.Source = ArtifactSource{Kind: "bundled", Path: "artifacts/demo-service-darwin-arm64"}
	artifact.Size = int64(len(content))
	digest := sha256.Sum256(content)
	artifact.SHA256 = hex.EncodeToString(digest[:])
	path := filepath.Join(descriptor.InstallDir, filepath.FromSlash(artifact.Source.Path))
	if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, content, 0o600); err != nil {
		t.Fatal(err)
	}
	return descriptor
}

func TestArtifactInstallerVerifiesBundledArtifactBeforeExecutablePublish(t *testing.T) {
	descriptor := artifactDescriptor(t, []byte("verified service bytes"))
	installer := NewArtifactInstaller(t.TempDir())
	installer.goos, installer.goarch = "darwin", "arm64"

	resolved, err := installer.Install(context.Background(), descriptor)
	if err != nil {
		t.Fatalf("Install() error = %v", err)
	}
	if len(resolved) != 1 || !resolved[0].Available {
		t.Fatalf("resolved artifacts = %+v", resolved)
	}
	data, err := os.ReadFile(resolved[0].ManagedPath) // #nosec G304 -- path returned by the managed artifact installer
	if err != nil || string(data) != "verified service bytes" {
		t.Fatalf("managed artifact = %q, %v", data, err)
	}
	info, err := os.Stat(resolved[0].ManagedPath)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o700 {
		t.Fatalf("artifact mode = %o, want 0700", info.Mode().Perm())
	}
	if parent, err := os.Stat(filepath.Dir(resolved[0].ManagedPath)); err != nil || parent.Mode().Perm() != 0o750 {
		t.Fatalf("artifact parent mode = %v, %v", parent, err)
	}
}

func TestArtifactInstallerReportsUnsupportedPlatformWithoutLaunchingOrBuilding(t *testing.T) {
	descriptor := artifactDescriptor(t, []byte("darwin only"))
	installer := NewArtifactInstaller(t.TempDir())
	installer.goos, installer.goarch = "linux", "amd64"
	resolved, err := installer.Install(context.Background(), descriptor)
	if err != nil {
		t.Fatal(err)
	}
	if len(resolved) != 1 || resolved[0].Available || resolved[0].Unavailable != "platform_unsupported" || resolved[0].ManagedPath != "" {
		t.Fatalf("unsupported resolution = %+v", resolved)
	}
}

func TestArtifactInstallerRejectsDigestMismatchAndSymlinkSource(t *testing.T) {
	descriptor := artifactDescriptor(t, []byte("actual"))
	descriptor.WorkspaceSurfaces.Services[0].Artifacts[0].SHA256 = strings.Repeat("b", 64)
	installer := NewArtifactInstaller(t.TempDir())
	installer.goos, installer.goarch = "darwin", "arm64"
	if _, err := installer.Install(context.Background(), descriptor); !errors.Is(err, ErrArtifactInvalid) {
		t.Fatalf("digest error = %v", err)
	}

	descriptor = artifactDescriptor(t, []byte("target"))
	artifactPath := filepath.Join(descriptor.InstallDir, filepath.FromSlash(descriptor.WorkspaceSurfaces.Services[0].Artifacts[0].Source.Path))
	if err := os.Remove(artifactPath); err != nil {
		t.Fatal(err)
	}
	outside := filepath.Join(t.TempDir(), "outside")
	if err := os.WriteFile(outside, []byte("target"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, artifactPath); err != nil {
		t.Fatal(err)
	}
	if _, err := installer.Install(context.Background(), descriptor); !errors.Is(err, ErrArtifactInvalid) {
		t.Fatalf("symlink error = %v", err)
	}
}

func TestArtifactInstallerStreamsHTTPSAndRejectsTruncation(t *testing.T) {
	content := []byte("remote verified artifact")
	descriptor := artifactDescriptor(t, content)
	artifact := &descriptor.WorkspaceSurfaces.Services[0].Artifacts[0]
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write(content[:len(content)-3])
	}))
	defer server.Close()
	artifact.Source = ArtifactSource{Kind: "https", URL: server.URL}

	installer := NewArtifactInstaller(t.TempDir())
	installer.goos, installer.goarch = "darwin", "arm64"
	installer.client = server.Client()
	if _, err := installer.Install(context.Background(), descriptor); !errors.Is(err, ErrArtifactInvalid) {
		t.Fatalf("truncation error = %v", err)
	}
}

func TestArtifactInstallerFailedUpdateLeavesPreviousManagedBytes(t *testing.T) {
	pluginsDir := t.TempDir()
	installer := NewArtifactInstaller(pluginsDir)
	installer.goos, installer.goarch = "darwin", "arm64"
	original := artifactDescriptor(t, []byte("original verified bytes"))
	installed, err := installer.Install(context.Background(), original)
	if err != nil {
		t.Fatal(err)
	}
	oldPath := installed[0].ManagedPath

	updated := artifactDescriptor(t, []byte("new bytes"))
	updated.Name = original.Name
	updated.WorkspaceSurfaces.Services[0].Artifacts[0].SHA256 = strings.Repeat("c", 64)
	if _, err := installer.Install(context.Background(), updated); !errors.Is(err, ErrArtifactInvalid) {
		t.Fatalf("failed update error = %v", err)
	}
	file, err := os.Open(oldPath) // #nosec G304 -- prior managed path returned by installer
	if err != nil {
		t.Fatal(err)
	}
	data, readErr := io.ReadAll(file)
	closeErr := file.Close()
	if readErr != nil || closeErr != nil || string(data) != "original verified bytes" {
		t.Fatalf("old artifact after failed update = %q, read=%v close=%v", data, readErr, closeErr)
	}
}

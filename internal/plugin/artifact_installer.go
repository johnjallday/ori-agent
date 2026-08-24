package plugin

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"
)

const maxPluginArtifactBytes int64 = 256 << 20

var (
	ErrArtifactUnsupported = errors.New("plugin artifact is unsupported on this platform")
	ErrArtifactInvalid     = errors.New("plugin artifact verification failed")
)

type ResolvedArtifact struct {
	ServiceID   string `json:"service_id"`
	ArtifactID  string `json:"artifact_id,omitempty"`
	OS          string `json:"os"`
	Arch        string `json:"arch"`
	SHA256      string `json:"sha256,omitempty"`
	Size        int64  `json:"size,omitempty"`
	ManagedPath string `json:"managed_path,omitempty"`
	Available   bool   `json:"available"`
	Unavailable string `json:"unavailable_code,omitempty"`
}

type ArtifactInstaller struct {
	root   string
	client *http.Client
	goos   string
	goarch string
}

func NewArtifactInstaller(pluginsDir string) *ArtifactInstaller {
	client := &http.Client{
		Timeout: 2 * time.Minute,
		CheckRedirect: func(request *http.Request, via []*http.Request) error {
			if len(via) >= 5 || request.URL.Scheme != "https" {
				return errors.New("plugin artifact redirect is not permitted")
			}
			return nil
		},
	}
	return &ArtifactInstaller{
		root: filepath.Join(pluginsDir, "artifacts"), client: client,
		goos: runtime.GOOS, goarch: runtime.GOARCH,
	}
}

// Install verifies every artifact needed on the current platform in a private
// staging directory, applies executable mode only after digest/size success,
// and atomically publishes one fingerprinted directory. An interrupted or
// invalid update cannot replace the previously recorded artifact directory.
func (i *ArtifactInstaller) Install(ctx context.Context, descriptor PluginDescriptor) ([]ResolvedArtifact, error) {
	if descriptor.WorkspaceSurfaces == nil || len(descriptor.WorkspaceSurfaces.Services) == 0 {
		return nil, nil
	}
	if i == nil || strings.TrimSpace(i.root) == "" {
		return nil, fmt.Errorf("%w: installer is unavailable", ErrArtifactInvalid)
	}
	if err := os.MkdirAll(i.root, 0o750); err != nil {
		return nil, fmt.Errorf("%w: create managed root", ErrArtifactInvalid)
	}
	pluginRoot := filepath.Join(i.root, descriptor.Name)
	if err := os.MkdirAll(pluginRoot, 0o750); err != nil {
		return nil, fmt.Errorf("%w: create plugin artifact root", ErrArtifactInvalid)
	}
	fingerprint := trustedComponentFingerprint(descriptor)
	if len(fingerprint) < 16 {
		return nil, fmt.Errorf("%w: trusted fingerprint is unavailable", ErrArtifactInvalid)
	}
	finalRoot := filepath.Join(pluginRoot, fingerprint[:16])
	stagingRoot, err := os.MkdirTemp(pluginRoot, ".staging-")
	if err != nil {
		return nil, fmt.Errorf("%w: create staging directory", ErrArtifactInvalid)
	}
	if err := os.Chmod(stagingRoot, 0o750); err != nil { // #nosec G302 -- private directory requires owner/group traversal
		_ = os.RemoveAll(stagingRoot)
		return nil, fmt.Errorf("%w: secure staging directory", ErrArtifactInvalid)
	}
	published := false
	defer func() {
		if !published {
			_ = os.RemoveAll(stagingRoot)
		}
	}()

	resolved := make([]ResolvedArtifact, 0, len(descriptor.WorkspaceSurfaces.Services))
	for _, service := range descriptor.WorkspaceSurfaces.Services {
		artifact, found := selectArtifact(service.Artifacts, i.goos, i.goarch)
		if !found {
			resolved = append(resolved, ResolvedArtifact{
				ServiceID: service.ID, OS: i.goos, Arch: i.goarch,
				Available: false, Unavailable: "platform_unsupported",
			})
			continue
		}
		serviceDir := filepath.Join(stagingRoot, service.ID)
		if err := os.MkdirAll(serviceDir, 0o750); err != nil {
			return nil, fmt.Errorf("%w: create service artifact directory", ErrArtifactInvalid)
		}
		stagedPath := filepath.Join(serviceDir, artifact.ID)
		if err := i.stageArtifact(ctx, descriptor.InstallDir, artifact, stagedPath); err != nil {
			return nil, err
		}
		resolved = append(resolved, ResolvedArtifact{
			ServiceID: service.ID, ArtifactID: artifact.ID, OS: artifact.OS, Arch: artifact.Arch,
			SHA256: artifact.SHA256, Size: artifact.Size,
			ManagedPath: filepath.Join(finalRoot, service.ID, artifact.ID), Available: true,
		})
	}

	if _, err := os.Stat(finalRoot); err == nil {
		if err := os.RemoveAll(stagingRoot); err != nil {
			return nil, fmt.Errorf("%w: remove duplicate staging directory", ErrArtifactInvalid)
		}
		published = true
		return resolved, nil
	} else if !errors.Is(err, os.ErrNotExist) {
		return nil, fmt.Errorf("%w: inspect managed artifact directory", ErrArtifactInvalid)
	}
	if err := os.Rename(stagingRoot, finalRoot); err != nil {
		return nil, fmt.Errorf("%w: publish verified artifacts", ErrArtifactInvalid)
	}
	published = true
	return resolved, nil
}

func (i *ArtifactInstaller) stageArtifact(ctx context.Context, installDir string, artifact ContributedArtifact, target string) error {
	if artifact.Size < 1 || artifact.Size > maxPluginArtifactBytes {
		return fmt.Errorf("%w: declared artifact size is invalid", ErrArtifactInvalid)
	}
	reader, closeReader, err := i.artifactReader(ctx, installDir, artifact)
	if err != nil {
		return err
	}
	defer closeReader()

	file, err := os.OpenFile(target, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600) // #nosec G304 -- target is a fixed-ID file under a private managed staging directory
	if err != nil {
		return fmt.Errorf("%w: create staged artifact", ErrArtifactInvalid)
	}
	hash := sha256.New()
	written, copyErr := io.Copy(io.MultiWriter(file, hash), io.LimitReader(reader, artifact.Size+1))
	closeErr := file.Close()
	if copyErr != nil || closeErr != nil || written != artifact.Size {
		return fmt.Errorf("%w: artifact download was interrupted or changed size", ErrArtifactInvalid)
	}
	if got := hex.EncodeToString(hash.Sum(nil)); got != artifact.SHA256 {
		return fmt.Errorf("%w: artifact digest mismatch", ErrArtifactInvalid)
	}
	if err := os.Chmod(target, 0o700); err != nil { // #nosec G302 -- verified service artifact must be owner-executable
		return fmt.Errorf("%w: apply executable permission", ErrArtifactInvalid)
	}
	return nil
}

func (i *ArtifactInstaller) artifactReader(ctx context.Context, installDir string, artifact ContributedArtifact) (io.Reader, func(), error) {
	switch artifact.Source.Kind {
	case "bundled":
		file, err := openBundledArtifact(installDir, artifact.Source.Path)
		if err != nil {
			return nil, func() {}, err
		}
		return file, func() { _ = file.Close() }, nil
	case "https":
		parsed, err := url.Parse(artifact.Source.URL)
		if err != nil || parsed.Scheme != "https" || parsed.Host == "" {
			return nil, func() {}, fmt.Errorf("%w: remote source is invalid", ErrArtifactInvalid)
		}
		request, err := http.NewRequestWithContext(ctx, http.MethodGet, parsed.String(), nil)
		if err != nil {
			return nil, func() {}, fmt.Errorf("%w: build artifact request", ErrArtifactInvalid)
		}
		response, err := i.client.Do(request)
		if err != nil {
			return nil, func() {}, fmt.Errorf("%w: download artifact", ErrArtifactInvalid)
		}
		if response.StatusCode != http.StatusOK {
			_ = response.Body.Close()
			return nil, func() {}, fmt.Errorf("%w: artifact server refused download", ErrArtifactInvalid)
		}
		return response.Body, func() { _ = response.Body.Close() }, nil
	default:
		return nil, func() {}, fmt.Errorf("%w: source kind is unsupported", ErrArtifactInvalid)
	}
}

func openBundledArtifact(root, relative string) (*os.File, error) {
	if !filepath.IsAbs(root) || !safeContributionPath(relative) {
		return nil, fmt.Errorf("%w: bundled source path is invalid", ErrArtifactInvalid)
	}
	current := filepath.Clean(root)
	info, err := os.Lstat(current) // #nosec G304 -- installed plugin root selected by the trusted descriptor
	if err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return nil, fmt.Errorf("%w: bundled source root is unsafe", ErrArtifactInvalid)
	}
	for _, part := range strings.Split(filepath.FromSlash(relative), string(filepath.Separator)) {
		current = filepath.Join(current, part)
		info, err = os.Lstat(current) // #nosec G304 -- each fixed manifest component remains under the checked install root
		if err != nil || info.Mode()&os.ModeSymlink != 0 {
			return nil, fmt.Errorf("%w: bundled source is unsafe", ErrArtifactInvalid)
		}
	}
	if !info.Mode().IsRegular() {
		return nil, fmt.Errorf("%w: bundled source is not a regular file", ErrArtifactInvalid)
	}
	file, err := os.Open(current) // #nosec G304 -- exact non-symlink regular artifact beneath the installed plugin root
	if err != nil {
		return nil, fmt.Errorf("%w: open bundled source", ErrArtifactInvalid)
	}
	return file, nil
}

func selectArtifact(artifacts []ContributedArtifact, goos, goarch string) (ContributedArtifact, bool) {
	for _, artifact := range artifacts {
		if strings.EqualFold(artifact.OS, goos) && strings.EqualFold(artifact.Arch, goarch) {
			return artifact, true
		}
	}
	return ContributedArtifact{}, false
}

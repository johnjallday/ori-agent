//go:build darwin

package reapersetup

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"
)

type exchangeSnapshot struct {
	exists bool
	data   []byte
	mode   os.FileMode
}

func (p *platformProbe) verifyProject(ctx context.Context, target VerificationTarget) VerificationObservation {
	if p == nil || p.client == nil || target.WebRemote.State != ProbeReady || target.Runner.State != ProbeReady ||
		target.WebRemote.Port < 1 || target.WebRemote.Port > 65535 || !validRunnerCommandID(target.Runner.CommandID) {
		return VerificationObservation{State: VerificationCheckFailed}
	}
	canonicalRoot, err := canonicalRunnerRoot(target.Runner.Root)
	if err != nil {
		return VerificationObservation{State: VerificationPermissionDenied}
	}
	expected, err := filepath.EvalSymlinks(filepath.Clean(target.ExpectedProject))
	if err != nil || !filepath.IsAbs(expected) || !strings.HasSuffix(strings.ToLower(expected), ".rpp") {
		return VerificationObservation{State: VerificationProjectMissing}
	}

	timeout := target.Timeout
	if timeout <= 0 {
		timeout = 4 * time.Second
	}
	if timeout > 10*time.Second {
		timeout = 10 * time.Second
	}
	verifyCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	p.mu.Lock()
	defer p.mu.Unlock()

	inboxPath := filepath.Join(canonicalRoot, "inbox.lua")
	statusPath := filepath.Join(canonicalRoot, "last_status.txt")
	inbox, err := snapshotExchangeFile(inboxPath, maxVerificationInbox)
	if err != nil {
		return verificationFileFailure(err)
	}
	status, err := snapshotExchangeFile(statusPath, maxProbeResponse)
	if err != nil {
		return verificationFileFailure(err)
	}
	defer restoreExchangeFile(inboxPath, inbox)
	defer restoreExchangeFile(statusPath, status)

	nonce, err := verificationNonce()
	if err != nil {
		return VerificationObservation{State: VerificationCheckFailed}
	}
	responsePath := filepath.Join(canonicalRoot, "verify-"+nonce+".txt")
	if _, err := os.Lstat(responsePath); err == nil || !os.IsNotExist(err) {
		return VerificationObservation{State: VerificationCheckFailed}
	}
	defer func() { _ = os.Remove(responsePath) }()

	_ = os.Remove(statusPath)
	if err := atomicExchangeWrite(canonicalRoot, inboxPath, []byte(trustedVerificationScript(nonce)), 0o600); err != nil {
		return verificationFileFailure(err)
	}

	request, err := http.NewRequestWithContext(verifyCtx, http.MethodGet, loopbackURL(target.WebRemote.Port, target.Runner.CommandID), nil)
	if err != nil {
		return VerificationObservation{State: VerificationCheckFailed}
	}
	response, err := p.client.Do(request)
	if err != nil {
		if errors.Is(verifyCtx.Err(), context.DeadlineExceeded) {
			return VerificationObservation{State: VerificationTimedOut}
		}
		return VerificationObservation{State: VerificationRunnerFailed}
	}
	_, _ = io.Copy(io.Discard, io.LimitReader(response.Body, maxProbeResponse))
	_ = response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return VerificationObservation{State: VerificationRunnerFailed}
	}

	observation := waitVerificationResponse(verifyCtx, responsePath, statusPath, nonce, expected)
	return observation
}

func verificationNonce() (string, error) {
	var value [16]byte
	if _, err := rand.Read(value[:]); err != nil {
		return "", err
	}
	return hex.EncodeToString(value[:]), nil
}

func snapshotExchangeFile(path string, max int64) (exchangeSnapshot, error) {
	info, err := os.Lstat(path) // #nosec G304 -- canonical runner exchange path
	if os.IsNotExist(err) {
		return exchangeSnapshot{}, nil
	}
	if err != nil || info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() || info.Size() > max {
		return exchangeSnapshot{}, ErrRunnerRootUnsafe
	}
	data, err := os.ReadFile(path) // #nosec G304 -- canonical runner exchange path, size checked above
	if err != nil {
		return exchangeSnapshot{}, err
	}
	return exchangeSnapshot{exists: true, data: data, mode: info.Mode().Perm()}, nil
}

func restoreExchangeFile(path string, snapshot exchangeSnapshot) {
	if !snapshot.exists {
		_ = os.Remove(path)
		return
	}
	root := filepath.Dir(path)
	_ = atomicExchangeWrite(root, path, snapshot.data, snapshot.mode)
}

func atomicExchangeWrite(root, destination string, data []byte, mode os.FileMode) error {
	if !pathInsideLexically(destination, root) || filepath.Dir(destination) != filepath.Clean(root) {
		return ErrRunnerRootUnsafe
	}
	temp, err := os.CreateTemp(root, ".ori-verify-*")
	if err != nil {
		return err
	}
	tempPath := temp.Name()
	defer func() { _ = os.Remove(tempPath) }()
	if err := temp.Chmod(mode.Perm()); err != nil {
		_ = temp.Close()
		return err
	}
	if _, err := temp.Write(data); err != nil {
		_ = temp.Close()
		return err
	}
	if err := temp.Sync(); err != nil {
		_ = temp.Close()
		return err
	}
	if err := temp.Close(); err != nil {
		return err
	}
	return os.Rename(tempPath, destination)
}

func waitVerificationResponse(ctx context.Context, responsePath, statusPath, nonce, expected string) VerificationObservation {
	ticker := time.NewTicker(50 * time.Millisecond)
	defer ticker.Stop()
	for {
		observation, complete := readVerificationResponse(responsePath, statusPath, nonce, expected)
		if complete {
			return observation
		}
		select {
		case <-ctx.Done():
			return VerificationObservation{State: VerificationTimedOut}
		case <-ticker.C:
		}
	}
}

func readVerificationResponse(responsePath, statusPath, nonce, expected string) (VerificationObservation, bool) {
	status, statusErr := readBoundedRegularFile(statusPath, maxProbeResponse)
	if statusErr == nil && strings.HasPrefix(strings.TrimSpace(string(status)), "error:") {
		return VerificationObservation{State: VerificationRunnerFailed}, true
	}
	response, responseErr := readBoundedRegularFile(responsePath, maxProbeResponse)
	if responseErr != nil {
		if os.IsNotExist(responseErr) {
			return VerificationObservation{}, false
		}
		return verificationFileFailure(responseErr), true
	}
	lines := strings.Split(string(response), "\n")
	if len(lines) != 3 || strings.TrimSpace(lines[0]) != verificationProtocolVersion || strings.TrimSpace(lines[1]) != nonce {
		return VerificationObservation{State: VerificationMalformed}, true
	}
	current := strings.TrimSpace(lines[2])
	if current == "" {
		return VerificationObservation{State: VerificationProjectMissing}, true
	}
	if !filepath.IsAbs(current) || !strings.HasSuffix(strings.ToLower(current), ".rpp") {
		return VerificationObservation{State: VerificationMalformed}, true
	}
	if !sameProjectPath(current, expected) {
		return VerificationObservation{State: VerificationWrongProject}, true
	}
	if statusErr != nil || strings.TrimSpace(string(status)) != "ok" {
		return VerificationObservation{}, false
	}
	return VerificationObservation{State: VerificationSucceeded}, true
}

func readBoundedRegularFile(path string, max int64) ([]byte, error) {
	info, err := os.Lstat(path) // #nosec G304 -- canonical runner exchange path
	if err != nil {
		return nil, err
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() || info.Size() > max {
		return nil, ErrRunnerRootUnsafe
	}
	return os.ReadFile(path) // #nosec G304 -- canonical runner exchange path, size checked above
}

func verificationFileFailure(err error) VerificationObservation {
	if errors.Is(err, os.ErrPermission) || errors.Is(err, ErrRunnerRootUnsafe) {
		return VerificationObservation{State: VerificationPermissionDenied}
	}
	return VerificationObservation{State: VerificationCheckFailed}
}

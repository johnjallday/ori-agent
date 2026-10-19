//go:build darwin && arm64

package backuprecovery

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"time"

	"github.com/google/uuid"
	"golang.org/x/sys/unix"
)

const timeMachinePreferencesPath = "/Library/Preferences/com.apple.TimeMachine.plist"

// timeMachineExcludeAttribute is the sticky per-item exclusion attribute.
const timeMachineExcludeAttribute = "com.apple.metadata:com_apple_backup_excludeItem"

// timeMachinePlatformSupported reports whether this build can ever read Time Machine.
const timeMachinePlatformSupported = true

// timeMachineVerbs are the only tmutil invocations Ori makes. latestbackup and
// listbackups are excluded: they mount the destination.
var timeMachineVerbs = [2][]string{{"destinationinfo", "-X"}, {"status", "-X"}}

// timeMachineRunProcess runs one fixed verb; tests observe every child spec.
var timeMachineRunProcess = runReadProcess

type timeMachineHelperRequest struct {
	Scratch string   `json:"scratch"`
	Sources []string `json:"sources"`
}

type darwinTimeMachineProvider struct {
	helper *ownBackupHelper
	root   localRoot
}

// newPlatformTimeMachineProvider pins the same validated product build and
// Ori's own helper image as the restic provider, and needs no operator-supplied
// executable: Time Machine is part of the OS.
func newPlatformTimeMachineProvider(ctx context.Context, dataRoot string) (localMetadataProvider, error) {
	if ctx == nil || ctx.Err() != nil {
		return nil, errProviderUnavailable
	}
	root, err := newLocalRoot(dataRoot)
	if err != nil {
		return nil, errLocalState
	}
	versionTool, err := verifySystemExecutable(systemProductMetadata)
	if err != nil {
		return nil, errProviderUnavailable
	}
	defer versionTool.close()
	build, err := runReadProcess(ctx, readProcessSpec{executable: versionTool.path, arguments: []string{"-buildVersion"}, directory: "/", environment: []string{"HOME=" + root.path, "TMPDIR=" + root.path, "PATH=/usr/bin:/bin", "LANG=C", "LC_ALL=C"}, stdoutLimit: 128, timeout: childDrainLimit})
	if err != nil || !bytes.Equal(build, []byte("25D771280a\n")) || !versionTool.unchanged() {
		return nil, errProviderUnavailable
	}
	utility, err := verifySystemExecutable(systemTimeMachineUtility)
	if err != nil {
		return nil, errProviderUnavailable
	}
	utility.close()
	helper, err := verifyOwnBackupHelper(ctx)
	if err != nil {
		return nil, err
	}
	return &darwinTimeMachineProvider{helper: helper, root: root}, nil
}

func (provider *darwinTimeMachineProvider) close() {
	if provider != nil {
		provider.helper.close()
	}
}

// read never uses a credential. Source paths travel only on the helper's
// stdin; the helper reads them as data and never passes one to a command.
func (provider *darwinTimeMachineProvider) read(parent context.Context, configuration localConfiguration, _ backupPasswordUse) (result providerReadResult, err error) {
	if parent == nil || parent.Err() != nil || provider == nil || provider.helper == nil || configuration.Provider != providerTimeMachine || !configuration.valid(false) {
		return providerReadResult{}, errMetadataRead
	}
	ctx, cancel := context.WithTimeout(parent, completeReadLimit)
	defer cancel()
	if !provider.root.check() {
		return providerReadResult{}, errLocalState
	}
	root, err := os.OpenRoot(provider.root.path)
	if err != nil {
		return providerReadResult{}, errLocalState
	}
	defer func() { _ = root.Close() }()
	name := ".backup-read-" + uuid.NewString()
	if root.Mkdir(name, 0700) != nil {
		return providerReadResult{}, errLocalState
	}
	owned, err := root.Lstat(name)
	if err != nil {
		return providerReadResult{}, errLocalState
	}
	defer func() {
		current, checkErr := root.Lstat(name)
		if checkErr != nil || !os.SameFile(owned, current) || current.Mode()&os.ModeSymlink != 0 || root.RemoveAll(name) != nil {
			result = providerReadResult{}
			err = errLocalState
		}
	}()
	scratch := filepath.Join(provider.root.path, name)
	request := timeMachineHelperRequest{Scratch: scratch, Sources: make([]string, 0, len(configuration.Sources))}
	for _, source := range configuration.Sources {
		request.Sources = append(request.Sources, source.Path)
	}
	encoded, err := json.Marshal(request)
	if err != nil || len(encoded) > maxSettingsBytes {
		return providerReadResult{}, errReadProcess
	}
	out, err := provider.helper.run(ctx, "timemachine-v1", "", scratch, encoded, nil)
	if err != nil {
		return providerReadResult{}, err
	}
	defer clear(out)
	answer, err := decodeLocalJSON[timeMachineHelperResult](out, maxPrivateStateBytes)
	if err != nil {
		return providerReadResult{}, errMetadataInvalid
	}
	if ctx.Err() != nil || !provider.root.check() {
		return providerReadResult{}, errReadProcessCancelled
	}
	return timeMachineReadResult(configuration, answer, time.Now().UTC())
}

// runTimeMachineHelper is the helper side of timemachine-v1: the two fixed
// verbs under the Time Machine profile, the preferences file read as data, and
// each source's exclusion attribute read in-process.
func runTimeMachineHelper(ctx context.Context, request timeMachineHelperRequest) (timeMachineHelperResult, error) {
	if !validMetadataPath(request.Scratch) || len(request.Sources) == 0 || len(request.Sources) > maxSelectedSources {
		return timeMachineHelperResult{}, errMetadataRead
	}
	for _, source := range request.Sources {
		if !localPath(source) {
			return timeMachineHelperResult{}, errMetadataRead
		}
	}
	utility, err := verifySystemExecutable(systemTimeMachineUtility)
	if err != nil {
		return timeMachineHelperResult{}, err
	}
	defer utility.close()
	profile, err := timeMachineSandboxProfile(utility.path, request.Sources)
	if err != nil {
		return timeMachineHelperResult{}, err
	}
	environment := []string{"HOME=" + request.Scratch, "TMPDIR=" + request.Scratch, "PATH=/usr/bin:/bin", "LANG=C", "LC_ALL=C", "TZ=UTC"}
	answers := [2][]byte{}
	for i, verb := range timeMachineVerbs {
		answers[i], err = timeMachineRunProcess(ctx, readProcessSpec{executable: utility.path, arguments: append([]string(nil), verb...), environment: environment, directory: "/", stdoutLimit: 256 << 10, timeout: providerReadLimit, pipelineMember: true, profile: profile})
		if err != nil {
			return timeMachineHelperResult{}, err
		}
	}
	if !utility.unchanged() {
		return timeMachineHelperResult{}, errTrustedExecutable
	}
	result := timeMachineHelperResult{DestinationInfo: answers[0], Status: answers[1], Sticky: make([]string, len(request.Sources))}
	file, err := os.Open(timeMachinePreferencesPath)
	switch {
	case errors.Is(err, unix.EPERM):
		result.PreferencesDenied = true // Full Disk Access is missing
	case err != nil:
		return timeMachineHelperResult{}, errMetadataRead
	default:
		result.Preferences, err = io.ReadAll(io.LimitReader(file, maxTimeMachinePlistBytes+1))
		_ = file.Close()
		if err != nil || len(result.Preferences) > maxTimeMachinePlistBytes {
			return timeMachineHelperResult{}, errMetadataRead
		}
	}
	for i, source := range request.Sources {
		result.Sticky[i] = timeMachineStickyState(source, readTimeMachineExcludeAttribute)
	}
	return result, nil
}

// readTimeMachineExcludeAttribute reads one item's exclusion attribute without
// following a link. Absent is not an error.
func readTimeMachineExcludeAttribute(path string) ([]byte, bool, error) {
	buffer := make([]byte, 4096)
	n, err := unix.Lgetxattr(path, timeMachineExcludeAttribute, buffer)
	if errors.Is(err, unix.ENOATTR) {
		return nil, false, nil
	}
	if err != nil || n < 0 || n > len(buffer) {
		return nil, false, errMetadataRead
	}
	return buffer[:n], true, nil
}

//go:build darwin && arm64

package backuprecovery

import (
	"bytes"
	"context"
	"encoding/binary"
	"encoding/json"
	"errors"
	"flag"
	"io"
	"os"
	"reflect"
	"strings"
	"testing"
	"time"
)

func TestBackupCompiledHelperEntry(t *testing.T) {
	if handled, code := RunHelperProcess(flag.Args()); handled {
		os.Exit(code)
	}
}

func TestResticEncryptedBrokerSoftwareHelper(t *testing.T) {
	if os.Getenv("ORI_BACKUP_READER_BROKER_HELPER") != "1" {
		return
	}
	input, err := io.ReadAll(io.LimitReader(os.Stdin, maxSourcePathBytes+1))
	if err != nil || len(input) > maxSourcePathBytes {
		os.Exit(51)
	}
	directory, err := readNativeDirectory(string(input))
	if err != nil {
		os.Exit(52)
	}
	anchor, err := newMetadataAnchor(string(input), directory.identity)
	if err != nil {
		directory.close()
		os.Exit(53)
	}
	bundle, err := anchor.collect(t.Context())
	if err != nil || !directory.unchanged() {
		anchor.close()
		directory.close()
		os.Exit(54)
	}
	encoded, err := encodeMetadataBundle(bundle)
	anchor.close()
	directory.close()
	if err != nil {
		os.Exit(55)
	}
	if _, err := os.Stdout.Write(encoded); err != nil {
		os.Exit(56)
	}
	os.Exit(0)
}

// Test-only stdin protocol. The invented password is a trailing pipe value,
// never a JSON field, argument, environment value, fixture file or log entry.
func TestResticReaderSoftwareHelper(t *testing.T) {
	if os.Getenv("ORI_BACKUP_READER_HELPER") != "1" {
		return
	}
	input, err := io.ReadAll(io.LimitReader(os.Stdin, (16<<10)+4098))
	if err != nil || len(input) < 4 {
		os.Exit(61)
	}
	defer clear(input)
	size := binary.BigEndian.Uint32(input[:4])
	if size > 16<<10 || int(size) > len(input)-4 {
		os.Exit(61)
	}
	var request struct {
		Executable, Repository, Source, Scratch string
		ExternalVolume                          string
		SnapshotCount                           int
		ExpectUnavailable                       bool
	}
	if json.Unmarshal(input[4:4+int(size)], &request) != nil {
		os.Exit(61)
	}
	password := bytes.TrimSuffix(input[4+int(size):], []byte{'\n'})
	ctx, cancel := context.WithTimeout(t.Context(), completeReadLimit)
	defer cancel()
	self, err := os.Executable()
	if err != nil {
		os.Exit(62)
	}
	profile, err := metadataBrokerSandboxProfile(self, []string{request.Source}, request.Repository)
	if err != nil {
		os.Exit(63)
	}
	collect := func() (metadataBundle, error) {
		encoded, err := runReadProcess(ctx, readProcessSpec{executable: self, arguments: []string{"-test.run=^TestResticEncryptedBrokerSoftwareHelper$"}, environment: []string{"HOME=" + request.Scratch, "TMPDIR=" + request.Scratch, "PATH=/usr/bin:/bin", "LANG=C", "LC_ALL=C", "GOMAXPROCS=2", "ORI_DISABLE_NATIVE_SECRET_STORE=1", "ORI_BACKUP_READER_BROKER_HELPER=1"}, directory: "/", input: []byte(request.Repository), stdoutLimit: maxBrokerOutputBytes, timeout: 20 * time.Second, pipelineMember: true, profile: profile})
		if err != nil {
			return metadataBundle{}, err
		}
		defer clear(encoded)
		return decodeMetadataBundle(encoded)
	}
	var bundle metadataBundle
	var result readerHelperResponse
	usePassword := func(ctx context.Context, use func([]byte) error) error { return use(password) }
	if request.ExternalVolume != "" {
		provider, makeErr := newPlatformProvider(ctx, request.Executable, request.Scratch)
		if makeErr != nil {
			_, _ = io.WriteString(os.Stdout, "{\"passed\":false,\"reason\":\""+makeErr.Error()+"\"}\n")
			os.Exit(69)
		}
		defer provider.close()
		provider.(*darwinMetadataProvider).helper.arguments = []string{"-test.run=^TestBackupCompiledHelperEntry$", "--"}
		configuration := localConfiguration{Provider: "restic", Sources: []localSource{{Path: request.Source}}, VolumePath: request.ExternalVolume, RepositoryPath: request.Repository, Credential: localCredential{VaultID: "fixture-vault", RecordID: "fixture-record", Revision: strings.Repeat("a", 64)}}
		var observed providerReadResult
		observed, err = provider.read(ctx, configuration, usePassword)
		if err == nil {
			if !observed.configuration.valid(true) {
				os.Exit(69)
			}
			result = readerHelperResponse{RepositoryID: observed.configuration.RepositoryID, Snapshots: observed.snapshots}
			wrong := observed.configuration
			expected := *wrong.Destination
			if expected.VolumeID[0] == '0' {
				expected.VolumeID = "1" + expected.VolumeID[1:]
			} else {
				expected.VolumeID = "0" + expected.VolumeID[1:]
			}
			wrong.Destination = &expected
			credentialCalled := false
			_, rejectErr := provider.read(ctx, wrong, func(context.Context, func([]byte) error) error { credentialCalled = true; return errReadProcess })
			if !errors.Is(rejectErr, errIdentityMismatch) || credentialCalled {
				os.Exit(70)
			}
		}
	} else {
		bundle, err = collect()
		if err != nil {
			os.Exit(64)
		}
		helper, makeErr := verifyOwnBackupHelper(ctx)
		if makeErr != nil {
			os.Exit(69)
		}
		defer helper.close()
		helper.arguments = []string{"-test.run=^TestBackupCompiledHelperEntry$", "--"}
		framed, frameErr := readerHelperInput(readerHelperRequest{Executable: request.Executable, Scratch: request.Scratch, Sources: []string{request.Source}, Repository: request.Repository}, bundle)
		if frameErr != nil {
			os.Exit(69)
		}
		var raw []byte
		raw, err = helper.run(ctx, "reader-v1", "", request.Scratch, framed, usePassword)
		if err == nil {
			result, err = decodeLocalJSON[readerHelperResponse](raw, maxPrivateStateBytes)
		}
		clear(raw)
	}
	if request.ExpectUnavailable {
		if !errors.Is(err, errCredentialUnavailable) {
			os.Exit(65)
		}
	} else if err != nil || len(result.RepositoryID) != 64 || len(result.Snapshots) != request.SnapshotCount {
		// Only fixed redacted categories, never provider diagnostics/metadata.
		if err != nil {
			_, _ = io.WriteString(os.Stdout, "{\"passed\":false,\"reason\":\""+err.Error()+"\"}\n")
		}
		os.Exit(66)
	}
	if !request.ExpectUnavailable && request.SnapshotCount == 1 {
		snapshot := result.Snapshots[0]
		if !reflect.DeepEqual(snapshot.DeclaredPaths, []string{request.Source}) || !reflect.DeepEqual(snapshot.Exclusions, []string{"excluded.txt"}) || snapshot.CompletedAt == nil || snapshot.FilesProcessed == nil || *snapshot.FilesProcessed != 1 {
			os.Exit(68)
		}
	}
	if request.ExternalVolume == "" {
		after, err := collect()
		if err != nil || !reflect.DeepEqual(after, bundle) {
			os.Exit(67)
		}
	}
	_, _ = io.WriteString(os.Stdout, "{\"passed\":true}\n")
	os.Exit(0)
}

func TestResticReaderHasOnlyTwoFixedOperations(t *testing.T) {
	for _, operation := range []resticReadOperation{resticReadConfig, resticReadSnapshots} {
		arguments, limit, err := fixedResticReadArguments("/private/invented", operation)
		if err != nil || limit < 1 || !reflect.DeepEqual(arguments[:8], []string{"--json", "--repo", "/private/invented/repository", "--password-file", "/dev/fd/0", "--no-lock", "--no-cache", map[resticReadOperation]string{resticReadConfig: "cat", resticReadSnapshots: "snapshots"}[operation]}) {
			t.Fatal("provider operation escaped its fixed arguments")
		}
	}
	for _, operation := range []resticReadOperation{0, 99} {
		if _, _, err := fixedResticReadArguments("/private/invented", operation); err != errReadProcess {
			t.Fatal("unlisted provider operation accepted")
		}
	}
}

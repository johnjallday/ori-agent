//go:build darwin && arm64

package backuprecovery

import (
	"context"
	"testing"
)

func TestSelectedMetadataBindsDirectoryAndLiveMountEvidence(t *testing.T) {
	destination, source := inventedStorageIdentity(true), inventedStorageIdentity(false)
	selection := metadataSelection{Sources: []localSource{{Path: "/Invented Source", DirectoryFileID: 40, Storage: durableStorage(source)}}, VolumePath: "/Invented Volume", RepositoryPath: "/Invented Volume/repository", Destination: durableStorage(destination), RepositoryFileID: 50}
	if !selection.valid() || !storageMatchesLocal(selection.Destination, destination) || storageMatchesLocal(selection.Destination, source) {
		t.Fatal("selection lost persistent storage binding")
	}
	before := selectedMetadata{destination: selectedDirectoryEvidence{storage: destination, fileID: 50}, sources: []selectedDirectoryEvidence{{storage: source, fileID: 40}}}
	after := selectedMetadata{destination: before.destination, sources: append([]selectedDirectoryEvidence(nil), before.sources...)}
	if !before.sameSelection(after) {
		t.Fatal("unchanged selection rejected")
	}
	after.sources[0].fileID++
	if before.sameSelection(after) {
		t.Fatal("same-name source replacement accepted")
	}
	after.sources[0] = before.sources[0]
	after.destination.storage.mountDevice++
	if before.sameSelection(after) {
		t.Fatal("in-flight remount accepted")
	}
	selection.RepositoryPath = "/Invented Volume/../elsewhere"
	if selection.valid() {
		t.Fatal("repository traversal accepted")
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := collectSelectedMetadata(ctx, metadataSelection{}, "/must-not-be-inspected"); err != errStorageIdentity {
		t.Fatal("cancelled invalid selection performed collection")
	}
}

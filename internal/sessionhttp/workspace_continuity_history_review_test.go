package sessionhttp

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/johnjallday/ori-agent/internal/session"
	"github.com/johnjallday/ori-agent/internal/sessionfiles"
	"github.com/johnjallday/ori-agent/internal/workspacecontinuity"
)

func publishReviewHistory(t *testing.T, root string, changed map[string]workspacecontinuity.Component, added map[string][]byte) {
	t.Helper()
	ctx := t.Context()
	inspected, err := workspacecontinuity.Inspect(ctx, root)
	if err != nil {
		t.Fatal(err)
	}
	manifest := inspected.Manifest
	manifest.Generation = uuid.NewString()
	objects := make(map[string][]byte, len(added)+1)
	for _, component := range manifest.Components {
		for _, ref := range component.Chunks {
			data, err := os.ReadFile(filepath.Join(root, workspacecontinuity.Directory, "objects", ref.Digest)) // #nosec G304 -- test-owned checkpoint root; ref is verified by Inspect
			if err != nil {
				t.Fatal(err)
			}
			objects[ref.Digest] = data
		}
	}
	for digest, data := range added {
		objects[digest] = data
	}
	for i := range manifest.Components {
		if component, ok := changed[manifest.Components[i].Domain]; ok {
			manifest.Components[i] = component
		}
	}
	if err := workspacecontinuity.Publish(ctx, root, manifest, func(_ context.Context, digest string) (io.ReadCloser, error) {
		data, ok := objects[digest]
		if !ok {
			return nil, workspacecontinuity.ErrIncomplete
		}
		return io.NopCloser(bytes.NewReader(data)), nil
	}, func(ctx context.Context) error { return ctx.Err() }); err != nil {
		t.Fatal(err)
	}
}

func TestImportReviewRejectsTypedHistoryThatOnlyMatchesItsChunkDigest(t *testing.T) {
	for _, family := range []struct{ domain, name string }{
		{"followups", "followups"}, {"brief_config", "configs"}, {"brief_history", "revisions"}, {"tool_history", "tool_calls"},
	} {
		t.Run(family.domain, func(t *testing.T) {
			h, cleanup := createTestHandler(t)
			defer cleanup()
			root := filepath.Join(t.TempDir(), "synthetic-workspace")
			ws := publishSavedWorkReviewFixture(t, root, "malformed-owner", true)
			data, ref, err := workspacecontinuity.EncodeChunk(workspacecontinuity.Chunk{Version: 1, WorkspaceID: ws.ID, Domain: family.domain, Family: family.name,
				Records: []workspacecontinuity.Record{{ID: "well-formed-id", Data: json.RawMessage(`{"not_a_canonical_record":true}`)}}})
			if err != nil {
				t.Fatal(err)
			}
			component := workspacecontinuity.Component{Domain: family.domain, Version: 1, Availability: workspacecontinuity.Present,
				Counts: map[string]int64{family.name: 1}, Chunks: []workspacecontinuity.ChunkRef{ref}}
			publishReviewHistory(t, root, map[string]workspacecontinuity.Component{family.domain: component}, map[string][]byte{ref.Digest: data})
			if _, err := workspacecontinuity.Inspect(t.Context(), root); err != nil {
				t.Fatal("synthetic checkpoint must pass digest validation", err)
			}
			code, response := requestImportReview(t, h, root, true, false)
			if code != http.StatusOK || response["continuity"].(map[string]any)["status"] != "unavailable" {
				t.Fatal("hash-valid but noncanonical history was advertised as restorable", code, response)
			}
		})
	}
}

func TestImportReviewRefusesValidHashWithOrphanHistoryOrUnclaimedBlob(t *testing.T) {
	h, cleanup := createTestHandler(t)
	defer cleanup()
	root := filepath.Join(t.TempDir(), "synthetic-workspace")
	ws := publishSavedWorkReviewFixture(t, root, "history-review-owner", true)
	at := time.Date(2026, 9, 1, 12, 0, 0, 0, time.UTC)
	sessionRecord, err := session.SnapshotContinuitySession(session.ContinuitySession{Version: 1, WorkspaceID: ws.ID, ID: "session-owned", Title: "Saved", AgentName: "same-name", CreatedAt: at, UpdatedAt: at, MessageCount: 1, Tags: []session.ContinuitySessionTag{}}, ws.ID)
	if err != nil {
		t.Fatal(err)
	}
	message := session.ContinuityMessage{Version: 1, WorkspaceID: ws.ID, SourceSequence: 1, Message: session.Message{ID: "message-owned", SessionID: "session-owned", Role: session.RoleUser, Content: "Private saved work", CreatedAt: at}}
	messageRecord, err := session.SnapshotContinuityMessage(message, ws.ID)
	if err != nil {
		t.Fatal(err)
	}
	sessionChunk, sRef, err := workspacecontinuity.EncodeChunk(workspacecontinuity.Chunk{Version: 1, WorkspaceID: ws.ID, Domain: "sessions", Family: "sessions", Records: []workspacecontinuity.Record{sessionRecord}})
	if err != nil {
		t.Fatal(err)
	}
	messageChunk, mRef, err := workspacecontinuity.EncodeChunk(workspacecontinuity.Chunk{Version: 1, WorkspaceID: ws.ID, Domain: "sessions", Family: "messages", Records: []workspacecontinuity.Record{messageRecord}})
	if err != nil {
		t.Fatal(err)
	}
	sessions := workspacecontinuity.Component{Domain: "sessions", Version: 1, Availability: workspacecontinuity.Present,
		Counts: map[string]int64{"sessions": 1, "messages": 1}, Chunks: []workspacecontinuity.ChunkRef{sRef, mRef}}
	publishReviewHistory(t, root, map[string]workspacecontinuity.Component{"sessions": sessions}, map[string][]byte{sRef.Digest: sessionChunk, mRef.Digest: messageChunk})
	code, response := requestImportReview(t, h, root, true, false)
	if code != http.StatusOK || response["continuity"].(map[string]any)["status"] != "review_required" {
		t.Fatal("valid typed history was rejected", code, response)
	}
	// A digest-valid chunk with a valid DTO pointing to a foreign session is
	// still not restored history. Review refuses it before any receipt/write.
	message.Message.SessionID = "foreign"
	orphan, err := session.SnapshotContinuityMessage(message, ws.ID)
	if err != nil {
		t.Fatal(err)
	}
	orphanChunk, orphanRef, err := workspacecontinuity.EncodeChunk(workspacecontinuity.Chunk{Version: 1, WorkspaceID: ws.ID, Domain: "sessions", Family: "messages", Records: []workspacecontinuity.Record{orphan}})
	if err != nil {
		t.Fatal(err)
	}
	sessions.Chunks = []workspacecontinuity.ChunkRef{sRef, orphanRef}
	publishReviewHistory(t, root, map[string]workspacecontinuity.Component{"sessions": sessions}, map[string][]byte{orphanRef.Digest: orphanChunk})
	code, response = requestImportReview(t, h, root, true, false)
	if code != http.StatusOK || response["continuity"].(map[string]any)["status"] != "unavailable" {
		t.Fatal("foreign session link appeared restorable", code, response)
	}
	code, _ = requestImportReview(t, h, root, false, true)
	if code != http.StatusConflict {
		t.Fatal("legacy duplicate override imported damaged modern history", code)
	}

	// Rebuild valid sessions, then declare an unclaimed copied upload object.
	uploadBytes := []byte("synthetic private file")
	ref := workspacecontinuity.BlobRef{Digest: workspacecontinuity.Digest(uploadBytes), Bytes: int64(len(uploadBytes))}
	upload, err := sessionfiles.SnapshotContinuityUpload(sessionfiles.ContinuityUpload{Version: 1, WorkspaceID: ws.ID, SessionID: "session-owned", State: "copied",
		Entry: sessionfiles.FileEntry{ID: "upload-owned", Name: "attachment.txt", Path: "attachment.txt", Size: int64(len(uploadBytes)), MimeType: "text/plain", Status: sessionfiles.FileStatusOK, AddedAt: at}, Blob: &ref}, ws.ID)
	if err != nil {
		t.Fatal(err)
	}
	uploadChunk, uRef, err := workspacecontinuity.EncodeChunk(workspacecontinuity.Chunk{Version: 1, WorkspaceID: ws.ID, Domain: "uploads", Family: "uploads", Records: []workspacecontinuity.Record{upload}})
	if err != nil {
		t.Fatal(err)
	}
	unusedBytes := []byte("undeclared private content")
	unused := workspacecontinuity.BlobRef{Digest: workspacecontinuity.Digest(unusedBytes), Bytes: int64(len(unusedBytes))}
	uploads := workspacecontinuity.Component{Domain: "uploads", Version: 1, Availability: workspacecontinuity.Present,
		Counts: map[string]int64{"uploads": 1}, Chunks: []workspacecontinuity.ChunkRef{uRef}, Blobs: []workspacecontinuity.BlobRef{ref, unused}}
	sessions.Chunks = []workspacecontinuity.ChunkRef{sRef, mRef}
	verified := uploads
	verified.Blobs = []workspacecontinuity.BlobRef{ref}
	publishReviewHistory(t, root, map[string]workspacecontinuity.Component{"sessions": sessions, "uploads": verified}, map[string][]byte{
		mRef.Digest: messageChunk, uRef.Digest: uploadChunk, ref.Digest: uploadBytes})
	code, response = requestImportReview(t, h, root, true, false)
	if code != http.StatusOK || response["continuity"].(map[string]any)["status"] != "review_required" {
		t.Fatal("declared owned session upload was refused", code, response)
	}
	collision, err := sessionfiles.SnapshotContinuityUpload(sessionfiles.ContinuityUpload{Version: 1, WorkspaceID: ws.ID, SessionID: "session-owned", State: "copied",
		Entry: sessionfiles.FileEntry{ID: "upload-other", Name: "ATTACHMENT.TXT", Path: "ATTACHMENT.TXT", Size: int64(len(uploadBytes)), MimeType: "text/plain", Status: sessionfiles.FileStatusOK, AddedAt: at}, Blob: &ref}, ws.ID)
	if err != nil {
		t.Fatal(err)
	}
	collisionChunk, collisionRef, err := workspacecontinuity.EncodeChunk(workspacecontinuity.Chunk{Version: 1, WorkspaceID: ws.ID, Domain: "uploads", Family: "uploads", Records: []workspacecontinuity.Record{upload, collision}})
	if err != nil {
		t.Fatal(err)
	}
	withCollision := verified
	withCollision.Chunks = []workspacecontinuity.ChunkRef{collisionRef}
	withCollision.Counts = map[string]int64{"uploads": 2}
	publishReviewHistory(t, root, map[string]workspacecontinuity.Component{"uploads": withCollision}, map[string][]byte{collisionRef.Digest: collisionChunk})
	code, response = requestImportReview(t, h, root, true, false)
	if code != http.StatusOK || response["continuity"].(map[string]any)["status"] != "unavailable" {
		t.Fatal("casefold file collision was advertised as restorable", code, response)
	}
	publishReviewHistory(t, root, map[string]workspacecontinuity.Component{"uploads": uploads}, map[string][]byte{uRef.Digest: uploadChunk, unused.Digest: unusedBytes})
	code, response = requestImportReview(t, h, root, true, false)
	if code != http.StatusOK || response["continuity"].(map[string]any)["status"] != "unavailable" {
		t.Fatal("unclaimed upload blob was described as complete", code, response)
	}
}

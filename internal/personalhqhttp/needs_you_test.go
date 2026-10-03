package personalhqhttp

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/johnjallday/ori-agent/internal/emailtriage"
	"github.com/johnjallday/ori-agent/internal/userprofile"
)

type fakeNeedsYou struct {
	list   emailtriage.List
	err    error
	marked []string
}

func (f *fakeNeedsYou) NeedsYou(context.Context, string, string) (emailtriage.List, error) {
	return f.list, f.err
}

func (f *fakeNeedsYou) MarkNeedsYou(_ context.Context, _, _, threadID string, bucket emailtriage.Bucket) error {
	if f.err != nil {
		return f.err
	}
	if threadID == "gone" {
		return emailtriage.ErrUnknownThread
	}
	f.marked = append(f.marked, threadID+"="+string(bucket))
	return nil
}

func (f *fakeNeedsYou) TrackNeedsYou(context.Context, string, string, string) error { return f.err }

func needsYouCall(t *testing.T, handler func(http.ResponseWriter, *http.Request), method, body string) (int, map[string]any) {
	t.Helper()
	var reader *bytes.Buffer
	if body != "" {
		reader = bytes.NewBufferString(body)
	} else {
		reader = &bytes.Buffer{}
	}
	req := httptest.NewRequest(method, "/api/workspaces/ws-1/email/needs-you", reader)
	req.SetPathValue("workspaceID", "ws-1")
	rec := httptest.NewRecorder()
	handler(rec, req)
	var decoded map[string]any
	_ = json.Unmarshal(rec.Body.Bytes(), &decoded)
	return rec.Code, decoded
}

func TestNeedsYouHTTP(t *testing.T) {
	svc := &fakeNeedsYou{list: emailtriage.List{NeedsYou: []emailtriage.Item{{ThreadID: "t1", Subject: "Offsite"}}}}
	handler := NewHandler(nil, nil, nil, userprofile.LocalUserProvider{})
	handler.SetNeedsYouService(svc)

	status, body := needsYouCall(t, handler.WorkspaceNeedsYou, http.MethodGet, "")
	if status != http.StatusOK || body["linked"] != true || body["list"] == nil {
		t.Fatalf("GET = %d %v", status, body)
	}

	status, body = needsYouCall(t, handler.WorkspaceNeedsYouMark, http.MethodPost, `{"thread_id":"t1","bucket":"ignorable"}`)
	if status != http.StatusOK || len(svc.marked) != 1 || svc.marked[0] != "t1=ignorable" {
		t.Fatalf("mark = %d %v, marked %v", status, body, svc.marked)
	}
	if status, body = needsYouCall(t, handler.WorkspaceNeedsYouMark, http.MethodPost, `{"thread_id":"gone","bucket":"ignorable"}`); status != http.StatusNotFound || body["error"] != "unknown_thread" {
		t.Fatalf("unknown thread = %d %v", status, body)
	}
	if status, _ = needsYouCall(t, handler.WorkspaceNeedsYouMark, http.MethodPost, `{"bucket":"ignorable"}`); status != http.StatusBadRequest {
		t.Fatalf("missing thread_id = %d", status)
	}

	// A workspace without a mailbox is an ordinary answer for the list…
	svc.err = ErrNoMailboxLinked
	if status, body = needsYouCall(t, handler.WorkspaceNeedsYou, http.MethodGet, ""); status != http.StatusOK || body["linked"] != false {
		t.Fatalf("unlinked GET = %d %v, want 200 linked=false", status, body)
	}
	// …but acting on it is not.
	if status, body = needsYouCall(t, handler.WorkspaceNeedsYouTrack, http.MethodPost, `{"thread_id":"t1"}`); status != http.StatusConflict || body["error"] != "not_linked" {
		t.Fatalf("unlinked track = %d %v", status, body)
	}

	svc.err = &NeedsYouError{Code: "vault_locked", Message: "Unlock it.", Status: http.StatusConflict}
	if status, body = needsYouCall(t, handler.WorkspaceNeedsYou, http.MethodGet, ""); status != http.StatusConflict || body["error"] != "vault_locked" || body["message"] != "Unlock it." {
		t.Fatalf("locked GET = %d %v", status, body)
	}

	if status, _ = needsYouCall(t, handler.WorkspaceNeedsYou, http.MethodPost, ""); status != http.StatusMethodNotAllowed {
		t.Fatalf("POST list = %d", status)
	}

	unwired := NewHandler(nil, nil, nil, userprofile.LocalUserProvider{})
	if status, body = needsYouCall(t, unwired.WorkspaceNeedsYou, http.MethodGet, ""); status != http.StatusServiceUnavailable || body["error"] != "unavailable" {
		t.Fatalf("unwired GET = %d %v", status, body)
	}
}

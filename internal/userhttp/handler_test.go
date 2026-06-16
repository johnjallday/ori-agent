package userhttp

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/johnjallday/ori-agent/internal/database"
	"github.com/johnjallday/ori-agent/internal/userprofile"
)

func newTestHandler(t *testing.T) (*Handler, userprofile.UserStore) {
	t.Helper()
	db, err := database.Open(context.Background(), &database.Config{InMemory: true, WALMode: false})
	if err != nil {
		t.Fatalf("database.Open: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	store := userprofile.NewSQLiteStore(db)
	return NewHandler(store, userprofile.LocalUserProvider{}), store
}

func TestProfileGetPutRoundTrip(t *testing.T) {
	handler, _ := newTestHandler(t)

	body := bytes.NewBufferString(`{
		"display_name":"Jules",
		"email":"jules@example.com",
		"timezone":"America/New_York",
		"locale":"en-US",
		"role_category":"developer",
		"specializations":["Go","SQLite"],
		"preferences":{"response_style":"concise","units":"metric"},
		"about":"Builds developer tools."
	}`)
	putReq := httptest.NewRequest(http.MethodPut, "/api/user/profile", body)
	putReq.Header.Set("Content-Type", "application/json")
	putRec := httptest.NewRecorder()
	handler.Profile(putRec, putReq)
	if putRec.Code != http.StatusOK {
		t.Fatalf("PUT status = %d body=%s", putRec.Code, putRec.Body.String())
	}

	getReq := httptest.NewRequest(http.MethodGet, "/api/user/profile", nil)
	getRec := httptest.NewRecorder()
	handler.Profile(getRec, getReq)
	if getRec.Code != http.StatusOK {
		t.Fatalf("GET status = %d body=%s", getRec.Code, getRec.Body.String())
	}
	var got struct {
		Profile userprofile.UserProfile `json:"profile"`
	}
	if err := json.Unmarshal(getRec.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode GET response: %v", err)
	}
	if got.Profile.DisplayName != "Jules" || got.Profile.Email != "jules@example.com" || got.Profile.Timezone != "America/New_York" {
		t.Fatalf("identity fields did not round-trip: %#v", got.Profile)
	}
	if got.Profile.Preferences["response_style"] != "concise" {
		t.Fatalf("preferences did not round-trip: %#v", got.Profile.Preferences)
	}
}

func TestProfilePutRejectsUnknownPreference(t *testing.T) {
	handler, _ := newTestHandler(t)

	req := httptest.NewRequest(http.MethodPut, "/api/user/profile", bytes.NewBufferString(`{"preferences":{"tone":"warm"}}`))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	handler.Profile(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 for unknown preference, got %d body=%s", rec.Code, rec.Body.String())
	}
}

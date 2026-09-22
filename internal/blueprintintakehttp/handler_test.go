package blueprintintakehttp

import (
	"bytes"
	"context"
	"encoding/json"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/johnjallday/ori-agent/internal/blueprintintake"
	"github.com/johnjallday/ori-agent/internal/workspace"
)

func newUploadTestHandler(t *testing.T) (*Handler, *workspace.Workspace) {
	t.Helper()
	store, err := workspace.NewFileStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	ws := workspace.NewWorkspace(workspace.CreateWorkspaceParams{Name: "Course"})
	ws.ID = "course-1"
	ws.SetTemplateProvenance(&workspace.TemplateProvenance{IntakeRequirements: []workspace.IntakeRequirement{{
		Key: "materials", Label: "Materials", Skill: "syllabus", Sources: workspace.IntakeSources{Files: true},
		AcceptedExtensions: []string{".txt", ".pdf"}, ProposalKinds: []string{"ticket"},
	}}})
	if err := store.Save(ws); err != nil {
		t.Fatal(err)
	}
	sources := blueprintintake.NewSourceService(store, store)
	sources.SetProviderResolver(func(context.Context, string) (blueprintintake.ModelProvider, error) {
		return blueprintintake.ModelProvider{Name: "openai"}, nil
	})
	return NewHandler(sources, store, nil), ws
}

func TestUploadFiles_ReturnsAStatusForEveryFile(t *testing.T) {
	handler, ws := newUploadTestHandler(t)
	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	files := []struct {
		name string
		data string
	}{
		{name: "notes.txt", data: "Quiz on Friday"},
		{name: "unsupported.exe", data: "ignored"},
		{name: "broken.pdf", data: "not a pdf"},
	}
	for _, file := range files {
		part, err := writer.CreateFormFile("files", file.name)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := part.Write([]byte(file.data)); err != nil {
			t.Fatal(err)
		}
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}

	req := httptest.NewRequest(http.MethodPost, uploadRoute(ws.ID, "materials"), &body)
	req.Header.Set("Content-Type", writer.FormDataContentType())
	req.SetPathValue("workspaceID", ws.ID)
	req.SetPathValue("intakeKey", "materials")
	recorder := httptest.NewRecorder()
	handler.UploadFiles(recorder, req)
	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d: %s", recorder.Code, recorder.Body.String())
	}
	var response struct {
		Files []blueprintintake.FileResult `json:"files"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &response); err != nil {
		t.Fatal(err)
	}
	if len(response.Files) != 3 || response.Files[0].Status != blueprintintake.SourceStatusParsed || response.Files[1].Status != blueprintintake.SourceStatusSkipped || response.Files[2].Status != blueprintintake.SourceStatusUnreadable {
		t.Fatalf("file results = %+v", response.Files)
	}
}

func TestConsentEndpointStoresCurrentUserAcceptance(t *testing.T) {
	handler, ws := newUploadTestHandler(t)
	req := httptest.NewRequest(http.MethodPost, "/consent", nil)
	req.SetPathValue("workspaceID", ws.ID)
	req.SetPathValue("intakeKey", "materials")
	recorder := httptest.NewRecorder()
	handler.AcceptConsent(recorder, req)
	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d: %s", recorder.Code, recorder.Body.String())
	}
	var response struct {
		Consent blueprintintake.ConsentStatus `json:"consent"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &response); err != nil {
		t.Fatal(err)
	}
	if !response.Consent.Accepted || response.Consent.AcceptedBy == "" || response.Consent.Provider != "openai" {
		t.Fatalf("consent = %+v", response.Consent)
	}
}

func TestUploadFiles_RefusesUnknownIntakeBeforeReadingBody(t *testing.T) {
	handler, ws := newUploadTestHandler(t)
	req := httptest.NewRequest(http.MethodPost, uploadRoute(ws.ID, "missing"), bytes.NewBufferString("not multipart"))
	req.SetPathValue("workspaceID", ws.ID)
	req.SetPathValue("intakeKey", "missing")
	recorder := httptest.NewRecorder()
	handler.UploadFiles(recorder, req)
	if recorder.Code != http.StatusNotFound {
		t.Fatalf("status = %d: %s", recorder.Code, recorder.Body.String())
	}
}

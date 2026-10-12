package workspace

import (
	"bytes"
	"encoding/json"
	"errors"
	"log"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"github.com/johnjallday/ori-agent/internal/pathselection"
	"github.com/johnjallday/ori-agent/internal/platform"
)

func TestFolderPickerLaunchErrorResponse_NotFound(t *testing.T) {
	status, message := folderPickerLaunchErrorResponse(errors.Join(errFolderPickerAppNotFound, os.ErrNotExist))
	if status != http.StatusNotFound {
		t.Fatalf("expected 404, got %d", status)
	}
	if message == "" {
		t.Fatal("expected not-found message")
	}
}

func TestFolderPickerLaunchErrorResponse_GenericFailure(t *testing.T) {
	status, message := folderPickerLaunchErrorResponse(errors.New("launch failed"))
	if status != http.StatusInternalServerError {
		t.Fatalf("expected 500, got %d", status)
	}
	if message != "Failed to launch folder picker: launch failed" {
		t.Fatalf("unexpected message: %q", message)
	}
}

// recordFolderPicker replaces the helper's two side effects and counts them.
// No handler test may start or signal a real picker.
func recordFolderPicker(t *testing.T) (shown, launched *int) {
	t.Helper()
	shown, launched = new(int), new(int)
	previousShow, previousLaunch := showFolderPicker, launchFolderPicker
	showFolderPicker = func(string) bool { *shown++; return false }
	launchFolderPicker = func(string) error { *launched++; return errFolderPickerAppNotFound }
	t.Cleanup(func() {
		showFolderPicker, launchFolderPicker = previousShow, previousLaunch
		folderPickerProcess.Lock()
		releaseFolderPickerLocked()
		folderPickerProcess.Unlock()
	})
	return shown, launched
}

func captureLog(t *testing.T) *bytes.Buffer {
	t.Helper()
	var buffer bytes.Buffer
	previous := log.Writer()
	log.SetOutput(&buffer)
	t.Cleanup(func() { log.SetOutput(previous) })
	return &buffer
}

// With desktop launches switched off, neither picker endpoint starts or signals
// the helper: each answers "dialog off" with the sentence the page shows and
// leaves the skipped launch in the log.
func TestFolderPicker_DialogOffStartsNothing(t *testing.T) {
	endpoints := map[string]func(*HTTPHandler) http.HandlerFunc{
		"/api/launch-folder-picker":      func(h *HTTPHandler) http.HandlerFunc { return h.LaunchFolderPicker },
		"/api/folder-picker/select-path": func(h *HTTPHandler) http.HandlerFunc { return h.SelectFolderPath },
	}
	for path, endpoint := range endpoints {
		t.Run(path, func(t *testing.T) {
			t.Setenv(platform.NoDesktopOpenEnv, "1")
			shown, launched := recordFolderPicker(t)
			logged := captureLog(t)

			recorder := httptest.NewRecorder()
			request := httptest.NewRequest(http.MethodPost, path, strings.NewReader(`{"workspace_id":"ws-1","title":"Choose a folder"}`))
			endpoint(&HTTPHandler{})(recorder, request)

			if recorder.Code != http.StatusOK {
				t.Fatalf("status = %d, body = %s", recorder.Code, recorder.Body.String())
			}
			var body map[string]any
			if err := json.Unmarshal(recorder.Body.Bytes(), &body); err != nil {
				t.Fatalf("body = %s: %v", recorder.Body.String(), err)
			}
			for key, want := range map[string]any{
				"success": false, "launched": false, "selected": false,
				"reason": "dialog_off", "note": folderPickerDialogOffNote, "error": folderPickerDialogOffNote,
			} {
				if got, ok := body[key]; !ok || got != want {
					t.Errorf("%s = %#v (present %v), want %#v", key, got, ok, want)
				}
			}
			if strings.Contains(folderPickerDialogOffNote, "_") || !strings.Contains(folderPickerDialogOffNote, "switched off in this session") {
				t.Errorf("note is not a sentence a person reads: %q", folderPickerDialogOffNote)
			}
			if *shown != 0 || *launched != 0 {
				t.Fatalf("picker touched while switched off: shown=%d launched=%d", *shown, *launched)
			}
			folderPickerProcess.Lock()
			claimed := folderPickerProcess.release != nil
			folderPickerProcess.Unlock()
			if claimed {
				t.Fatal("a skipped launch claimed the picker's lifetime")
			}
			if !strings.Contains(logged.String(), "Skipped desktop launch") {
				t.Fatalf("log = %q, want the skipped launch", logged.String())
			}
		})
	}
}

// fakeTypedFolderPaths stands in for the server's typed-path decision.
type fakeTypedFolderPaths struct {
	allowErr error
	typed    TypedFolderPath
	checkErr error
	checked  []string
}

func (f *fakeTypedFolderPaths) Allow(*http.Request) error { return f.allowErr }

func (f *fakeTypedFolderPaths) Check(raw string) (TypedFolderPath, error) {
	f.checked = append(f.checked, raw)
	return f.typed, f.checkErr
}

func selectTypedPath(t *testing.T, handler *HTTPHandler, body string) (*httptest.ResponseRecorder, folderPickerSelectPathResponse) {
	t.Helper()
	recorder := httptest.NewRecorder()
	handler.SelectFolderPath(recorder, httptest.NewRequest(http.MethodPost, "/api/folder-picker/select-path", strings.NewReader(body)))
	var response folderPickerSelectPathResponse
	if err := json.Unmarshal(recorder.Body.Bytes(), &response); err != nil {
		t.Fatalf("body = %s: %v", recorder.Body.String(), err)
	}
	return recorder, response
}

// A typed path the server accepts is answered exactly like a folder chosen in
// the dialog, token included, without the dialog and even while it is off.
func TestSelectFolderPath_TypedPathGetsThePickersAnswer(t *testing.T) {
	for _, switchValue := range []string{"", "1"} {
		t.Run("switch "+switchValue, func(t *testing.T) {
			t.Setenv(platform.NoDesktopOpenEnv, switchValue)
			shown, launched := recordFolderPicker(t)
			logged := captureLog(t)
			selections := pathselection.NewStore()
			checker := &fakeTypedFolderPaths{typed: TypedFolderPath{Path: "/Users/riley/Music/REAPER Projects"}}
			handler := &HTTPHandler{}
			handler.SetTrustedPathSelectionIssuer(selections)
			handler.SetTypedFolderPathChecker(checker)

			// The creator's request: a title and no workspace.
			recorder, response := selectTypedPath(t, handler, `{"title":"Choose the project folder","path":" ~/Music/REAPER Projects "}`)
			if recorder.Code != http.StatusOK || !response.Success || !response.Selected ||
				response.Path != "/Users/riley/Music/REAPER Projects" || response.SelectionToken == "" || response.Error != "" {
				t.Fatalf("status=%d response=%+v", recorder.Code, response)
			}
			if len(checker.checked) != 1 || checker.checked[0] != " ~/Music/REAPER Projects " {
				t.Fatalf("checked = %q, want the typed text handed to the server's check", checker.checked)
			}
			// The token is the one the creator's existing flow resolves.
			if path, err := selections.Resolve(response.SelectionToken); err != nil || path != response.Path {
				t.Fatalf("Resolve = %q, %v", path, err)
			}
			if *shown != 0 || *launched != 0 {
				t.Fatalf("a typed path touched the picker: shown=%d launched=%d", *shown, *launched)
			}
			if strings.Contains(logged.String(), "Skipped desktop launch") {
				t.Fatalf("a typed path is not a skipped launch: %q", logged.String())
			}

			// Named for a workspace, the token is scoped to it like a dialog choice.
			_, scoped := selectTypedPath(t, handler, `{"workspace_id":"ws-1","path":"/Users/riley/Music/REAPER Projects"}`)
			if path, err := selections.ResolveFor(scoped.SelectionToken, "ws-1"); err != nil || path != scoped.Path {
				t.Fatalf("ResolveFor = %q, %v", path, err)
			}
			if _, err := selections.Resolve(scoped.SelectionToken); err == nil {
				t.Fatal("a workspace-scoped typed selection resolved without its scope")
			}
		})
	}
}

func TestSelectFolderPath_TypedPathWarningTravelsWithTheSelection(t *testing.T) {
	recordFolderPicker(t)
	handler := &HTTPHandler{}
	handler.SetTrustedPathSelectionIssuer(pathselection.NewStore())
	handler.SetTypedFolderPathChecker(&fakeTypedFolderPaths{typed: TypedFolderPath{
		Path: "/Volumes/Sessions/Album", Warning: "outside_home", WarningNote: "This folder is outside your home folder.",
	}})
	_, response := selectTypedPath(t, handler, `{"path":"/Volumes/Sessions/Album"}`)
	if !response.Selected || response.SelectionToken == "" || response.Warning != "outside_home" || response.WarningNote != "This folder is outside your home folder." {
		t.Fatalf("response=%+v", response)
	}
}

// A refused typed path issues nothing and says why in the checker's words.
func TestSelectFolderPath_TypedPathRefusals(t *testing.T) {
	cases := []struct {
		name    string
		checker TypedFolderPathChecker
		status  int
		message string
	}{
		{"the server refuses the path", &fakeTypedFolderPaths{checkErr: errors.New("That is a file, not a folder. Choose a folder.")},
			http.StatusUnprocessableEntity, "That is a file, not a folder. Choose a folder."},
		{"the request is not from Ori's page", &fakeTypedFolderPaths{allowErr: errors.New("cross-origin"), typed: TypedFolderPath{Path: "/Users/riley/Music"}},
			http.StatusForbidden, "That request did not come from Ori."},
		{"no check is wired", nil, http.StatusServiceUnavailable, "Typing a folder path is unavailable here. Use the folder dialog instead."},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			shown, launched := recordFolderPicker(t)
			handler := &HTTPHandler{}
			handler.SetTrustedPathSelectionIssuer(pathselection.NewStore())
			if tc.checker != nil {
				handler.SetTypedFolderPathChecker(tc.checker)
			}
			recorder, response := selectTypedPath(t, handler, `{"path":"/Users/riley/Music/notes.txt"}`)
			if recorder.Code != tc.status || response.Error != tc.message {
				t.Fatalf("status=%d error=%q, want %d %q", recorder.Code, response.Error, tc.status, tc.message)
			}
			if response.Success || response.Selected || response.Path != "" || response.SelectionToken != "" {
				t.Fatalf("a refusal still selected something: %+v", response)
			}
			if fake, ok := tc.checker.(*fakeTypedFolderPaths); ok && fake.allowErr != nil && len(fake.checked) != 0 {
				t.Fatal("the path was checked for a request that was not allowed")
			}
			if *shown != 0 || *launched != 0 {
				t.Fatalf("a refused typed path fell back to the picker: shown=%d launched=%d", *shown, *launched)
			}
		})
	}
}

// Without a path the endpoint is the dialog it always was.
func TestSelectFolderPath_BlankPathStillOpensTheDialog(t *testing.T) {
	for _, body := range []string{`{"title":"Choose a folder"}`, `{"title":"Choose a folder","path":"   "}`, ``} {
		t.Run(body, func(t *testing.T) {
			t.Setenv(platform.NoDesktopOpenEnv, "")
			shown, launched := recordFolderPicker(t)
			checker := &fakeTypedFolderPaths{typed: TypedFolderPath{Path: "/Users/riley/Music"}}
			handler := &HTTPHandler{}
			handler.SetTypedFolderPathChecker(checker)
			recorder, response := selectTypedPath(t, handler, body)
			if *shown != 1 || *launched != 1 || len(checker.checked) != 0 {
				t.Fatalf("shown=%d launched=%d checked=%q, want the dialog and no typed check", *shown, *launched, checker.checked)
			}
			if recorder.Code != http.StatusNotFound || response.Selected {
				t.Fatalf("status=%d response=%+v, want the launcher's own not-found answer", recorder.Code, response)
			}
		})
	}
}

// The switch only gates the launch. With it clear the endpoints still reach
// for the helper exactly as before.
func TestFolderPicker_DialogOnStillLaunches(t *testing.T) {
	for _, value := range []string{"", "0", "false"} {
		t.Run("value "+value, func(t *testing.T) {
			t.Setenv(platform.NoDesktopOpenEnv, value)
			shown, launched := recordFolderPicker(t)
			logged := captureLog(t)

			recorder := httptest.NewRecorder()
			request := httptest.NewRequest(http.MethodPost, "/api/launch-folder-picker", strings.NewReader(`{"workspace_id":"ws-1"}`))
			(&HTTPHandler{}).LaunchFolderPicker(recorder, request)

			if *shown != 1 || *launched != 1 {
				t.Fatalf("shown=%d launched=%d, want one attempt each", *shown, *launched)
			}
			if recorder.Code != http.StatusNotFound {
				t.Fatalf("status = %d, want the launcher's own not-found answer", recorder.Code)
			}
			if strings.Contains(logged.String(), "Skipped desktop launch") {
				t.Fatalf("log = %q, nothing was skipped", logged.String())
			}
		})
	}
}

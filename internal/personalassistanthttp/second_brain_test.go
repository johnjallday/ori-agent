package personalassistanthttp

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/johnjallday/ori-agent/internal/connectionshttp"
	"github.com/johnjallday/ori-agent/internal/personalassistant"
	"github.com/johnjallday/ori-agent/internal/secondbrain"
	"github.com/johnjallday/ori-agent/internal/userprofile"
)

// fakeSecondBrain records what reached the service; err answers every call.
type fakeSecondBrain struct {
	calls   []string
	connect personalassistant.SecondBrainConnectInput
	create  personalassistant.SecondBrainCreateInput
	capture [2]string // text, request id
	fillIn  [2]string // decision, request id
	project personalassistant.SecondBrainProjectInput
	skipID  string
	weekID  string
	item    [4]string // look id, item id, action, request id
	accept  personalassistant.SecondBrainAcceptInput
	lookID  string
	version int64
	err     error
}

func (f *fakeSecondBrain) record(call string) error {
	f.calls = append(f.calls, call)
	return f.err
}

func (f *fakeSecondBrain) Status(context.Context, string) (personalassistant.SecondBrainView, error) {
	return personalassistant.SecondBrainView{Status: "not_connected"}, f.record("status")
}

func (f *fakeSecondBrain) MarkPrompted(context.Context, string, string) (personalassistant.SecondBrainView, error) {
	return personalassistant.SecondBrainView{}, f.record("prompted")
}

func (f *fakeSecondBrain) DecideAsk(context.Context, string, string, string) (personalassistant.SecondBrainView, error) {
	return personalassistant.SecondBrainView{}, f.record("ask")
}

func (f *fakeSecondBrain) FillInDecision(_ context.Context, _ string, decision, requestID string) (personalassistant.SecondBrainView, error) {
	f.fillIn = [2]string{decision, requestID}
	return personalassistant.SecondBrainView{}, f.record("fill_in")
}

func (f *fakeSecondBrain) WriteProject(_ context.Context, _ string, input personalassistant.SecondBrainProjectInput) (personalassistant.SecondBrainProjectNote, error) {
	f.project = input
	return personalassistant.SecondBrainProjectNote{Name: input.Name, Rel: "Projects/" + input.Name + "/README.md"}, f.record("project")
}

func (f *fakeSecondBrain) SkipProject(_ context.Context, _ string, requestID string) (personalassistant.SecondBrainView, error) {
	f.skipID = requestID
	return personalassistant.SecondBrainView{}, f.record("project_skip")
}

func (f *fakeSecondBrain) DismissWeek(_ context.Context, _ string, requestID string) (personalassistant.SecondBrainView, error) {
	f.weekID = requestID
	return personalassistant.SecondBrainView{}, f.record("week_dismiss")
}

func (f *fakeSecondBrain) Connect(_ context.Context, _ string, input personalassistant.SecondBrainConnectInput) (personalassistant.SecondBrainView, error) {
	f.connect = input
	return personalassistant.SecondBrainView{LookID: "look-1"}, f.record("connect")
}

func (f *fakeSecondBrain) Create(_ context.Context, _ string, input personalassistant.SecondBrainCreateInput) (personalassistant.SecondBrainView, error) {
	f.create = input
	return personalassistant.SecondBrainView{LookID: "look-1"}, f.record("create")
}

func (f *fakeSecondBrain) Capture(_ context.Context, _ string, text, requestID string) (personalassistant.SecondBrainCaptureView, error) {
	f.capture = [2]string{text, requestID}
	return personalassistant.SecondBrainCaptureView{Note: "Inbox/20261031-1407-note.md", Name: "20261031-1407-note"}, f.record("capture")
}

func (f *fakeSecondBrain) Look(context.Context, string) (personalassistant.SecondBrainLookView, error) {
	return personalassistant.SecondBrainLookView{State: "running"}, f.record("look")
}

func (f *fakeSecondBrain) LookAgain(context.Context, string, string) (personalassistant.SecondBrainLookView, error) {
	return personalassistant.SecondBrainLookView{}, f.record("look_again")
}

func (f *fakeSecondBrain) AcceptFollowUps(_ context.Context, _ string, lookID string, input personalassistant.SecondBrainAcceptInput) (personalassistant.SecondBrainAcceptView, error) {
	f.lookID, f.accept = lookID, input
	return personalassistant.SecondBrainAcceptView{LookID: lookID, Created: 2}, f.record("follow_ups")
}

func (f *fakeSecondBrain) DismissLook(context.Context, string, string, string) (personalassistant.SecondBrainLookView, error) {
	return personalassistant.SecondBrainLookView{}, f.record("dismiss")
}

func (f *fakeSecondBrain) Disconnect(_ context.Context, _ string, _ string, ifVersion int64) (personalassistant.SecondBrainView, error) {
	f.version = ifVersion
	return personalassistant.SecondBrainView{}, f.record("disconnect")
}

func (f *fakeSecondBrain) ActOnItem(_ context.Context, _ string, lookID, itemID, action, requestID string) (personalassistant.SecondBrainLookView, error) {
	f.item = [4]string{lookID, itemID, action, requestID}
	return personalassistant.SecondBrainLookView{State: "finished", LookID: lookID}, f.record("item")
}

func (f *fakeSecondBrain) Inbox(context.Context, string) (personalassistant.SecondBrainInboxView, error) {
	return personalassistant.SecondBrainInboxView{Notes: []personalassistant.SecondBrainInboxNote{{Name: "a.md", Kind: "capture", Title: "A", OwnedByOri: true}}, Total: 1}, f.record("inbox")
}

func newSecondBrainHandler(fake *fakeSecondBrain) http.Handler {
	h := NewHandler(nil, userprofile.LocalUserProvider{})
	h.SetSecondBrain(fake, connectionshttp.NewOriginGuard().Check)
	mux := http.NewServeMux()
	const base = "/api/personal-assistant/second-brain"
	mux.HandleFunc("GET "+base, h.GetSecondBrain)
	mux.HandleFunc("POST "+base+"/prompted", h.PromptedSecondBrain)
	mux.HandleFunc("POST "+base+"/ask", h.AskSecondBrain)
	mux.HandleFunc("POST "+base+"/fill-in", h.FillInSecondBrain)
	mux.HandleFunc("POST "+base+"/project", h.ProjectSecondBrain)
	mux.HandleFunc("POST "+base+"/project/skip", h.SkipProjectSecondBrain)
	mux.HandleFunc("POST "+base+"/week/dismiss", h.DismissWeekSecondBrain)
	mux.HandleFunc("POST "+base+"/connect", h.ConnectSecondBrain)
	mux.HandleFunc("POST "+base+"/create", h.CreateSecondBrain)
	mux.HandleFunc("POST "+base+"/capture", h.CaptureSecondBrain)
	mux.HandleFunc("GET "+base+"/look", h.GetSecondBrainLook)
	mux.HandleFunc("POST "+base+"/look", h.LookSecondBrainAgain)
	mux.HandleFunc("POST "+base+"/look/{lookID}/follow-ups", h.AcceptSecondBrainFollowUps)
	mux.HandleFunc("POST "+base+"/look/{lookID}/dismiss", h.DismissSecondBrainLook)
	mux.HandleFunc("POST "+base+"/look/{lookID}/items/{itemID}", h.ActOnSecondBrainItem)
	mux.HandleFunc("GET "+base+"/inbox", h.GetSecondBrainInbox)
	mux.HandleFunc("POST "+base+"/disconnect", h.DisconnectSecondBrain)
	return mux
}

func secondBrainRequest(handler http.Handler, method, path, body, origin string) *httptest.ResponseRecorder {
	request := httptest.NewRequest(method, "http://localhost:8931/api/personal-assistant/second-brain"+path, strings.NewReader(body))
	request.Host = "localhost:8931"
	if origin != "" {
		request.Header.Set("Origin", origin)
	}
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, request)
	return recorder
}

func TestSecondBrainRoutes_RefuseAForeignOriginBeforeAnythingElse(t *testing.T) {
	fake := &fakeSecondBrain{}
	handler := newSecondBrainHandler(fake)
	for _, route := range []struct{ method, path, body string }{
		{http.MethodGet, "", ""},
		{http.MethodPost, "/prompted", `{"request_id":"p-1"}`},
		{http.MethodPost, "/ask", `{"decision":"later","request_id":"a-1"}`},
		{http.MethodPost, "/fill-in", `{"decision":"allow","request_id":"fi-1"}`},
		{http.MethodPost, "/project", `{"name":"Launch","answer":"Finishing the Launch album.","request_id":"pj-1"}`},
		{http.MethodPost, "/project/skip", `{"request_id":"ps-1"}`},
		{http.MethodPost, "/week/dismiss", `{"request_id":"wk-1"}`},
		{http.MethodPost, "/connect", `{"detected_id":"v_1","request_id":"c-1"}`},
		{http.MethodPost, "/create", `{"selection_token":"t-1","name":"Second Brain","request_id":"s-1"}`},
		{http.MethodPost, "/capture", `{"text":"Call Dana","request_id":"n-1"}`},
		{http.MethodGet, "/look", ""},
		{http.MethodPost, "/look", `{"request_id":"l-1"}`},
		{http.MethodPost, "/look/look-1/follow-ups", `{"items":"suggested","request_id":"f-1"}`},
		{http.MethodPost, "/look/look-1/dismiss", `{"request_id":"x-1"}`},
		{http.MethodPost, "/look/look-1/items/item-1", `{"action":"done","request_id":"i-1"}`},
		{http.MethodGet, "/inbox", ""},
		{http.MethodPost, "/disconnect", `{"request_id":"d-1","if_version":1}`},
	} {
		if got := secondBrainRequest(handler, route.method, route.path, route.body, "https://evil.example").Code; got != http.StatusForbidden {
			t.Errorf("%s %s from a foreign origin = %d, want 403", route.method, route.path, got)
		}
		if got := secondBrainRequest(handler, route.method, route.path, route.body, "http://localhost:8931").Code; got != http.StatusOK {
			t.Errorf("%s %s from Ori's page = %d, want 200", route.method, route.path, got)
		}
	}
	if len(fake.calls) != 17 {
		t.Fatalf("calls = %v, want only the seventeen local requests", fake.calls)
	}

	rebound := httptest.NewRequest(http.MethodGet, "http://attacker.example/api/personal-assistant/second-brain", nil)
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, rebound)
	if recorder.Code != http.StatusForbidden {
		t.Fatalf("a rebound host = %d, want 403", recorder.Code)
	}
}

func TestSecondBrainItemRoute_TakesAClosedBodyAndMapsEachRefusal(t *testing.T) {
	fake := &fakeSecondBrain{}
	handler := newSecondBrainHandler(fake)
	const origin = "http://localhost:8931"
	response := secondBrainRequest(handler, http.MethodPost, "/look/look-1/items/item-1", `{"action":" done ","request_id":" i-1 "}`, origin)
	if response.Code != http.StatusOK || fake.item != [4]string{"look-1", "item-1", "done", "i-1"} {
		t.Fatalf("code = %d, reached the service with %v", response.Code, fake.item)
	}
	if !strings.Contains(response.Body.String(), `"look"`) {
		t.Fatalf("body = %s", response.Body)
	}

	calls := len(fake.calls)
	if got := secondBrainRequest(handler, http.MethodPost, "/look/look-1/items/item-1", `{"action":"done","request_id":"i-2","path":"/etc/passwd"}`, origin).Code; got != http.StatusBadRequest {
		t.Fatalf("an unknown field = %d, want 400", got)
	}
	if len(fake.calls) != calls {
		t.Fatal("a refused body reached the service")
	}

	for name, tc := range map[string]struct {
		err  error
		want int
	}{
		"unknown look":       {personalassistant.ErrSecondBrainLookNotFound, http.StatusNotFound},
		"unknown item":       {personalassistant.ErrSecondBrainItemNotFound, http.StatusNotFound},
		"seeded follow-up":   {personalassistant.ErrSecondBrainSeededItem, http.StatusConflict},
		"bad action":         {personalassistant.ErrValidation, http.StatusBadRequest},
		"id used for others": {secondbrain.ErrRequestReused, http.StatusConflict},
		"nothing connected":  {personalassistant.ErrSecondBrainNotConnected, http.StatusConflict},
	} {
		t.Run(name, func(t *testing.T) {
			fake.err = tc.err
			if got := secondBrainRequest(handler, http.MethodPost, "/look/look-1/items/item-1", `{"action":"done","request_id":"i-3"}`, origin).Code; got != tc.want {
				t.Fatalf("code = %d, want %d", got, tc.want)
			}
		})
	}
}

func TestSecondBrainInboxRoute_ListsAndIsAbsentForAConnectedVault(t *testing.T) {
	fake := &fakeSecondBrain{}
	handler := newSecondBrainHandler(fake)
	const origin = "http://localhost:8931"
	response := secondBrainRequest(handler, http.MethodGet, "/inbox", "", origin)
	if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), `"owned_by_ori":true`) {
		t.Fatalf("inbox = %d %s", response.Code, response.Body)
	}
	fake.err = personalassistant.ErrSecondBrainNoInbox
	if got := secondBrainRequest(handler, http.MethodGet, "/inbox", "", origin).Code; got != http.StatusNotFound {
		t.Fatalf("a connected vault's Inbox = %d, want 404", got)
	}
}

func TestSecondBrainRoutes_RefuseUnknownFieldsAndAnyPath(t *testing.T) {
	for _, route := range []struct{ path, body string }{
		{"/prompted", `{"request_id":"p-1","path":"/Users/sam/Notes"}`},
		{"/prompted", `{"request_id":"p-1","extra":true}`},
		{"/ask", `{"decision":"later","request_id":"a-1","folder":"Notes"}`},
		{"/fill-in", `{"decision":"allow","request_id":"fi-1","path":"/Users/sam/Second Brain"}`},
		{"/fill-in", `{"decision":"allow","request_id":"fi-1","folders":["People"]}`},
		{"/fill-in", `{"decision":"allow"}{"decision":"later"}`},
		{"/project", `{"name":"Launch","answer":"x","request_id":"pj-1","path":"/Users/sam/Second Brain/Projects/Launch"}`},
		{"/project", `{"name":"Launch","answer":"x","request_id":"pj-1","folder":"Music"}`},
		{"/project/skip", `{"request_id":"ps-1","name":"Launch"}`},
		{"/week/dismiss", `{"request_id":"wk-1","counts":{"facts":99}}`},
		{"/connect", `{"path":"/Users/sam/Notes","request_id":"c-1"}`},
		{"/connect", `{"detected_id":"v_1","request_id":"c-1","vault_path":"/Users/sam/Notes"}`},
		{"/connect", `{"detected_id":"v_1","request_id":"c-1","name":"Notes"}`},
		{"/look", `{"request_id":"l-1","path":"/"}`},
		{"/look/look-1/follow-ups", `{"items":"suggested","request_id":"f-1","note_path":"Inbox.md"}`},
		{"/disconnect", `{"request_id":"d-1","if_version":1,"force":true}`},
		{"/create", `{"selection_token":"t-1","name":"Brain","request_id":"s-1","path":"/Users/sam/Documents"}`},
		{"/create", `{"selection_token":"t-1","name":"Brain","request_id":"s-1","parent_folder":"/Users/sam"}`},
		{"/create", `{"selection_token":"t-1","name":"Brain","request_id":"s-1","origin":"connected"}`},
		{"/connect", `{"detected_id":"v_1","request_id":"c-1"}{"detected_id":"v_2"}`},
		{"/connect", `["detected_id"]`},
	} {
		fake := &fakeSecondBrain{}
		response := secondBrainRequest(newSecondBrainHandler(fake), http.MethodPost, route.path, route.body, "http://localhost:8931")
		if response.Code != http.StatusBadRequest || len(fake.calls) != 0 {
			t.Errorf("POST %s %s = %d (calls %v), want 400 before the service", route.path, route.body, response.Code, fake.calls)
		}
		if strings.Contains(route.body, "path") && !strings.Contains(response.Body.String(), "does not accept a folder path") {
			t.Errorf("POST %s with a path key said %s", route.path, response.Body.String())
		}
	}
}

func TestSecondBrainConnect_TakesExactlyOneWayToNameTheFolder(t *testing.T) {
	for body, want := range map[string]int{
		`{"request_id":"c-1"}`: http.StatusBadRequest,
		`{"detected_id":"v_1","selection_token":"t","request_id":"c-1"}`:  http.StatusBadRequest,
		`{"detected_id":"v_1","request_id":"c-1"}`:                        http.StatusOK,
		`{"selection_token":"token-1","request_id":"c-1","replace":true}`: http.StatusOK,
	} {
		fake := &fakeSecondBrain{}
		if got := secondBrainRequest(newSecondBrainHandler(fake), http.MethodPost, "/connect", body, "").Code; got != want {
			t.Errorf("connect %s = %d, want %d", body, got, want)
		}
		if want == http.StatusOK && strings.Contains(body, "token-1") && (!fake.connect.Replace || fake.connect.SelectionToken != "token-1") {
			t.Errorf("connect input = %+v", fake.connect)
		}
	}
}

func TestSecondBrainCreate_TakesATokenAndANameNeverAPath(t *testing.T) {
	for body, want := range map[string]int{
		`{"selection_token":"token-1","name":"My Brain","request_id":"s-1"}`:                http.StatusOK,
		`{"selection_token":" token-1 ","name":" My Brain ","request_id":"s-1"}`:            http.StatusOK,
		`{"selection_token":"token-1","name":"My Brain","request_id":"s-1","path":"/tmp"}`:  http.StatusBadRequest,
		`{"selection_token":"token-1","name":"My Brain","request_id":"s-1","replace":true}`: http.StatusBadRequest,
	} {
		fake := &fakeSecondBrain{}
		response := secondBrainRequest(newSecondBrainHandler(fake), http.MethodPost, "/create", body, "")
		if response.Code != want {
			t.Errorf("create %s = %d, want %d", body, response.Code, want)
			continue
		}
		if want == http.StatusOK {
			if fake.create.SelectionToken != "token-1" || fake.create.Name != "My Brain" || fake.create.RequestID != "s-1" {
				t.Errorf("create input = %+v", fake.create)
			}
			if !strings.Contains(response.Body.String(), `"look_id":"look-1"`) {
				t.Errorf("create body = %s", response.Body.String())
			}
		} else if len(fake.calls) != 0 {
			t.Errorf("create %s reached the service", body)
		}
	}
}

// No token asks for the default place; the service decides whether there is one.
func TestSecondBrainCreate_WithoutATokenAsksTheServiceForTheDefaultPlace(t *testing.T) {
	for _, body := range []string{`{"request_id":"s-1"}`, `{"selection_token":"  ","request_id":"s-1"}`} {
		fake := &fakeSecondBrain{}
		response := secondBrainRequest(newSecondBrainHandler(fake), http.MethodPost, "/create", body, "")
		if response.Code != http.StatusOK {
			t.Errorf("create %s = %d", body, response.Code)
			continue
		}
		if fake.create.SelectionToken != "" || fake.create.RequestID != "s-1" {
			t.Errorf("create input = %+v", fake.create)
		}
	}
}

func TestSecondBrainCapture_TakesTextAndARequestIDNeverAPath(t *testing.T) {
	fake := &fakeSecondBrain{}
	handler := newSecondBrainHandler(fake)
	response := secondBrainRequest(handler, http.MethodPost, "/capture", `{"text":" Call Dana ","request_id":" n-1 "}`, "")
	if response.Code != http.StatusOK || fake.capture != [2]string{" Call Dana ", "n-1"} ||
		!strings.Contains(response.Body.String(), `"note":"Inbox/20261031-1407-note.md"`) {
		t.Fatalf("capture = %d %s, reached the service as %q", response.Code, response.Body.String(), fake.capture)
	}
	for _, body := range []string{
		`{"text":"Call Dana","request_id":"n-1","path":"Inbox/x.md"}`,
		`{"text":"Call Dana","request_id":"n-1","folder":"Projects"}`,
		`{"text":"Call Dana","request_id":"n-1","kind":"journal"}`,
		`{"text":["Call Dana"],"request_id":"n-1"}`,
	} {
		fake := &fakeSecondBrain{}
		if got := secondBrainRequest(newSecondBrainHandler(fake), http.MethodPost, "/capture", body, "").Code; got != http.StatusBadRequest || len(fake.calls) != 0 {
			t.Errorf("capture %s = %d (calls %v), want 400 before the service", body, got, fake.calls)
		}
	}
	big := `{"text":"` + strings.Repeat("a", 30*1024) + `","request_id":"n-1"}`
	if got := secondBrainRequest(newSecondBrainHandler(&fakeSecondBrain{}), http.MethodPost, "/capture", big, "").Code; got != http.StatusBadRequest {
		t.Errorf("a body over the cap = %d, want 400", got)
	}
}

func TestSecondBrainFollowUps_TakeSuggestedOrAListOfIDs(t *testing.T) {
	cases := []struct {
		body      string
		want      int
		suggested bool
		ids       int
	}{
		{body: `{"items":"suggested","request_id":"f-1"}`, want: http.StatusOK, suggested: true},
		{body: `{"items":["a","b"],"request_id":"f-1"}`, want: http.StatusOK, ids: 2},
		{body: `{"items":"everything","request_id":"f-1"}`, want: http.StatusBadRequest},
		{body: `{"items":[],"request_id":"f-1"}`, want: http.StatusBadRequest},
		{body: `{"items":[1,2],"request_id":"f-1"}`, want: http.StatusBadRequest},
		{body: `{"request_id":"f-1"}`, want: http.StatusBadRequest},
	}
	for _, tc := range cases {
		fake := &fakeSecondBrain{}
		response := secondBrainRequest(newSecondBrainHandler(fake), http.MethodPost, "/look/look-7/follow-ups", tc.body, "")
		if response.Code != tc.want {
			t.Errorf("%s = %d, want %d", tc.body, response.Code, tc.want)
			continue
		}
		if tc.want == http.StatusOK && (fake.lookID != "look-7" || fake.accept.Suggested != tc.suggested || len(fake.accept.ItemIDs) != tc.ids) {
			t.Errorf("%s reached the service as %q %+v", tc.body, fake.lookID, fake.accept)
		}
	}
	many := make([]string, secondbrain.MaxLookItems+1)
	for i := range many {
		many[i] = strings.Repeat("a", 64)
	}
	data, _ := json.Marshal(map[string]any{"items": many, "request_id": "f-1"})
	if got := secondBrainRequest(newSecondBrainHandler(&fakeSecondBrain{}), http.MethodPost, "/look/look-7/follow-ups", string(data), "").Code; got != http.StatusBadRequest {
		t.Errorf("more ids than a look keeps = %d, want 400", got)
	}
}

func TestSecondBrainDisconnect_RequiresIfVersion(t *testing.T) {
	fake := &fakeSecondBrain{}
	handler := newSecondBrainHandler(fake)
	if got := secondBrainRequest(handler, http.MethodPost, "/disconnect", `{"request_id":"d-1"}`, "").Code; got != http.StatusBadRequest {
		t.Fatalf("without if_version = %d", got)
	}
	if got := secondBrainRequest(handler, http.MethodPost, "/disconnect", `{"request_id":"d-1","if_version":0}`, "").Code; got != http.StatusOK || fake.version != 0 {
		t.Fatalf("if_version 0 = %d, version %d", got, fake.version)
	}
}

func TestSecondBrainErrors_SayOneSentenceEach(t *testing.T) {
	cases := []struct {
		err    error
		status int
		key    string
	}{
		{&personalassistant.SecondBrainFolderError{Message: "No Markdown notes found in Brain.", NoNotes: true}, http.StatusBadRequest, `"no_notes":true`},
		{&personalassistant.SecondBrainTooSoonError{RetryAfter: 90 * time.Second}, http.StatusTooManyRequests, `"retry_after_seconds":90`},
		{secondbrain.ErrReplaceNeedsConfirm, http.StatusConflict, `"needs_confirm":true`},
		{personalassistant.ErrSecondBrainSelection, http.StatusConflict, `"needs_pick_reason":"lost"`},
		{personalassistant.ErrSecondBrainUnknownVault, http.StatusNotFound, "Obsidian's list"},
		{personalassistant.ErrSecondBrainNotConnected, http.StatusConflict, "No notes folder"},
		{personalassistant.ErrSecondBrainAlreadyConnected, http.StatusConflict, "Disconnect it before"},
		{secondbrain.ErrNotOwned, http.StatusConflict, "only writes notes to a second brain it set up"},
		{secondbrain.ErrCaptureSecretLike, http.StatusBadRequest, "looks like a secret"},
		{secondbrain.ErrCaptureInvalid, http.StatusBadRequest, "1 to 4,000 characters"},
		{secondbrain.ErrUserModified, http.StatusConflict, "could not write that note"},
		{&personalassistant.SecondBrainFolderError{Message: "Brain already has something in it. Choose another name or location."}, http.StatusBadRequest, `"repair":"choose_folder"`},
		{personalassistant.ErrNeedsHQ, http.StatusConflict, "Personal HQ"},
		{secondbrain.ErrRequestReused, http.StatusConflict, "request id"},
		{errors.New("disk on fire"), http.StatusServiceUnavailable, "temporarily unavailable"},
	}
	for _, tc := range cases {
		fake := &fakeSecondBrain{err: tc.err}
		response := secondBrainRequest(newSecondBrainHandler(fake), http.MethodPost, "/connect", `{"detected_id":"v_1","request_id":"c-1"}`, "")
		if response.Code != tc.status || !strings.Contains(response.Body.String(), tc.key) {
			t.Errorf("%v → %d %s, want %d with %s", tc.err, response.Code, response.Body.String(), tc.status, tc.key)
		}
		if strings.Contains(response.Body.String(), "disk on fire") {
			t.Errorf("an internal error leaked: %s", response.Body.String())
		}
	}
}

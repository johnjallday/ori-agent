package sessionhttp

import (
	"context"
	"net/http"
	"strings"
	"testing"

	"github.com/johnjallday/ori-agent/internal/personalassistant"
)

// fakeHomeProvider is the reviewed-release service: release is the version it
// would install now; onInstall runs after a confirmed install.
type fakeHomeProvider struct {
	release   string
	err       error
	previews  []string
	installs  []string
	onInstall func()
}

func (p *fakeHomeProvider) PreviewHomeProvider(_ context.Context, key string) (BuildHomeProvider, error) {
	p.previews = append(p.previews, key)
	if p.err != nil {
		return BuildHomeProvider{}, p.err
	}
	return BuildHomeProvider{
		PluginID: "music-project-management", Version: p.release, Source: "https://github.com/example/music@abc",
		Disclosure: map[string]any{"name": "music-project-management"},
	}, nil
}

func (p *fakeHomeProvider) InstallHomeProvider(_ context.Context, key, version string) (BuildHomeProvider, error) {
	p.installs = append(p.installs, key+"@"+version)
	if p.err != nil {
		return BuildHomeProvider{}, p.err
	}
	if version != p.release {
		return BuildHomeProvider{}, ErrBuildProviderChanged
	}
	if p.onInstall != nil {
		p.onInstall()
	}
	return BuildHomeProvider{PluginID: "music-project-management", Version: version, Installed: true, Ready: true}, nil
}

// waitingPlan is a recognized song folder's plan while its Home's plugin is
// not installed.
func waitingPlan() *personalassistant.BuildFolderPlan {
	return &personalassistant.BuildFolderPlan{
		Lines: []personalassistant.FolderPlanLine{}, Unavailable: personalassistant.FolderAvailabilityProviderNotInstalled,
		UnavailableSubject: "Music Project Management",
	}
}

func homeProviderOf(t *testing.T, body map[string]any) map[string]any {
	t.Helper()
	provider, _ := body["home_provider"].(map[string]any)
	if provider == nil {
		t.Fatalf("no home_provider in %v", body)
	}
	return provider
}

func installProviderOf(body map[string]any) map[string]any {
	session, _ := body["session"].(map[string]any)
	offer, _ := session["install_provider"].(map[string]any)
	return offer
}

func (f *buildFixture) homeProvider(t *testing.T, session personalassistant.WorkspaceBuildSession, body map[string]any) (int, map[string]any) {
	t.Helper()
	body["version"] = session.Version
	return f.do(t, http.MethodPost, "/api/workspaces/build-sessions/"+session.ID+"/home-provider", body)
}

// PRD user story 2: a song folder whose Home's plugin is missing installs it
// from the build — the reviewed release is disclosed first, only that release
// installs, and then the plan Set it up runs is there.
func TestWorkspaceBuildHomeProvider_InstallsOnlyTheReleaseTheUserReviewedThenThePlanIsReady(t *testing.T) {
	f := newBuildFixture(t, true)
	folders := &fakeBuildFolders{observation: reaperObservation(), recognition: reaperRecognition(), plan: waitingPlan()}
	f.withFolders(t, folders)
	provider := &fakeHomeProvider{release: "0.3.0", onInstall: func() { folders.plan = songPlan() }}
	f.handler.workspaceBuild.HomeProvider = provider
	build := f.start(t)
	_, body := f.attachFolder(t, build, map[string]any{"mode": "chip", "chip": "documents"})
	session := sessionOf(t, body)
	if plan := planOf(t, body); plan["ready"] == true || plan["unavailable"] != personalassistant.FolderAvailabilityProviderNotInstalled {
		t.Fatalf("the plan waits on the provider: %v", plan)
	}
	if offer := installProviderOf(body); offer["name"] != "Music Project Management" {
		t.Fatalf("the pane is offered the install: %v", body["session"])
	}

	// The preview discloses the release and installs nothing.
	status, body := f.homeProvider(t, session, map[string]any{"confirm": false})
	if status != http.StatusOK {
		t.Fatalf("preview status %d: %v", status, body)
	}
	preview := homeProviderOf(t, body)
	if preview["name"] != "Music Project Management" || preview["version"] != "0.3.0" || preview["disclosure"] == nil ||
		len(provider.previews) != 1 || provider.previews[0] != "music_project_management" || len(provider.installs) != 0 {
		t.Fatalf("preview = %v; previews %v installs %v", preview, provider.previews, provider.installs)
	}

	// A release other than the one reviewed is refused, and nothing changes.
	status, body = f.homeProvider(t, session, map[string]any{"confirm": true, "reviewed_version": "0.2.9"})
	if status != http.StatusConflict || body["code"] != "provider_changed" {
		t.Fatalf("a stale release: %d %v", status, body)
	}

	status, body = f.homeProvider(t, session, map[string]any{"confirm": true, "reviewed_version": "0.3.0"})
	if status != http.StatusOK {
		t.Fatalf("install status %d: %v", status, body)
	}
	got := sessionOf(t, body)
	if plan := planOf(t, body); plan["ready"] != true || plan["digest"] != songDigest {
		t.Fatalf("after the install the plan is ready: %v", plan)
	}
	if last := lastEntry(got); !last.Host || last.Text != "Installed Music Project Management 0.3.0. Hire and start can run the plan now." {
		t.Fatalf("the transcript says what was installed: %+v", last)
	}
	if offer := installProviderOf(body); offer != nil {
		t.Fatalf("nothing is left to offer: %v", offer)
	}

	// With a ready plan there is nothing left to install.
	status, body = f.homeProvider(t, got, map[string]any{"confirm": false})
	if status != http.StatusConflict || body["code"] != "no_provider" {
		t.Fatalf("a ready plan has no provider to install: %d %v", status, body)
	}
}

func TestWorkspaceBuildHomeProvider_RefusesWhatItCannotInstall(t *testing.T) {
	f := newBuildFixture(t, true)
	folders := &fakeBuildFolders{observation: reaperObservation(), recognition: reaperRecognition(), plan: waitingPlan()}
	f.withFolders(t, folders)
	build := f.start(t)
	_, body := f.attachFolder(t, build, map[string]any{"mode": "chip", "chip": "documents"})
	session := sessionOf(t, body)

	// Not wired: the pane only says which plugin to install.
	if offer := installProviderOf(body); offer != nil {
		t.Fatalf("without the service nothing is offered: %v", offer)
	}
	if status, body := f.homeProvider(t, session, map[string]any{"confirm": false}); status != http.StatusConflict || body["code"] != "unavailable" {
		t.Fatalf("without the service: %d %v", status, body)
	}
	provider := &fakeHomeProvider{release: "0.3.0"}
	f.handler.workspaceBuild.HomeProvider = provider

	// A confirmation needs a preview first; a stale build version is refused.
	if status, _ := f.homeProvider(t, session, map[string]any{"confirm": false, "reviewed_version": "0.3.0"}); status != http.StatusBadRequest {
		t.Fatalf("a reviewed version without confirm: %d", status)
	}
	stale := session
	stale.Version--
	if status, body := f.homeProvider(t, stale, map[string]any{"confirm": false}); status != http.StatusConflict || body["code"] != "version" {
		t.Fatalf("a stale build: %d %v", status, body)
	}

	// The service has no release, or a Home is pinned to the old one.
	provider.err = ErrBuildProviderUnavailable
	if status, body := f.homeProvider(t, session, map[string]any{"confirm": false}); status != http.StatusServiceUnavailable ||
		body["code"] != "provider_unavailable" || !strings.Contains(body["error"].(string), "Music Project Management") {
		t.Fatalf("no release: %d %v", status, body)
	}
	provider.err = ErrBuildProviderHomeUpgrade
	if status, body := f.homeProvider(t, session, map[string]any{"confirm": true, "reviewed_version": "0.3.0"}); status != http.StatusConflict ||
		body["code"] != "home_upgrade_required" {
		t.Fatalf("a pinned Home: %d %v", status, body)
	}
	provider.err = nil

	// A plan that does not wait on the provider has nothing to install.
	folders.plan = &personalassistant.BuildFolderPlan{Lines: []personalassistant.FolderPlanLine{}, Unavailable: personalassistant.FolderAvailabilityPlanUnavailable}
	if status, body := f.homeProvider(t, session, map[string]any{"confirm": false}); status != http.StatusConflict || body["code"] != "no_provider" {
		t.Fatalf("a plan that waits on nothing installable: %d %v", status, body)
	}
	if len(provider.installs) != 1 {
		t.Fatalf("only the pinned-Home attempt reached the service: %v", provider.installs)
	}
}

// The model is told the folder's setup is one install away, and not to settle
// on Blank; without the service it keeps the old rule.
func TestWorkspaceBuildPrompt_ASetupWaitingOnItsPluginIsNeverSettledAsBlank(t *testing.T) {
	for _, wired := range []bool{true, false} {
		f := newBuildFixture(t, true, describeReply)
		f.withMusicCatalog()
		f.withFolders(t, &fakeBuildFolders{observation: reaperObservation(), recognition: reaperRecognition(), plan: waitingPlan()})
		if wired {
			f.handler.workspaceBuild.HomeProvider = &fakeHomeProvider{release: "0.3.0"}
		}
		build := f.start(t)
		_, body := f.attachFolder(t, build, map[string]any{"mode": "chip", "chip": "documents"})
		f.turn(t, sessionOf(t, body), map[string]any{"text": "music production demos"})
		if len(f.provider.systems) == 0 {
			t.Fatal("no model turn ran")
		}
		system := f.provider.systems[0]
		offers := strings.Contains(system, "The form offers Install Music Project Management") &&
			strings.Contains(system, "Do not choose Blank or another blueprint for this folder") &&
			strings.Contains(system, "do not say it is ready to create, and do not ask about its group")
		if offers != wired {
			t.Fatalf("wired=%v: the prompt offers the install = %v:\n%s", wired, offers, system)
		}
	}
}

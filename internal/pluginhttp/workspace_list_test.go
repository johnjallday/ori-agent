package pluginhttp

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/johnjallday/ori-agent/internal/plugin"
)

func listHandler(t *testing.T) (*Handler, string) {
	t.Helper()
	root := filepath.Join(t.TempDir(), "Ori Workspaces")
	h := newHandlerWithManager(plugin.NewManager(&stubReg{}, t.TempDir(), ""))
	h.SetWorkspaceRootResolver(func() string { return root })
	return h, root
}

func readList(t *testing.T, root string) plugin.PluginList {
	t.Helper()
	list, found, err := plugin.ReadPluginList(root)
	if err != nil || !found {
		t.Fatalf("read list: %v %v", found, err)
	}
	return list
}

func listModTime(t *testing.T, root string) time.Time {
	t.Helper()
	info, err := os.Stat(plugin.PluginListPath(root))
	if err != nil {
		t.Fatal(err)
	}
	return info.ModTime()
}

func TestThePluginListRecordsInstallUpdateAndUninstall(t *testing.T) {
	h, root := listHandler(t)
	bundle := claudeBundle(t)
	if rr := postInstall(t, h, bundle, true); rr.Code != http.StatusOK {
		t.Fatalf("install: %s", rr.Body.String())
	}
	list := readList(t, root)
	if len(list.Plugins) != 1 || list.Plugins[0].Name != "reaper" || list.Plugins[0].Source != bundle || list.Plugins[0].Version != "0.1.0" {
		t.Fatalf("after install = %+v", list)
	}

	// Enabling and disabling belong to this machine: no write.
	old := time.Now().Add(-time.Hour)
	_ = os.Chtimes(plugin.PluginListPath(root), old, old)
	before := listModTime(t, root)
	for _, enable := range []bool{true, false} {
		rr := httptest.NewRecorder()
		request := httptest.NewRequest(http.MethodPost, "/api/plugins/reaper/enable", nil)
		request.SetPathValue("name", "reaper")
		h.SetEnabledHandler(enable)(rr, request)
		if rr.Code != http.StatusOK {
			t.Fatalf("set enabled %v: %d %s", enable, rr.Code, rr.Body.String())
		}
	}
	if got := listModTime(t, root); !got.Equal(before) {
		t.Fatal("enable/disable rewrote the plugin list")
	}

	// A no-op update changes nothing either.
	if _, err := h.mgr.Update("reaper", func(plugin.TrustReport) bool { return true }); err != nil {
		t.Fatal(err)
	}
	if got := listModTime(t, root); !got.Equal(before) {
		t.Fatal("a no-op update rewrote the plugin list")
	}
	mustWrite(t, filepath.Join(bundle, ".claude-plugin", "plugin.json"), `{"name":"reaper","version":"0.2.0"}`)
	if _, err := h.mgr.Update("reaper", func(plugin.TrustReport) bool { return true }); err != nil {
		t.Fatal(err)
	}
	if list := readList(t, root); list.Plugins[0].Version != "0.2.0" {
		t.Fatalf("after update = %+v", list)
	}

	if err := h.mgr.Uninstall("reaper"); err != nil {
		t.Fatal(err)
	}
	list = readList(t, root)
	if len(list.Plugins) != 0 || len(list.Removed) != 1 || list.Removed[0].Name != "reaper" {
		t.Fatalf("after uninstall = %+v", list)
	}
}

func TestFillingTheListRespectsTheConfirmedRootGuard(t *testing.T) {
	h, root := listHandler(t)
	h.list.root = nil // install without recording, as a machine that had the plugin before
	if rr := postInstall(t, h, claudeBundle(t), true); rr.Code != http.StatusOK {
		t.Fatalf("install: %s", rr.Body.String())
	}
	h.SetWorkspaceRootResolver(func() string { return root })

	h.FillWorkspaceList(false)
	if _, err := os.Stat(plugin.PluginListPath(root)); !os.IsNotExist(err) {
		t.Fatal("a root this data dir did not confirm got a plugin list")
	}
	h.FillWorkspaceList(true)
	if list := readList(t, root); len(list.Plugins) != 1 || list.Plugins[0].Name != "reaper" {
		t.Fatalf("after fill = %+v", list)
	}
	before := listModTime(t, root)
	h.FillWorkspaceList(true)
	if !listModTime(t, root).Equal(before) {
		t.Fatal("a second fill rewrote the list")
	}
}

func TestAnUnreadableListIsReportedAndNeverOverwrittenByStartup(t *testing.T) {
	h, root := listHandler(t)
	h.list.root = nil
	if rr := postInstall(t, h, claudeBundle(t), true); rr.Code != http.StatusOK {
		t.Fatalf("install: %s", rr.Body.String())
	}
	h.SetWorkspaceRootResolver(func() string { return root })
	mustWrite(t, plugin.PluginListPath(root), "{broken")

	h.FillWorkspaceList(true)
	if data, _ := os.ReadFile(plugin.PluginListPath(root)); string(data) != "{broken" {
		t.Fatalf("startup overwrote an unreadable list: %q", data)
	}
	rr := httptest.NewRecorder()
	h.WorkspaceListHandler(rr, httptest.NewRequest(http.MethodGet, "/api/plugins/workspace-list", nil))
	var body workspaceListResponse
	if err := json.Unmarshal(rr.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(body.ReadError, "Your plugin list (Plugins.json) could not be read: ") || len(body.Pending) != 0 {
		t.Fatalf("response = %+v", body)
	}
}

func TestTheWorkspaceListOffersAListedPluginToInstall(t *testing.T) {
	h, root := listHandler(t)
	bundle := claudeBundle(t)
	list := plugin.PluginList{Plugins: []plugin.PluginListEntry{{Name: "reaper", Version: "0.1.0", Source: bundle, Format: plugin.FormatClaude}}}
	if _, err := plugin.WritePluginList(root, list); err != nil {
		t.Fatal(err)
	}
	rr := httptest.NewRecorder()
	h.WorkspaceListHandler(rr, httptest.NewRequest(http.MethodGet, "/api/plugins/workspace-list", nil))
	var body workspaceListResponse
	if err := json.Unmarshal(rr.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if len(body.Pending) != 1 || body.Pending[0].Kind != plugin.PendingInstall || body.Pending[0].Source != bundle || !body.Pending[0].Installable {
		t.Fatalf("pending = %+v", body)
	}
}

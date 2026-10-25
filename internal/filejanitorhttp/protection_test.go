package filejanitorhttp

import (
	"context"
	"net/http"
	"testing"
	"time"

	"github.com/johnjallday/ori-agent/internal/filecuratorguidance"
	"github.com/johnjallday/ori-agent/internal/filejanitor"
)

type protectionStub struct {
	calls int
	after func()
}

func (p *protectionStub) ReadProtection(_ context.Context, owner, id string) (filecuratorguidance.ProtectionFact, error) {
	p.calls++
	if p.after != nil {
		p.after()
	}
	return filecuratorguidance.ProtectionFact{WorkspaceID: id, State: "configured", RootGeneration: "root", AsOf: time.Date(2026, 10, 25, 12, 0, 0, 0, time.UTC)}, nil
}

func TestProtectionRouteIsAgentlessStoredOnlyAndOwnerChecked(t *testing.T) {
	for _, prefix := range routePrefixes() {
		t.Run(prefix, func(t *testing.T) {
			h, s := newTestHandler(t, map[string]string{"ws-1": "local", "other": "foreign"})
			// No settings store/readiness dependencies: Status would fail/panic.
			h.service = filejanitor.NewService(nil, s)
			reader := &protectionStub{}
			h.SetProtectionReader(reader)
			rec, body := serve(t, h, http.MethodGet, "/api/workspaces/ws-1/"+prefix+"/protection", "")
			if rec.Code != http.StatusOK || body["state"] != "configured" || reader.calls != 1 || rec.Header().Get("Cache-Control") != "no-store" {
				t.Fatal(rec.Code, body, reader.calls)
			}
			rec, _ = serve(t, h, http.MethodGet, "/api/workspaces/other/"+prefix+"/protection", "")
			if rec.Code != http.StatusNotFound || reader.calls != 1 {
				t.Fatal("foreign caller reached facts")
			}
			reader.after = func() { s.workspaces["ws-1"].OwnerUserID = "foreign" }
			rec, _ = serve(t, h, http.MethodGet, "/api/workspaces/ws-1/"+prefix+"/protection", "")
			if rec.Code != http.StatusNotFound {
				t.Fatal("late fact leaked", rec.Body.String())
			}
		})
	}
}

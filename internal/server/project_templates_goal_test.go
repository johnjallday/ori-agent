package server

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/johnjallday/ori-agent/internal/projecttemplates"
)

func TestProjectTemplateGoalUpdate(t *testing.T) {
	for _, tc := range []struct {
		name, field, want string
		status            int
	}{
		{"omitted", "", "Original", 200},
		{"null preserves", `,"goal":null`, "Original", 200},
		{"trimmed Unicode", `,"goal":"  Écrire 日本語 <b>literally</b>  "`, "Écrire 日本語 <b>literally</b>", 200},
		{"empty clears", `,"goal":""`, "", 200},
		{"whitespace clears", `,"goal":"  \n\t "`, "", 200},
		{"number", `,"goal":42`, "Original", 400},
		{"bool", `,"goal":true`, "Original", 400},
		{"object", `,"goal":{}`, "Original", 400},
		{"array", `,"goal":[]`, "Original", 400},
	} {
		t.Run(tc.name, func(t *testing.T) {
			lib := t.TempDir()
			if _, err := projecttemplates.CreateBlank(lib, "Mine"); err != nil {
				t.Fatal(err)
			}
			goal := "Original"
			if _, err := projecttemplates.UpdateManifest(lib, "mine", "Mine", "Desc", nil, &projecttemplates.ManifestEdit{Goal: &goal}); err != nil {
				t.Fatal(err)
			}
			s := newTemplateRoutesServer(t, lib)
			manifest := filepath.Join(lib, "mine", projecttemplates.ManifestFileName)
			before, err := os.ReadFile(manifest)
			if err != nil {
				t.Fatal(err)
			}
			r := httptest.NewRequest(http.MethodPut, "/template", bytes.NewBufferString(`{"name":"Mine","description":"Desc"`+tc.field+`}`))
			r.SetPathValue("templateID", "mine")
			w := httptest.NewRecorder()
			s.handleProjectTemplateUpdate(w, r)
			if w.Code != tc.status {
				t.Fatalf("status %d: %s", w.Code, w.Body.String())
			}
			got, err := projecttemplates.FindLibraryTemplate(lib, "mine")
			if err != nil || got.Goal != tc.want {
				t.Fatalf("goal %q, err %v", got.Goal, err)
			}
			if tc.status != http.StatusOK {
				after, err := os.ReadFile(manifest)
				if err != nil || !bytes.Equal(before, after) {
					t.Fatal("rejected edit changed manifest")
				}
			}
		})
	}
}

func TestProjectTemplateGoalImmutableSourcesAndBuiltinCopy(t *testing.T) {
	lib := t.TempDir()
	s := newTemplateRoutesServer(t, lib)
	if err := projecttemplates.EnsureLibrary(lib); err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{"code-project", "plugin:neutral:source"} {
		r := httptest.NewRequest(http.MethodPut, "/template", bytes.NewBufferString(`{"goal":"Not allowed"}`))
		r.SetPathValue("templateID", id)
		w := httptest.NewRecorder()
		s.handleProjectTemplateUpdate(w, r)
		if w.Code != http.StatusForbidden {
			t.Fatalf("immutable %s: %d %s", id, w.Code, w.Body.String())
		}
	}
	original, err := projecttemplates.FindLibraryTemplate(lib, "code-project")
	if err != nil {
		t.Fatal(err)
	}
	for i, status := range []int{http.StatusCreated, http.StatusConflict} {
		r := httptest.NewRequest(http.MethodPost, "/duplicate", bytes.NewBufferString(`{"name":"My code"}`))
		r.SetPathValue("templateID", "code-project")
		w := httptest.NewRecorder()
		s.handleProjectTemplateDuplicate(w, r)
		if w.Code != status {
			t.Fatalf("copy %d: %d %s", i, w.Code, w.Body.String())
		}
	}
	copy, err := projecttemplates.FindLibraryTemplate(lib, "my-code")
	if err != nil || copy.Builtin || copy.Goal != original.Goal || copy.BehaviorProfile != original.BehaviorProfile {
		t.Fatalf("ordinary copy: %+v %v", copy, err)
	}
}

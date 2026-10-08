package homeprofile

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/johnjallday/ori-agent/internal/workspace"
)

// ErrOperationUnavailable means no installed project plugin offers a facts
// operation for this Home, so nothing can be read. Routes answer 409
// plugin_operation_unavailable.
var ErrOperationUnavailable = errors.New("no installed project plugin offers a facts operation")

// TemplatesApp is the one application whose templates a Home can list, and
// how: through the facts operation of the project plugin behind it.
type TemplatesApp struct {
	// AppID and AppName are the host tool table's identity and display name.
	AppID   string
	AppName string
	// Folders are the template folder names the consent review shows.
	Folders []string
	// PluginInstalled says the project plugin is installed and enabled;
	// OperationAvailable says it also declares a facts operation.
	PluginInstalled    bool
	OperationAvailable bool
}

// Facts is what a project plugin reports about its own application.
type Facts struct {
	App                string
	Installed          bool
	Version            string
	TemplatesAvailable bool
	Templates          []workspace.HomeProfileTemplate
	Truncated          bool
}

// The facts operation's closed output. The plugin's process is untrusted
// input even after the host's schema check, so every value is bounded again.
type factsOutput struct {
	App                string         `json:"app"`
	Installed          bool           `json:"installed"`
	Version            string         `json:"version"`
	TemplatesAvailable bool           `json:"templates_available"`
	Templates          []factTemplate `json:"templates"`
	Truncated          bool           `json:"truncated"`
}

type factTemplate struct {
	Name       string `json:"name"`
	Kind       string `json:"kind"`
	File       string `json:"file"`
	ModifiedAt string `json:"modified_at"`
}

// decodeFacts accepts a facts answer for app only. An answer for another
// application, more templates than the limit or anything undecodable is an
// error. A single unusable item is dropped and the list marked incomplete,
// so one odd file name cannot hide every other template. Nothing that looks
// like a path is kept.
func decodeFacts(raw json.RawMessage, app TemplatesApp) (Facts, error) {
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	var output factsOutput
	if err := decoder.Decode(&output); err != nil {
		return Facts{}, fmt.Errorf("facts answer is not the declared shape: %w", err)
	}
	if _, err := decoder.Token(); !errors.Is(err, io.EOF) {
		return Facts{}, errors.New("facts answer has trailing data")
	}
	if !strings.EqualFold(strings.TrimSpace(output.App), app.AppName) {
		return Facts{}, errors.New("facts answer is about another application")
	}
	if len(output.Templates) > workspace.HomeProfileMaxTemplates {
		return Facts{}, errors.New("facts answer lists more templates than the limit")
	}
	facts := Facts{App: app.AppName, Installed: output.Installed, TemplatesAvailable: output.TemplatesAvailable, Truncated: output.Truncated}
	if version := strings.TrimSpace(output.Version); output.Installed && plainLine(version, 40) {
		facts.Version = version
	}
	if !output.Installed {
		return facts, nil
	}
	seen := make(map[string]bool, len(output.Templates))
	for _, item := range output.Templates {
		name, file := strings.TrimSpace(item.Name), strings.TrimSpace(item.File)
		kind := item.Kind
		key := kind + "\x00" + file
		if (kind != workspace.HomeProfileTemplateProject && kind != workspace.HomeProfileTemplateTrack) ||
			!plainLine(name, 120) || !workspace.HomeProfileTemplateFileValid(file) || seen[key] {
			facts.Truncated = true
			continue
		}
		seen[key] = true
		template := workspace.HomeProfileTemplate{Name: name, Kind: kind, File: file}
		// A time is kept only when it is a plausible file time. One far outside
		// that (an offset can push a year out of the range a record can be
		// saved with) is dropped rather than left to fail the Home's write.
		if modified, err := time.Parse(time.RFC3339, item.ModifiedAt); err == nil {
			if modified = modified.UTC(); modified.Year() >= 1970 && modified.Year() <= 9998 {
				template.ModifiedAt = &modified
			}
		}
		facts.Templates = append(facts.Templates, template)
	}
	return facts, nil
}

// plainLine accepts one line of plain, visible text within limit characters.
// Besides control characters it refuses what could make a name read
// differently from what it is: line and paragraph separators and the marks
// that reorder text. A value with nothing visible in it is refused too. The
// plugin is asked to send none of these; the host does not take its word.
func plainLine(value string, limit int) bool {
	if value == "" || !utf8.ValidString(value) || utf8.RuneCountInString(value) > limit {
		return false
	}
	visible := false
	for _, r := range value {
		switch {
		case unicode.IsControl(r), unicode.Is(unicode.Zl, r), unicode.Is(unicode.Zp, r), unicode.Is(unicode.Bidi_Control, r):
			return false
		case unicode.IsGraphic(r) && !unicode.IsSpace(r) && !unicode.Is(unicode.Cf, r):
			visible = true
		}
	}
	return visible
}

func (s *Service) templatesApp(home *workspace.Workspace) (TemplatesApp, bool) {
	if s.deps.TemplatesApp == nil {
		return TemplatesApp{}, false
	}
	app, ok := s.deps.TemplatesApp(home)
	if !ok || app.AppID == "" || app.AppName == "" {
		return TemplatesApp{}, false
	}
	return app, true
}

// readFacts calls the facts operation once. It reports ErrOperationUnavailable
// when there is nothing to call, and any other error when the call or its
// answer failed.
func (s *Service) readFacts(ctx context.Context, home *workspace.Workspace, app TemplatesApp, includeTemplates bool) (Facts, error) {
	if s.deps.ReadFacts == nil || !app.OperationAvailable {
		return Facts{}, ErrOperationUnavailable
	}
	raw, err := s.deps.ReadFacts(ctx, home, includeTemplates)
	if err != nil {
		return Facts{}, err
	}
	return decodeFacts(raw, app)
}

// readVersion asks the application's plugin for its version when that
// application was found. It lists nothing, and a failure only means no
// version is shown.
func (s *Service) readVersion(ctx context.Context, home *workspace.Workspace, foundIDs map[string]bool) (string, string) {
	app, ok := s.templatesApp(home)
	if !ok || !foundIDs[app.AppID] {
		return "", ""
	}
	facts, err := s.readFacts(ctx, home, app, false)
	if err != nil || !facts.Installed {
		return "", ""
	}
	return app.AppID, facts.Version
}

// applyVersion records an application's version on its row.
func applyVersion(profile *workspace.HomeProfile, appID, version string) {
	if appID == "" || version == "" {
		return
	}
	for index := range profile.Apps {
		if profile.Apps[index].ID == appID {
			profile.Apps[index].Version = version
		}
	}
}

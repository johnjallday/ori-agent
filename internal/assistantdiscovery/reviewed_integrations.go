package assistantdiscovery

import (
	"context"
	"slices"
	"strings"
	"unicode"

	"github.com/johnjallday/ori-agent/internal/plugin"
	"github.com/johnjallday/ori-agent/internal/reviewedintegration"
)

// Where a guided setup's quest comes from: the host generates the install
// quest; an installed plugin declares its own.
const (
	SetupQuestSourceHost   = "host"
	SetupQuestSourcePlugin = "plugin"
)

// SetupQuestRef names a guided setup by its host-resolved key. It carries no
// route or URL; the browser builds the link from the key.
type SetupQuestRef struct {
	Source   string `json:"source"`
	PluginID string `json:"plugin_id,omitempty"`
	ID       string `json:"id"`
}

// ReviewedIntegration is one host-reviewed integration as the assistant may
// name it: what it is called, whether its plugin is here, and the guided setup
// to open next. Every field comes from the compiled registry and the plugin
// store at read time.
type ReviewedIntegration struct {
	Key         string `json:"key"`
	DisplayName string `json:"display_name"`
	// Description is the entry's reviewed sentence about what the integration
	// is, for the assistant to repeat rather than invent.
	Description            string `json:"description,omitempty"`
	Installed              bool   `json:"installed"`
	InstalledVersion       string `json:"installed_version,omitempty"`
	Enabled                bool   `json:"enabled"`
	MinimumReviewedVersion string `json:"minimum_reviewed_version"`
	// SetupComplete is true once the plugin is installed and enabled and its
	// own guided setup is finished.
	SetupComplete bool `json:"setup_complete"`
	// SetupQuest is the guided setup to open next: the install quest until the
	// plugin is installed and enabled, then the plugin's own. Nil when setup is
	// complete or the plugin lists none.
	SetupQuest *SetupQuestRef `json:"setup_quest,omitempty"`
}

// ReviewedIntegrationsResult is the reader's whole answer. Status is
// "unavailable" when the registry or the plugin store could not be read, in
// which case nothing is claimed about any integration.
type ReviewedIntegrationsResult struct {
	Status       string                `json:"status"`
	Integrations []ReviewedIntegration `json:"integrations"`
}

// PluginSetupQuest is an installed plugin's own guided setup for one
// integration and whether this user has finished it.
type PluginSetupQuest struct {
	Ref      SetupQuestRef
	Complete bool
}

// InstalledPluginLister is the plugin store's inventory.
type InstalledPluginLister interface {
	List() ([]plugin.InstalledPlugin, error)
}

// ReviewedIntegrationSources are the owners the reader asks. Entries is the
// compiled registry, so no integration's name is ever written in host code.
type ReviewedIntegrationSources struct {
	Entries func() []reviewedintegration.Entry
	Plugins InstalledPluginLister
	// Platform is the host's "os/arch"; an entry that does not support it is
	// left out.
	Platform string
	// PluginQuest returns the installed plugin's own guided setup for an
	// integration key. Nil, or false, means the plugin lists none.
	PluginQuest func(ctx context.Context, userID, integrationKey string) (PluginSetupQuest, bool)
}

// ConfigureReviewedIntegrations wires the reviewed-integration reader.
func (s *Service) ConfigureReviewedIntegrations(sources ReviewedIntegrationSources) {
	if s != nil {
		s.reviewed = sources
	}
}

// ReviewedIntegrationsConfigured reports whether the reader has its owners, so
// a host without a plugin store never offers a reader that can only say
// "unavailable".
func (s *Service) ReviewedIntegrationsConfigured() bool {
	return s != nil && s.reviewed.Entries != nil && s.reviewed.Plugins != nil
}

// ReviewedIntegrations lists the integrations Ori has reviewed that are ready
// for release on this platform, each joined with its plugin's install state
// and the guided setup to open next. It reads the registry and the plugin
// store only: it installs, enables, downloads and starts nothing.
func (s *Service) ReviewedIntegrations(ctx context.Context, userID string) ReviewedIntegrationsResult {
	result := ReviewedIntegrationsResult{Status: string(Unavailable), Integrations: []ReviewedIntegration{}}
	if s == nil || s.reviewed.Entries == nil || s.reviewed.Plugins == nil {
		return result
	}
	installed, err := s.reviewed.Plugins.List()
	if err != nil || ctx.Err() != nil {
		return result
	}
	byName := make(map[string]plugin.InstalledPlugin, len(installed))
	for _, record := range installed {
		byName[strings.ToLower(strings.TrimSpace(record.Name))] = record
	}
	result.Status = string(Available)
	for _, entry := range s.offeredEntries() {
		if len(result.Integrations) >= MaxCandidates {
			break
		}
		name, key := strings.TrimSpace(entry.DisplayName), strings.TrimSpace(entry.Key)
		integration := ReviewedIntegration{
			Key: key, DisplayName: name, Description: strings.TrimSpace(entry.InstallDescription),
			MinimumReviewedVersion: entry.MinimumVersion,
		}
		install := &SetupQuestRef{Source: SetupQuestSourceHost, ID: entry.InstallQuestID()}
		record, present := byName[strings.ToLower(strings.TrimSpace(entry.PluginID))]
		switch {
		case !present:
			integration.SetupQuest = install
		case !record.Enabled:
			// Enabling is the install quest's second decision.
			integration.Installed, integration.InstalledVersion = true, record.Version
			integration.SetupQuest = install
		default:
			integration.Installed, integration.InstalledVersion, integration.Enabled = true, record.Version, true
			if s.reviewed.PluginQuest != nil {
				if quest, ok := s.reviewed.PluginQuest(ctx, userID, key); ok {
					if quest.Complete {
						integration.SetupComplete = true
					} else {
						ref := quest.Ref
						integration.SetupQuest = &ref
					}
				}
			}
		}
		result.Integrations = append(result.Integrations, integration)
	}
	return result
}

// offeredEntries are the registry entries this host may name: release-ready,
// supported on this platform, and carrying a key and a display name.
func (s *Service) offeredEntries() []reviewedintegration.Entry {
	if s == nil || s.reviewed.Entries == nil {
		return nil
	}
	var offered []reviewedintegration.Entry
	for _, entry := range s.reviewed.Entries() {
		if strings.TrimSpace(entry.DisplayName) == "" || strings.TrimSpace(entry.Key) == "" ||
			!entry.ReleaseReady || !slices.Contains(entry.SupportedPlatforms, s.reviewed.Platform) {
			continue
		}
		offered = append(offered, entry)
	}
	return offered
}

// ReviewedIntegrationsNamedIn returns the keys of the offered integrations
// whose display name appears in text. It reads the compiled registry only, so
// a turn can ask on every message without touching the plugin store.
func (s *Service) ReviewedIntegrationsNamedIn(text string) []string {
	var keys []string
	for _, entry := range s.offeredEntries() {
		if MentionsName(text, entry.DisplayName) {
			keys = append(keys, strings.TrimSpace(entry.Key))
		}
	}
	return keys
}

// MentionsName reports whether name appears in text as a whole word (or whole
// phrase), ignoring case. A letter or digit on either side means the match is
// part of a longer word and does not count; punctuation, as in a possessive,
// does not get in the way.
func MentionsName(text, name string) bool {
	needle := strings.ToLower(strings.Join(strings.Fields(name), " "))
	if needle == "" {
		return false
	}
	haystack := []rune(strings.ToLower(strings.Join(strings.Fields(text), " ")))
	want := []rune(needle)
	for start := 0; start+len(want) <= len(haystack); start++ {
		if !slices.Equal(haystack[start:start+len(want)], want) {
			continue
		}
		before := start == 0 || !wordRune(haystack[start-1])
		after := start+len(want) == len(haystack) || !wordRune(haystack[start+len(want)])
		if before && after {
			return true
		}
	}
	return false
}

func wordRune(r rune) bool { return unicode.IsLetter(r) || unicode.IsDigit(r) }

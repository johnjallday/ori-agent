package server

import (
	"strings"
	"time"

	"github.com/johnjallday/ori-agent/internal/downloadsjanitor"
	"github.com/johnjallday/ori-agent/internal/trigger"
)

// janitorTriggerStore adapts the trigger service to the narrow store the
// Downloads Janitor needs to install its own folder watcher.
//
// The Janitor deliberately does not import the trigger package's types: it
// describes the watcher it wants (folder, events, debounce, domain) and this
// adapter expresses that as a real trigger. Anything else a trigger can be —
// webhooks, mission runs, task prompts — stays outside the Janitor's reach.
type janitorTriggerStore struct {
	service *trigger.Service
}

func (s janitorTriggerStore) List(workspaceID string) ([]downloadsjanitor.TriggerRecord, error) {
	if s.service == nil {
		return nil, nil
	}
	var out []downloadsjanitor.TriggerRecord
	for _, t := range s.service.List(workspaceID) {
		if t.Action.Kind != trigger.ActionDomainScan {
			continue
		}
		out = append(out, downloadsjanitor.TriggerRecord{
			ID:              t.ID,
			WorkspaceID:     t.WorkspaceID,
			Name:            t.Name,
			Enabled:         t.Enabled,
			Path:            fileWatchPath(t),
			Events:          fileWatchEvents(t),
			DebounceSeconds: t.DebounceSeconds,
			Domain:          t.Action.Domain,
		})
	}
	return out, nil
}

func fileWatchPath(t trigger.Trigger) string {
	if t.FileWatch == nil {
		return ""
	}
	return t.FileWatch.Path
}

func fileWatchEvents(t trigger.Trigger) []string {
	if t.FileWatch == nil {
		return nil
	}
	return append([]string(nil), t.FileWatch.Events...)
}

func (s janitorTriggerStore) Upsert(record downloadsjanitor.TriggerRecord) (downloadsjanitor.TriggerRecord, error) {
	if s.service == nil {
		return record, nil
	}
	apply := func(t *trigger.Trigger) {
		t.Name = record.Name
		t.WorkspaceID = record.WorkspaceID
		t.Enabled = record.Enabled
		t.Type = trigger.TypeFileWatch
		t.DebounceSeconds = record.DebounceSeconds
		t.FileWatch = &trigger.FileWatchConfig{
			Path:   record.Path,
			Events: append([]string(nil), record.Events...),
		}
		t.Action = trigger.Action{Kind: trigger.ActionDomainScan, Domain: record.Domain}
	}

	if strings.TrimSpace(record.ID) != "" {
		updated, err := s.service.Update(record.WorkspaceID, record.ID, func(t *trigger.Trigger) error {
			apply(t)
			return nil
		})
		if err != nil {
			return record, err
		}
		record.ID = updated.ID
		return record, nil
	}

	var created trigger.Trigger
	apply(&created)
	stored, err := s.service.Create(created)
	if err != nil {
		return record, err
	}
	record.ID = stored.ID
	return record, nil
}

func (s janitorTriggerStore) Delete(workspaceID, triggerID string) error {
	if s.service == nil {
		return nil
	}
	return s.service.Delete(workspaceID, triggerID)
}

// wireDownloadsJanitorAutomation connects the Janitor's watcher and daily
// catch-up to the trigger service, and starts the scheduler.
//
// It runs after the trigger service exists. Until then the Janitor's readiness
// reports watcher and scheduler as not running — which is the honest answer,
// and keeps a workspace out of "Ready" rather than claiming automation that
// is not there.
func (b *ServerBuilder) wireDownloadsJanitorAutomation() {
	if b.downloadsJanitorService == nil || b.triggerService == nil {
		return
	}
	automation := downloadsjanitor.NewAutomation(b.downloadsJanitorService, janitorTriggerStore{service: b.triggerService})
	b.downloadsJanitorAutomation = automation
	b.downloadsJanitorService.SetAutomationStatus(automation)
	b.triggerService.RegisterDomainScanHandler(downloadsjanitor.DomainKey, automation)

	if b.downloadsJanitorHandler != nil {
		b.downloadsJanitorHandler.SetAutomation(automation)
	}

	// The daily catch-up runs over every configured workspace. Listing is done
	// per tick rather than cached, so a workspace configured after startup is
	// picked up without a restart.
	automation.Start(func() []string {
		if b.workspaceStore == nil {
			return nil
		}
		ids, err := b.workspaceStore.List()
		if err != nil {
			return nil
		}
		return ids
	}, downloadsJanitorSchedulerInterval)
}

// downloadsJanitorSchedulerInterval is how often the catch-up loop checks
// whether any workspace's local time has arrived. A minute is far finer than a
// daily schedule needs and costs a settings read per configured workspace.
const downloadsJanitorSchedulerInterval = time.Minute

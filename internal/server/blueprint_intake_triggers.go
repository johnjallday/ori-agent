package server

import (
	"github.com/johnjallday/ori-agent/internal/blueprintintake"
	"github.com/johnjallday/ori-agent/internal/trigger"
)

type blueprintIntakeTriggerStore struct{ service *trigger.Service }

func (s blueprintIntakeTriggerStore) List(workspaceID string) ([]blueprintintake.ReintakeTriggerRecord, error) {
	items := s.service.List(workspaceID)
	out := make([]blueprintintake.ReintakeTriggerRecord, 0, len(items))
	for _, item := range items {
		record := blueprintintake.ReintakeTriggerRecord{ID: item.ID, WorkspaceID: item.WorkspaceID, Name: item.Name, Enabled: item.Enabled, DebounceSeconds: item.DebounceSeconds, Domain: item.Action.Domain}
		if item.FileWatch != nil {
			record.Path = item.FileWatch.Path
			record.Events = append([]string(nil), item.FileWatch.Events...)
		}
		out = append(out, record)
	}
	return out, nil
}

func (s blueprintIntakeTriggerStore) Upsert(record blueprintintake.ReintakeTriggerRecord) (blueprintintake.ReintakeTriggerRecord, error) {
	value := trigger.Trigger{
		ID: record.ID, WorkspaceID: record.WorkspaceID, Name: record.Name, Type: trigger.TypeFileWatch,
		Enabled: record.Enabled, DebounceSeconds: record.DebounceSeconds,
		Action:    trigger.Action{Kind: trigger.ActionDomainScan, Domain: record.Domain},
		FileWatch: &trigger.FileWatchConfig{Path: record.Path, Events: append([]string(nil), record.Events...)},
	}
	var saved trigger.Trigger
	var err error
	if record.ID == "" {
		saved, err = s.service.Create(value)
	} else {
		saved, err = s.service.Update(record.WorkspaceID, record.ID, func(current *trigger.Trigger) error {
			current.Name, current.Enabled, current.DebounceSeconds = value.Name, value.Enabled, value.DebounceSeconds
			current.Action, current.FileWatch = value.Action, value.FileWatch
			return nil
		})
	}
	if err != nil {
		return blueprintintake.ReintakeTriggerRecord{}, err
	}
	return blueprintintake.ReintakeTriggerRecord{ID: saved.ID, WorkspaceID: saved.WorkspaceID, Name: saved.Name, Enabled: saved.Enabled, Path: saved.FileWatch.Path, Events: saved.FileWatch.Events, DebounceSeconds: saved.DebounceSeconds, Domain: saved.Action.Domain}, nil
}

func (b *ServerBuilder) wireBlueprintReintake() {
	if b.triggerService == nil || b.blueprintReintakeService == nil || b.blueprintIntakeService == nil {
		return
	}
	automation := blueprintintake.NewReintakeAutomation(b.blueprintReintakeService, b.blueprintIntakeService, b.workspaceFileStore, blueprintIntakeTriggerStore{service: b.triggerService})
	automation.SetAdmissionGate(b.resetWork)
	b.triggerService.RegisterDomainScanHandler(blueprintintake.ReintakeDomainKey, automation)
	if b.blueprintIntakeSetupAdapter != nil {
		b.blueprintIntakeSetupAdapter.SetAutomation(automation)
	}
	automation.Start(func() []string {
		ids, err := b.workspaceFileStore.List()
		if err != nil {
			return nil
		}
		return ids
	}, 0)
	b.blueprintReintake = automation
}

package server

import (
	"context"
	"errors"
	"time"

	"github.com/johnjallday/ori-agent/internal/config"
	"github.com/johnjallday/ori-agent/internal/connectionshttp"
	"github.com/johnjallday/ori-agent/internal/filejanitor"
	"github.com/johnjallday/ori-agent/internal/folderpath"
	"github.com/johnjallday/ori-agent/internal/followup"
	"github.com/johnjallday/ori-agent/internal/logger"
	"github.com/johnjallday/ori-agent/internal/pathselection"
	"github.com/johnjallday/ori-agent/internal/personalassistant"
	"github.com/johnjallday/ori-agent/internal/personalhq"
	"github.com/johnjallday/ori-agent/internal/secondbrain"
	"github.com/johnjallday/ori-agent/internal/userprofile"
	"github.com/johnjallday/ori-agent/internal/workspace"
)

// wireSecondBrain builds "connect your second brain" over the resolved HQ
// knowledge store. Detection reads Obsidian's vault list under the server's
// own HOME (FR4), so a sandboxed server only ever sees its sandbox's list.
func (b *ServerBuilder) wireSecondBrain(knowledge *personalassistant.KnowledgeStore) {
	if b == nil || knowledge == nil || b.personalAssistantHandler == nil {
		return
	}
	guards := filejanitor.DefaultRootGuards()
	service := personalassistant.NewSecondBrainService(personalassistant.NewSecondBrainStore(knowledge), personalassistant.SecondBrainDeps{
		Vaults: secondbrain.NewDetector(guards.HomeDir, guards),
		// The store may be created by a later phase; look it up at call time.
		ResolveSelection: func(token string) (string, error) {
			if b.pathSelectionStore == nil {
				return "", pathselection.ErrUnavailable
			}
			return b.pathSelectionStore.Resolve(token)
		},
		CheckFolder: func(path string) (folderpath.Checked, error) {
			return folderpath.Check(path, filejanitor.DefaultRootGuards())
		},
		// Read on every call: the Workspace Directory can be chosen or changed
		// while the server runs. The staging folder used before it is chosen
		// is not a home for the user's notes, so it offers no default.
		DefaultParent: func() string {
			root := resolveWorkspaceRoot(b.configManager)
			if root == config.UnconfirmedWorkspaceRoot() {
				return ""
			}
			return root
		},
		FollowUps: lateFollowUps{builder: b},
		// The headline is written by the system model, through the same path as
		// the Daily Brief and email triage, resolved on every look so a model set
		// later is used without a restart (FR23).
		Looker: secondbrain.Looker{Model: systemHeadlineModel{builder: b}},
	})
	// Setting one up copies what Ori already holds into the new folder; the
	// learning services it reads are built later, so they are looked up then.
	service.SetSeeds(secondBrainSeeds{builder: b}.Seeds)
	service.SetHoldings(secondBrainSeeds{builder: b}.Holdings)
	b.personalAssistantSecondBrain = service
	b.personalAssistantHandler.SetSecondBrain(service, connectionshttp.NewOriginGuard().Check)
}

// wireSecondBrainTriggers sends the other changes the fill-in mirrors to its one
// entry point, after each one's own write has succeeded (FR6): a follow-up, a new
// workspace, and a changed goal. Workspace events carry no user, and this is a
// one-user server. A workspace made another way reaches the notes at the next
// trigger, since each sync compares everything Ori holds.
func (b *ServerBuilder) wireSecondBrainTriggers() {
	service := b.personalAssistantSecondBrain
	if service == nil {
		return
	}
	if b.followUpService != nil {
		b.followUpService.OnChanged(func(userID string) { service.SyncSoon(userID, "follow-up") })
	}
	if b.eventBus != nil {
		b.eventBus.SubscribeToEventTypes(
			[]workspace.EventType{workspace.EventWorkspaceCreated, workspace.EventGoalCharterChanged},
			func(event workspace.Event) { service.SyncSoon(userprofile.LocalUserID, string(event.Type)) },
		)
	}
}

// journalDailyMirror copies a saved Journal to Daily/<date>.md in a second
// brain Ori set up (FR14). The second-brain service is built after the Journal
// service, so it is read at call time.
type journalDailyMirror struct{ builder *ServerBuilder }

func (m journalDailyMirror) MirrorDaily(ctx context.Context, userID, localDate, content string) personalhq.DailyResult {
	if m.builder == nil || m.builder.personalAssistantSecondBrain == nil {
		return ""
	}
	mirrored, err := m.builder.personalAssistantSecondBrain.WriteDaily(ctx, userID, localDate, content)
	switch {
	case err == nil && mirrored:
		return personalhq.DailyMirrored
	case err == nil:
		// Nothing connected, or a folder the user connected: nothing to copy to.
		return ""
	case errors.Is(err, secondbrain.ErrUserModified):
		return personalhq.DailyUserOwned
	default:
		logger.Warn("A saved journal could not be copied to Daily", logger.Fields{"error": err.Error()})
		return personalhq.DailyFailed
	}
}

// lateFollowUps reads the follow-up service at call time, since it may be
// wired in another phase.
type lateFollowUps struct{ builder *ServerBuilder }

var errFollowUpsUnavailable = errors.New("server: follow-ups are unavailable")

func (f lateFollowUps) service() (*followup.Service, error) {
	if f.builder == nil || f.builder.followUpService == nil {
		return nil, errFollowUpsUnavailable
	}
	return f.builder.followUpService, nil
}

func (f lateFollowUps) Capture(ctx context.Context, in followup.CaptureInput) (*followup.FollowUp, error) {
	service, err := f.service()
	if err != nil {
		return nil, err
	}
	return service.Capture(ctx, in)
}

func (f lateFollowUps) FindBySource(ctx context.Context, userID, sourceType, sourceID string) (*followup.FollowUp, error) {
	service, err := f.service()
	if err != nil {
		return nil, err
	}
	return service.FindBySource(ctx, userID, sourceType, sourceID)
}

func (f lateFollowUps) Edit(ctx context.Context, userID, id, title, detail string, dueAt *time.Time) (*followup.FollowUp, error) {
	service, err := f.service()
	if err != nil {
		return nil, err
	}
	return service.Edit(ctx, userID, id, title, detail, dueAt)
}

func (f lateFollowUps) Complete(ctx context.Context, userID, id string) (*followup.FollowUp, error) {
	service, err := f.service()
	if err != nil {
		return nil, err
	}
	return service.Complete(ctx, userID, id)
}

var errNoSystemModel = errors.New("server: no system model is set")

// systemHeadlineModel is the configured system model, read on every look.
type systemHeadlineModel struct{ builder *ServerBuilder }

func (m systemHeadlineModel) Complete(ctx context.Context, system, user string) (string, error) {
	if m.builder == nil {
		return "", errNoSystemModel
	}
	model := systemModelCompleter(m.builder.configManager, m.builder.llmFactory)
	if model == nil {
		return "", errNoSystemModel
	}
	return model.Complete(ctx, system, user)
}

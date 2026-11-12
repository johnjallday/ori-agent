package server

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/johnjallday/ori-agent/internal/folderagent"
	"github.com/johnjallday/ori-agent/internal/logger"
	"github.com/johnjallday/ori-agent/internal/session"
)

// folderReportStore is the session store's part that posts a folder agent's
// report into the Commander's conversation (workspace chat sync).
type folderReportStore interface {
	DeliverFolderReport(context.Context, session.FolderReportInput) (session.FolderReportMessage, bool, error)
	LatestFolderReport(context.Context, string) (session.FolderReportMessage, error)
	AcknowledgeFolderReport(context.Context, string, string) error
}

// folderAgentReports posts a look's report as the Commander's message in its
// conversation in the workspace, where the drawer draws the report's card.
type folderAgentReports struct {
	store folderReportStore
}

// newFolderAgentReports is nil when the session store cannot hold reports;
// the folder agent then posts nothing and the page keeps its own card.
func newFolderAgentReports(store any) folderagent.Reports {
	reports, ok := store.(folderReportStore)
	if !ok || reports == nil {
		return nil
	}
	return folderAgentReports{store: reports}
}

const folderReportDeliverTimeout = 5 * time.Second

func (r folderAgentReports) Reported(ctx context.Context, report folderagent.Report) {
	// The look's own context may end with it; posting is bounded on its own.
	ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), folderReportDeliverTimeout)
	defer cancel()
	title := "Getting started"
	if folder := strings.TrimSpace(report.FolderName); folder != "" {
		title = "Plan for " + folder
	}
	message, changed, err := r.store.DeliverFolderReport(ctx, session.FolderReportInput{
		WorkspaceID: report.WorkspaceID,
		AgentName:   report.AgentName,
		ReportKey:   report.Key,
		Content:     report.Text,
		Title:       title,
	})
	if err != nil {
		if !errors.Is(err, session.ErrFolderReportUnavailable) {
			logger.Warn("Folder agent report could not be posted to the conversation",
				logger.Fields{"workspace_id": report.WorkspaceID, "error": err.Error()})
		}
		return
	}
	if changed {
		logger.Info("Folder agent report posted to the conversation",
			logger.Fields{"workspace_id": report.WorkspaceID, "session_id": message.SessionID})
	}
}

func (r folderAgentReports) Latest(ctx context.Context, workspaceID string) (folderagent.ChatRef, bool) {
	message, err := r.store.LatestFolderReport(ctx, workspaceID)
	if err != nil {
		return folderagent.ChatRef{}, false
	}
	return folderagent.ChatRef{SessionID: message.SessionID, MessageID: message.MessageID, Unread: message.SeenAt == nil}, true
}

func (r folderAgentReports) Seen(ctx context.Context, workspaceID, messageID string) error {
	err := r.store.AcknowledgeFolderReport(ctx, workspaceID, messageID)
	if errors.Is(err, session.ErrFolderReportUnavailable) || errors.Is(err, session.ErrInvalidID) {
		return folderagent.ErrChatUnavailable
	}
	return err
}

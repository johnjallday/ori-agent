package filejanitor

import (
	"errors"
	"fmt"
	"os"
	"slices"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/johnjallday/ori-agent/internal/directoryid"
	"github.com/johnjallday/ori-agent/internal/logger"
	"github.com/johnjallday/ori-agent/internal/userprofile"
	"github.com/johnjallday/ori-agent/internal/workspace"
)

// A connection lets one workspace's Filing send reviewed files to the top level of
// another workspace's folder: "Downloads sends to Documents". It is one way, it is
// only ever created by the person turning it on (never by grouping or by folders
// being near each other), and either side can turn it off.
//
// The sender holds the record, in its own settings. The record carries the four
// identities both folders had when it was turned on: each workspace's folder
// generation and each folder's "device:inode". Whether a connection is still on is
// worked out every time from both workspaces as they are now, so pointing either
// workspace at another folder, revoking access, removing Filing or deleting a
// workspace ends it without any hook having to run. An ended connection stays
// listed, saying why, until someone turns it off.
//
// Nothing here moves a file. The press that sends files checks the connection
// again, under both folders' locks.

// MaxSendConnections bounds how many workspaces one workspace can send to.
const MaxSendConnections = 8

// CodeConnectionRefused is the setup error code for a connection Ori will not
// turn on. Its message says why in one plain sentence.
const CodeConnectionRefused = "connection_refused"

// guidedPurposeKey and guidedDocumentsPurpose are the workspace's own marker for
// a workspace created from the guided Documents suggestion.
const (
	guidedPurposeKey       = "guided_purpose"
	guidedDocumentsPurpose = "documents"
)

// SendConnection is the stored record, in the sender's settings. It holds no path.
type SendConnection struct {
	ID                  string     `json:"id"`
	ReceiverWorkspaceID string     `json:"receiver_workspace_id"`
	Kinds               []Category `json:"kinds"`
	// SenderRootID and ReceiverRootID are each workspace's folder generation
	// (JanitorSettings.RootID) when the connection was turned on.
	SenderRootID   string `json:"sender_root_id"`
	ReceiverRootID string `json:"receiver_root_id"`
	// SenderRootDirID and ReceiverRootDirID are each folder's "device:inode" when
	// the connection was turned on.
	SenderRootDirID   string    `json:"sender_root_dir_id"`
	ReceiverRootDirID string    `json:"receiver_root_dir_id"`
	CreatedAt         time.Time `json:"created_at"`
	CreatedBy         string    `json:"created_by"`
}

// Carries reports whether the connection carries files of kind.
func (c SendConnection) Carries(kind Category) bool { return slices.Contains(c.Kinds, kind) }

func (c SendConnection) valid(senderWorkspaceID string) bool {
	if !strings.HasPrefix(c.ID, "connection-") || !managedRootGeneration.MatchString(c.ID) ||
		strings.TrimSpace(c.ReceiverWorkspaceID) == "" || strings.EqualFold(c.ReceiverWorkspaceID, senderWorkspaceID) ||
		!managedRootGeneration.MatchString(c.SenderRootID) || !managedRootGeneration.MatchString(c.ReceiverRootID) ||
		deviceOf(c.SenderRootDirID) == "" || deviceOf(c.ReceiverRootDirID) == "" ||
		c.CreatedAt.IsZero() || strings.TrimSpace(c.CreatedBy) == "" || len(c.Kinds) == 0 {
		return false
	}
	for i, kind := range c.Kinds {
		if !ValidCategory(kind) || slices.Contains(c.Kinds[:i], kind) {
			return false
		}
	}
	return true
}

// validateSendConnections is the save rule: every record well formed, at most one
// per receiver, and no kind carried by two connections (one kind goes to at most
// one receiver, FR 7).
func validateSendConnections(senderWorkspaceID string, connections []SendConnection) error {
	if len(connections) > MaxSendConnections {
		return fmt.Errorf("%w: too many connections", ErrInvalidSettings)
	}
	ids, receivers, kinds := map[string]bool{}, map[string]bool{}, map[Category]bool{}
	for _, connection := range connections {
		receiver := strings.ToLower(connection.ReceiverWorkspaceID)
		if !connection.valid(senderWorkspaceID) || ids[connection.ID] || receivers[receiver] {
			return fmt.Errorf("%w: a connection is not valid", ErrInvalidSettings)
		}
		ids[connection.ID], receivers[receiver] = true, true
		for _, kind := range connection.Kinds {
			if kinds[kind] {
				return fmt.Errorf("%w: a kind goes to two workspaces", ErrInvalidSettings)
			}
			kinds[kind] = true
		}
	}
	return nil
}

// normalizeSendConnections is the load rule: a malformed list is dropped whole
// rather than half trusted. Losing it only loses the ability to send.
func normalizeSendConnections(senderWorkspaceID string, connections []SendConnection) []SendConnection {
	if len(connections) == 0 || validateSendConnections(senderWorkspaceID, connections) != nil {
		return nil
	}
	out := make([]SendConnection, len(connections))
	for i, connection := range connections {
		connection.Kinds = append([]Category(nil), connection.Kinds...)
		out[i] = connection
	}
	return out
}

// ConnectionView is one connection as either side shows it. It holds no path.
type ConnectionView struct {
	ID                  string     `json:"id"`
	SenderWorkspaceID   string     `json:"sender_workspace_id"`
	SenderName          string     `json:"sender_name"`
	ReceiverWorkspaceID string     `json:"receiver_workspace_id"`
	ReceiverName        string     `json:"receiver_name"`
	Kinds               []Category `json:"kinds"`
	// On is true while files can be sent. Ended says why not, in one sentence.
	On        bool      `json:"on"`
	Ended     string    `json:"ended,omitempty"`
	CreatedAt time.Time `json:"created_at"`
}

// ReceiverOption is another workspace with Filing set up on its folder, offered on
// the connect card (FR 2). Refusal is set when Ori can already tell it will not
// turn the connection on; the server checks everything again when asked.
type ReceiverOption struct {
	WorkspaceID string `json:"workspace_id"`
	Name        string `json:"name"`
	// GuidedDocuments is true for a workspace made from the guided Documents
	// suggestion: the card pre-ticks Documents for it and for no other (FR 6).
	GuidedDocuments bool   `json:"guided_documents,omitempty"`
	Refusal         string `json:"refusal,omitempty"`
}

// Connections is everything a workspace's Filing Settings tab shows about sending.
type Connections struct {
	WorkspaceName string           `json:"workspace_name"`
	SendsTo       []ConnectionView `json:"sends_to"`
	ReceivesFrom  []ConnectionView `json:"receives_from"`
	Receivers     []ReceiverOption `json:"receivers"`
}

// ConnectRequest turns a connection on: the receiver by workspace ID and the
// kinds the person ticked. The server works out both folders itself.
type ConnectRequest struct {
	ReceiverWorkspaceID string   `json:"receiver_workspace_id"`
	Kinds               []string `json:"kinds"`
}

func connectionRefused(message string) error {
	return setupErr(CodeConnectionRefused, message, "", nil)
}

// sendSide is one workspace as a connection sees it right now.
type sendSide struct {
	workspaceID string
	name        string
	settings    JanitorSettings
	root        ManagedRoot
	// dirID is the folder's "device:inode", read with no-follow; empty when the
	// folder could not be checked.
	dirID string
}

// workspaceName is the workspace's display name, or a plain fallback.
func (s *Service) workspaceName(workspaceID string) string {
	if ws, err := s.readWorkspace(workspaceID); err == nil && ws != nil && strings.TrimSpace(ws.Name) != "" {
		return strings.TrimSpace(ws.Name)
	}
	return "That workspace"
}

func ownedBy(ws *workspace.Workspace, owner string) bool {
	actual := strings.TrimSpace(ws.OwnerUserID)
	if actual == "" {
		actual = userprofile.LocalUserID
	}
	return strings.EqualFold(actual, strings.TrimSpace(owner))
}

// filingPaused reports a Filing the person paused after approving unattended
// work. A manual-only setup also stores Paused, and is not paused.
func filingPaused(settings JanitorSettings) bool {
	return settings.Paused && !settings.ManualScansOnly() && !settings.AutomationApprovedAt.IsZero()
}

// loadSide reads one workspace's Filing as it is now. withDisk also resolves the
// folder and reads its identity; without it nothing touches the disk.
func (s *Service) loadSide(owner, workspaceID string, withDisk bool) (sendSide, error) {
	side := sendSide{workspaceID: workspaceID, name: s.workspaceName(workspaceID)}
	root, err := s.StoredManagedRoot(owner, workspaceID)
	if err != nil {
		return side, err
	}
	settings, err := s.store.LoadSettings(workspaceID)
	if err != nil || settings.RootID != root.Generation {
		return side, ErrManagedRootUnavailable
	}
	side.root, side.settings = root, settings
	if !withDisk {
		return side, nil
	}
	resolved, err := s.scannerFor().ResolveRoot(settings)
	if err != nil || resolved != root.Path {
		return side, ErrRootUnavailable
	}
	info, err := os.Lstat(root.Path)
	if err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return side, ErrRootUnavailable
	}
	side.dirID = directoryid.Witness(info)
	if side.dirID == "" {
		return side, ErrRootUnavailable
	}
	return side, nil
}

// connectionState works out whether a stored connection is on, and if not, why
// not. It is the one place that decides; both sides and the press ask it.
func (s *Service) connectionState(owner string, sender sendSide, senderErr error, connection SendConnection) (on bool, ended string) {
	_, on, ended = s.checkConnection(owner, sender, senderErr, connection)
	return on, ended
}

// checkConnection is connectionState that also hands back the receiving side as
// it is now, for the review and the press that need its folder.
func (s *Service) checkConnection(owner string, sender sendSide, senderErr error, connection SendConnection) (receiver sendSide, on bool, ended string) {
	receiverName := s.workspaceName(connection.ReceiverWorkspaceID)
	switch {
	case senderErr != nil && errors.Is(senderErr, ErrRootUnavailable):
		return receiver, false, fmt.Sprintf("Ended because Ori cannot reach %s's folder.", sender.name)
	case senderErr != nil, sender.settings.RootID != connection.SenderRootID:
		return receiver, false, fmt.Sprintf("Ended because %s now points at a different folder, or no longer has Filing set up.", sender.name)
	}
	receiver, err := s.loadSide(owner, connection.ReceiverWorkspaceID, true)
	switch {
	case errors.Is(err, ErrRootUnavailable):
		return receiver, false, fmt.Sprintf("Ended because Ori cannot reach %s's folder.", receiverName)
	case err != nil:
		return receiver, false, fmt.Sprintf("Ended because %s no longer has Filing set up on its folder.", receiverName)
	case receiver.settings.RootID != connection.ReceiverRootID:
		return receiver, false, fmt.Sprintf("Ended because %s now points at a different folder.", receiverName)
	case sender.dirID != connection.SenderRootDirID || receiver.dirID != connection.ReceiverRootDirID:
		return receiver, false, "Ended because one of the two folders was moved or replaced."
	}
	if err := s.ConditionalMoveBetween(sender.root.Path, connection.SenderRootDirID, receiver.root.Path, connection.ReceiverRootDirID); err != nil {
		return receiver, false, "Ended because Ori can no longer move files safely between these two folders."
	}
	return receiver, true, ""
}

func (s *Service) viewOf(owner string, sender sendSide, senderErr error, connection SendConnection) ConnectionView {
	on, ended := s.connectionState(owner, sender, senderErr, connection)
	return ConnectionView{
		ID: connection.ID, SenderWorkspaceID: sender.workspaceID, SenderName: sender.name,
		ReceiverWorkspaceID: connection.ReceiverWorkspaceID, ReceiverName: s.workspaceName(connection.ReceiverWorkspaceID),
		Kinds: append([]Category(nil), connection.Kinds...), On: on, Ended: ended, CreatedAt: connection.CreatedAt,
	}
}

// otherWorkspaces lists every other workspace the owner has, or nothing when the
// store cannot list.
func (s *Service) otherWorkspaces(owner, workspaceID string) []*workspace.Workspace {
	lister, ok := s.workspaces.(WorkspaceLister)
	if !ok {
		return nil
	}
	ids, err := lister.List()
	if err != nil {
		logger.Warn("Filing could not list workspaces for connections", logger.Fields{"error": err.Error()})
		return nil
	}
	var out []*workspace.Workspace
	for _, id := range ids {
		if strings.EqualFold(strings.TrimSpace(id), workspaceID) {
			continue
		}
		ws, err := s.readWorkspace(id)
		if err != nil || ws == nil || ws.ID != id || !ownedBy(ws, owner) {
			continue
		}
		out = append(out, ws)
	}
	return out
}

// ListConnections is what a workspace's Settings tab shows: the connections it
// sends on, the ones it receives on, and the workspaces it could send to.
func (s *Service) ListConnections(owner, workspaceID string) (Connections, error) {
	if s == nil || s.store == nil {
		return Connections{}, ErrManagedRootUnavailable
	}
	workspaceID = strings.TrimSpace(workspaceID)
	out := Connections{WorkspaceName: s.workspaceName(workspaceID), SendsTo: []ConnectionView{}, ReceivesFrom: []ConnectionView{}, Receivers: []ReceiverOption{}}
	self, selfErr := s.loadSide(owner, workspaceID, true)
	settings, err := s.store.LoadSettings(workspaceID)
	if err != nil {
		return Connections{}, err
	}
	for _, connection := range settings.SendsTo {
		out.SendsTo = append(out.SendsTo, s.viewOf(owner, self, selfErr, connection))
	}

	sendsOnTo := map[string]bool{}
	for _, view := range out.SendsTo {
		if view.On {
			sendsOnTo[strings.ToLower(view.ReceiverWorkspaceID)] = true
		}
	}
	for _, ws := range s.otherWorkspaces(owner, workspaceID) {
		other, err := s.store.LoadSettings(ws.ID)
		if err != nil {
			continue
		}
		var otherSide *sendSide
		var otherErr error
		receivesOn := false
		for _, connection := range other.SendsTo {
			if !strings.EqualFold(connection.ReceiverWorkspaceID, workspaceID) {
				continue
			}
			if otherSide == nil {
				side, err := s.loadSide(owner, ws.ID, true)
				otherSide, otherErr = &side, err
			}
			view := s.viewOf(owner, *otherSide, otherErr, connection)
			receivesOn = receivesOn || view.On
			out.ReceivesFrom = append(out.ReceivesFrom, view)
		}
		if !ws.HasInstalledCapability(workspace.CapabilityFileJanitor) || !other.IsSetUp() {
			continue // only a workspace with Filing set up can receive (FR 2)
		}
		option := ReceiverOption{WorkspaceID: ws.ID, Name: s.workspaceName(ws.ID)}
		if purpose, _ := ws.SharedData[guidedPurposeKey].(string); purpose == guidedDocumentsPurpose {
			option.GuidedDocuments = true
		}
		switch {
		case sendsOnTo[strings.ToLower(ws.ID)]:
			option.Refusal = fmt.Sprintf("%s already sends to %s.", out.WorkspaceName, option.Name)
		case receivesOn:
			option.Refusal = fmt.Sprintf("%s already sends to %s. A pair of workspaces sends one way only.", option.Name, out.WorkspaceName)
		default:
			if _, err := s.loadSide(owner, ws.ID, false); err != nil {
				option.Refusal = fmt.Sprintf("%s's Filing has lost access to its folder, so it cannot receive files.", option.Name)
			} else if filingPaused(other) {
				option.Refusal = fmt.Sprintf("%s's Filing is paused. Turn it back on there first.", option.Name)
			}
		}
		out.Receivers = append(out.Receivers, option)
	}
	slices.SortFunc(out.Receivers, func(a, b ReceiverOption) int {
		return strings.Compare(strings.ToLower(a.Name), strings.ToLower(b.Name))
	})
	return out, nil
}

// normalizeKinds validates the kinds the person ticked: at least one, each one
// of the fixed kinds, none twice, in the fixed display order.
func normalizeKinds(raw []string) ([]Category, error) {
	seen := map[Category]bool{}
	for _, value := range raw {
		definition, err := LookupCategory(value)
		if err != nil {
			return nil, connectionRefused("Choose kinds from the list.")
		}
		seen[definition.ID] = true
	}
	if len(seen) == 0 {
		return nil, connectionRefused("Tick at least one kind of file to send.")
	}
	kinds := make([]Category, 0, len(seen))
	for _, definition := range CategoryRegistry {
		if seen[definition.ID] {
			kinds = append(kinds, definition.ID)
		}
	}
	return kinds, nil
}

func kindLabel(kind Category) string {
	if definition, err := LookupCategory(string(kind)); err == nil {
		return definition.Label
	}
	return string(kind)
}

// ConnectWorkspace turns on "sender sends to receiver" after every check in FR 9
// passes. It moves no file.
func (s *Service) ConnectWorkspace(owner, senderID string, req ConnectRequest) (Connections, error) {
	if s == nil || s.store == nil {
		return Connections{}, ErrManagedRootUnavailable
	}
	senderID = strings.TrimSpace(senderID)
	receiverID := strings.TrimSpace(req.ReceiverWorkspaceID)
	kinds, err := normalizeKinds(req.Kinds)
	if err != nil {
		return Connections{}, err
	}
	if receiverID == "" || strings.EqualFold(receiverID, senderID) {
		return Connections{}, connectionRefused("Choose another workspace to send files to.")
	}

	// The same lock every folder claim takes: the checks below read other
	// workspaces' settings and then write this one's.
	s.claimMu.Lock()
	defer s.claimMu.Unlock()

	sender, err := s.loadSide(owner, senderID, true)
	if err != nil {
		return Connections{}, connectionRefused("Set up Filing on this workspace's folder before sending files elsewhere.")
	}
	receiverWS, err := s.readWorkspace(receiverID)
	if err != nil || receiverWS == nil || receiverWS.ID != receiverID || !ownedBy(receiverWS, owner) {
		return Connections{}, connectionRefused("That workspace is not available.")
	}
	receiverName := s.workspaceName(receiverID)
	receiverSettings, err := s.store.LoadSettings(receiverID)
	if err != nil || !receiverWS.HasInstalledCapability(workspace.CapabilityFileJanitor) || !receiverSettings.IsSetUp() {
		return Connections{}, connectionRefused(fmt.Sprintf("%s has no Filing set up on its folder, so it cannot receive files.", receiverName))
	}
	if filingPaused(receiverSettings) {
		return Connections{}, connectionRefused(fmt.Sprintf("%s's Filing is paused. Turn it back on there first.", receiverName))
	}
	receiver, err := s.loadSide(owner, receiverID, true)
	if err != nil {
		return Connections{}, connectionRefused(fmt.Sprintf("%s's Filing has lost access to its folder, so it cannot receive files.", receiverName))
	}
	if RootsOverlap(sender.root.Path, receiver.root.Path) || sender.dirID == receiver.dirID {
		return Connections{}, connectionRefused(fmt.Sprintf("%s and %s use the same folder, or one folder is inside the other.", sender.name, receiverName))
	}
	if err := s.ConditionalMoveBetween(sender.root.Path, sender.dirID, receiver.root.Path, receiver.dirID); err != nil {
		if errors.Is(err, ErrMoveCrossDevice) {
			return Connections{}, connectionRefused(fmt.Sprintf("%s and %s are on different disks. Ori only sends a file by moving it on one disk, never by copying it.", sender.name, receiverName))
		}
		return Connections{}, connectionRefused(fmt.Sprintf("One of these folders is on a disk where Ori cannot move a file without risking replacing another, so %s cannot receive files from %s.", receiverName, sender.name))
	}
	// One way only (FR 8): the reverse of a connection that is still on is refused.
	for _, reverse := range receiverSettings.SendsTo {
		if !strings.EqualFold(reverse.ReceiverWorkspaceID, senderID) {
			continue
		}
		if on, _ := s.connectionState(owner, receiver, nil, reverse); on {
			return Connections{}, connectionRefused(fmt.Sprintf("%s already sends to %s. A pair of workspaces sends one way only, so turn that off first.", receiverName, sender.name))
		}
	}
	// Which of this sender's connections are still on, worked out before the write
	// (it reads other workspaces and the disk).
	current, err := s.store.LoadSettings(senderID)
	if err != nil {
		return Connections{}, err
	}
	onIDs := map[string]bool{}
	for _, connection := range current.SendsTo {
		if on, _ := s.connectionState(owner, sender, nil, connection); on {
			onIDs[connection.ID] = true
		}
	}

	now := s.clock()
	_, err = s.store.UpdateSettings(senderID, func(settings *JanitorSettings) error {
		if settings.RootID != sender.settings.RootID {
			return connectionRefused("This workspace's folder changed. Open Settings again and try once more.")
		}
		kept := make([]SendConnection, 0, len(settings.SendsTo)+1)
		for _, existing := range settings.SendsTo {
			if !onIDs[existing.ID] {
				continue // an ended connection is replaced, never revived
			}
			if strings.EqualFold(existing.ReceiverWorkspaceID, receiverID) {
				return connectionRefused(fmt.Sprintf("%s already sends to %s.", sender.name, receiverName))
			}
			for _, kind := range kinds {
				if existing.Carries(kind) {
					return connectionRefused(fmt.Sprintf("%s files already go to %s. Leave %s unticked, or turn that connection off first.",
						kindLabel(kind), s.workspaceName(existing.ReceiverWorkspaceID), kindLabel(kind)))
				}
			}
			kept = append(kept, existing)
		}
		if len(kept) >= MaxSendConnections {
			return connectionRefused(fmt.Sprintf("This workspace already sends to %d workspaces, which is the most it can.", MaxSendConnections))
		}
		settings.SendsTo = append(kept, SendConnection{
			ID: "connection-" + uuid.New().String(), ReceiverWorkspaceID: receiverID, Kinds: kinds,
			SenderRootID: sender.settings.RootID, SenderRootDirID: sender.dirID,
			ReceiverRootID: receiver.settings.RootID, ReceiverRootDirID: receiver.dirID,
			CreatedAt: now, CreatedBy: owner,
		})
		return nil
	})
	if err != nil {
		return Connections{}, err
	}
	logger.Info("Filing connection turned on", logger.Fields{"workspace_id": senderID, "receiver_workspace_id": receiverID, "kinds": len(kinds)})
	return s.ListConnections(owner, senderID)
}

// TurnOffConnection removes a connection, from whichever side asks (FR 11). It
// moves no file: files already sent stay where they are. Turning off a connection
// that is already gone is not an error.
func (s *Service) TurnOffConnection(owner, workspaceID, connectionID string) (Connections, error) {
	if s == nil || s.store == nil {
		return Connections{}, ErrManagedRootUnavailable
	}
	workspaceID, connectionID = strings.TrimSpace(workspaceID), strings.TrimSpace(connectionID)
	if !managedRootGeneration.MatchString(connectionID) {
		return Connections{}, connectionRefused("That connection is not available.")
	}
	remove := func(senderID string, receiverMustBe string) (bool, error) {
		removed := false
		_, err := s.store.UpdateSettings(senderID, func(settings *JanitorSettings) error {
			kept := settings.SendsTo[:0:0]
			for _, connection := range settings.SendsTo {
				if connection.ID == connectionID && (receiverMustBe == "" || strings.EqualFold(connection.ReceiverWorkspaceID, receiverMustBe)) {
					removed = true
					continue
				}
				kept = append(kept, connection)
			}
			if len(kept) == 0 {
				kept = nil
			}
			settings.SendsTo = kept
			return nil
		})
		return removed, err
	}

	// The sender's side.
	if removed, err := remove(workspaceID, ""); err != nil {
		return Connections{}, err
	} else if removed {
		logger.Info("Filing connection turned off", logger.Fields{"workspace_id": workspaceID, "side": "sender"})
		return s.ListConnections(owner, workspaceID)
	}
	// The receiver's side: the record is in a sender's settings, and only a
	// connection naming this workspace as its receiver may be removed from here.
	for _, ws := range s.otherWorkspaces(owner, workspaceID) {
		settings, err := s.store.LoadSettings(ws.ID)
		if err != nil {
			continue
		}
		for _, connection := range settings.SendsTo {
			if connection.ID != connectionID || !strings.EqualFold(connection.ReceiverWorkspaceID, workspaceID) {
				continue
			}
			if _, err := remove(ws.ID, workspaceID); err != nil {
				return Connections{}, err
			}
			logger.Info("Filing connection turned off", logger.Fields{"workspace_id": workspaceID, "side": "receiver"})
			return s.ListConnections(owner, workspaceID)
		}
	}
	return s.ListConnections(owner, workspaceID)
}

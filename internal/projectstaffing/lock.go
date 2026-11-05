package projectstaffing

import (
	"strings"
	"sync"

	"github.com/johnjallday/ori-agent/internal/workspace"
)

// One Home's shared staffing is decided, reserved, created, bound and recorded
// by several writes to two stores. A per-Home lock makes every consent-driven
// path in this process take turns: two tabs, an Open, a confirmed setup, a
// created project and the Home's sharing switch can no longer interleave
// between a reservation and the create it names. A second Ori process on the
// same data is not serialized here; the workspace fence still refuses its stale
// writes.

// homeLock is the mutex for one Home.
func (s *Service) homeLock(homeID string) *sync.Mutex {
	lock, _ := s.homes.LoadOrStore(strings.TrimSpace(homeID), &sync.Mutex{})
	return lock.(*sync.Mutex)
}

// Tx is one Home's staffing while its lock is held. Its methods never take the
// lock again, so a sequence of them is one turn.
type Tx struct {
	s    *Service
	home string
}

// WithHome runs fn while holding the Home's lock.
func (s *Service) WithHome(homeID string, fn func(tx *Tx) error) error {
	if s == nil {
		return fn(&Tx{})
	}
	lock := s.homeLock(homeID)
	lock.Lock()
	defer lock.Unlock()
	return fn(&Tx{s: s, home: strings.TrimSpace(homeID)})
}

// Hold takes the Home's lock for a caller that writes the consent itself (the
// sharing switch) and returns the release. It must not call the locking
// Service methods before releasing.
func (s *Service) Hold(homeID string) (release func()) {
	if s == nil {
		return func() {}
	}
	lock := s.homeLock(homeID)
	lock.Lock()
	return lock.Unlock
}

// HomeID is the Home this turn holds.
func (tx *Tx) HomeID() string { return tx.home }

// Consents is the consent store, for a write the caller makes itself (the
// sharing switch); it must stay inside this turn.
func (tx *Tx) Consents() *workspace.ProjectStaffingConsents { return tx.s.Consents() }

// owns refuses a project of another Home: the lock held is not its lock.
func (tx *Tx) owns(projectID string) error {
	song, err := tx.s.Song(projectID)
	if err != nil {
		return err
	}
	if song.Home.ID != tx.home {
		return ErrNotShared
	}
	return nil
}

// Fill is Service.Fill inside this turn.
func (tx *Tx) Fill(projectID string, roleIDs []string, grant *Grant) ([]Fill, error) {
	if err := tx.owns(projectID); err != nil {
		return nil, err
	}
	return tx.s.fill(projectID, roleIDs, grant)
}

// Settle is Service.Settle inside this turn.
func (tx *Tx) Settle(projectID string) error {
	if err := tx.owns(projectID); err != nil {
		return nil
	}
	return tx.s.settle(projectID)
}

// Recover is Service.Recover for this turn's Home.
func (tx *Tx) Recover() error { return tx.s.recoverHome(tx.home) }

// UnrecordedHolder is the reserved name an agent holds without proof that the
// consent created it, for one of roleIDs; "" when there is none.
func (tx *Tx) UnrecordedHolder(roleIDs []string) string {
	return tx.s.unrecordedHolder(tx.home, roleIDs)
}

// ReleaseReservation drops a role's pending name after its create was seen to
// fail because another agent has that name.
func (tx *Tx) ReleaseReservation(roleID, agentName string) error {
	return tx.s.consents.ReleaseReservation(tx.home, roleID, agentName)
}

// PendingHolder is UnrecordedHolder for a read outside a turn: a preview only.
func (s *Service) PendingHolder(homeID string, roleIDs []string) string {
	return s.unrecordedHolder(homeID, roleIDs)
}

// FreeName is the name a create for label would take now in the Home's
// projects (the user's agents, the Home and every project linked to it), for a
// preview. The create decides again when it runs.
func (s *Service) FreeName(homeID, label string) string {
	if s == nil || s.workspaces == nil {
		return ""
	}
	home, err := s.workspaces.Get(strings.TrimSpace(homeID))
	if err != nil || home == nil {
		return ""
	}
	return workspace.FirstFreeAgentName(label, s.nameTaken(Song{Home: home}))
}

// HomeOf is the Home a project is linked to, or "" when it has none: the key
// of the lock a caller takes with WithHome for that project.
func (s *Service) HomeOf(projectID string) string { return s.homeOf(projectID) }

// homeOf is the Home a project is linked to, or "" when it has none.
func (s *Service) homeOf(projectID string) string {
	if s == nil || s.workspaces == nil {
		return ""
	}
	project, err := s.workspaces.Get(strings.TrimSpace(projectID))
	if err != nil || project == nil {
		return ""
	}
	if link := project.GetAssistantProjectLink(); link != nil {
		return link.StationWorkspaceID
	}
	return ""
}

// Fill says how each of roleIDs (required, unfilled project roles of the song)
// is filled under the Home's consent. With grant set, a Home with no consent, or
// one switched off, first gets a fresh consent. A create reserves the name it
// will use before returning, so Settle can recognise it afterwards. It takes the
// Home's lock for the decision; a caller that also staffs and settles should
// run the whole sequence in one WithHome turn.
func (s *Service) Fill(projectID string, roleIDs []string, grant *Grant) ([]Fill, error) {
	var fills []Fill
	err := s.WithHome(s.homeOf(projectID), func(tx *Tx) error {
		var err error
		fills, err = tx.Fill(projectID, roleIDs, grant)
		return err
	})
	return fills, err
}

// Settle records, from the song's own staffing, the agent the consent's create
// made (see settle), under the Home's lock. It is safe to repeat.
func (s *Service) Settle(projectID string) error {
	return s.WithHome(s.homeOf(projectID), func(tx *Tx) error { return tx.Settle(projectID) })
}

// Recover settles every project of the Home, under its lock, so a create that
// was bound but never recorded becomes the consent's agent (see recoverHome).
func (s *Service) Recover(homeID string) error {
	return s.WithHome(homeID, func(*Tx) error { return s.recoverHome(homeID) })
}

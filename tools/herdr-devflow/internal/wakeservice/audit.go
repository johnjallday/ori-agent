package wakeservice

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/johnjallday/ori-agent/tools/herdr-devflow/internal/wakeprotocol"
)

const maxAuditEntryBytes = 2048

// auditEntry is the intentionally small privileged audit vocabulary. It
// excludes candidate reasons because those can contain user prompt or
// repository data; caller identity and the fixed protocol identity are enough
// to investigate a wake mutation safely.
type auditEntry struct {
	At           time.Time              `json:"at"`
	UID          int                    `json:"uid"`
	Operation    wakeprotocol.Operation `json:"operation"`
	CandidateID  string                 `json:"candidate_id,omitempty"`
	Source       wakeprotocol.Source    `json:"source,omitempty"`
	Purpose      wakeprotocol.Purpose   `json:"purpose,omitempty"`
	RequestedAt  time.Time              `json:"requested_at,omitempty"`
	ProgrammedAt time.Time              `json:"programmed_at,omitempty"`
	Result       wakeprotocol.Result    `json:"result"`
	Code         wakeprotocol.Code      `json:"code"`
	Reason       string                 `json:"reason,omitempty"`
}

func (s *Service) recordAudit(uid int, request wakeprotocol.Request, response wakeprotocol.Response, now time.Time) {
	entry := auditEntry{
		At: now.UTC(), UID: uid, Operation: request.Operation,
		Result: response.Result, Code: response.Code, Reason: boundedMessage(response.Message),
	}
	if request.Candidate != nil {
		entry.CandidateID = request.Candidate.ID
		entry.Source = request.Candidate.Source
		entry.Purpose = request.Candidate.Purpose
		entry.RequestedAt = request.Candidate.WakeAt.UTC()
	} else if request.Target != nil {
		entry.CandidateID = request.Target.ID
		entry.Source = request.Target.Source
		entry.Purpose = request.Target.Purpose
	}
	if response.State != nil && response.State.Programmed != nil {
		entry.ProgrammedAt = response.State.Programmed.WakeAt.UTC()
	}
	// An audit I/O problem must never make a completed privileged mutation look
	// as though it did not happen. The durable root state and direct pmset
	// verification remain the recovery authority.
	_ = s.store.appendAudit(entry)
}

func (s *rootStore) appendAudit(entry auditEntry) error {
	if err := prepareStateDir(s.dir, s.requireRoot); err != nil {
		return err
	}
	path := filepath.Join(s.dir, AuditFile)
	if info, err := os.Lstat(path); err == nil {
		if !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 {
			return fmt.Errorf("wake audit is not a regular file")
		}
		if info.Mode().Perm()&0077 != 0 {
			return fmt.Errorf("wake audit permissions are broader than 0600")
		}
		if s.requireRoot {
			if owner, ok := fileOwnerUID(info); !ok || owner != 0 {
				return fmt.Errorf("wake audit is not root-owned")
			}
		}
	} else if !os.IsNotExist(err) {
		return fmt.Errorf("inspect wake audit: %w", err)
	}
	payload, err := json.Marshal(entry)
	if err != nil {
		return fmt.Errorf("encode wake audit: %w", err)
	}
	if len(payload) > maxAuditEntryBytes {
		return fmt.Errorf("wake audit entry exceeds %d bytes", maxAuditEntryBytes)
	}
	// #nosec G304 -- path is a fixed filename below a root-owned, mode-0700 state
	// directory checked immediately above; the audit file itself is lstat-checked
	// for regular-file and non-symlink safety before opening.
	file, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0600)
	if err != nil {
		return fmt.Errorf("open wake audit: %w", err)
	}
	defer file.Close()
	if err := file.Chmod(0600); err != nil {
		return fmt.Errorf("secure wake audit: %w", err)
	}
	if _, err := file.Write(append(bytes.Clone(payload), '\n')); err != nil {
		return fmt.Errorf("append wake audit: %w", err)
	}
	if err := file.Sync(); err != nil {
		return fmt.Errorf("sync wake audit: %w", err)
	}
	return nil
}

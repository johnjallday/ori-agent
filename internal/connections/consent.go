package connections

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"sync"
	"time"
)

// ConsentAction records whether a product's cloud-data path was granted or
// withdrawn.
type ConsentAction string

const (
	ConsentGranted   ConsentAction = "granted"
	ConsentWithdrawn ConsentAction = "withdrawn"
)

// ConsentRecord is one audit entry for enabling/withdrawing a product's
// cloud-model data path (FR 96). It is deliberately token-, secret-, and
// content-free: only the product, the action, the data path, the connection's
// identity subject (an OIDC sub, not a secret), and a timestamp.
type ConsentRecord struct {
	Product   ProductKey    `json:"product"`
	Action    ConsentAction `json:"action"`
	DataPath  string        `json:"data_path"` // e.g. "cloud-model"
	Subject   string        `json:"subject,omitempty"`
	Timestamp time.Time     `json:"timestamp"`
}

// cloudModelDataPath is the data path a product grant opens: the user's product
// content may be sent to the configured model. It is recorded verbatim in the
// audit trail so a reviewer can see what was consented to.
const cloudModelDataPath = "cloud-model"

// ConsentLog is an append-only, content-free audit trail persisted next to the
// connection. It is safe for concurrent use.
type ConsentLog struct {
	path string
	mu   sync.Mutex
}

// NewConsentLog returns a log stored at <dataDir>/connections/consent.json.
func NewConsentLog(dataDir string) *ConsentLog {
	return &ConsentLog{path: filepath.Join(dataDir, "connections", "consent.json")}
}

// List returns the audit records oldest-first (empty if none yet).
func (l *ConsentLog) List() ([]ConsentRecord, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.readLocked()
}

func (l *ConsentLog) readLocked() ([]ConsentRecord, error) {
	data, err := os.ReadFile(l.path) // #nosec G304 -- path is app-owned, derived from the data dir
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, nil
		}
		return nil, err
	}
	var records []ConsentRecord
	if len(data) == 0 {
		return nil, nil
	}
	if err := json.Unmarshal(data, &records); err != nil {
		return nil, err
	}
	return records, nil
}

func (l *ConsentLog) appendLocked(records []ConsentRecord, add ...ConsentRecord) error {
	records = append(records, add...)
	blob, err := json.MarshalIndent(records, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(l.path), 0o750); err != nil {
		return err
	}
	return os.WriteFile(l.path, blob, 0o600)
}

// activeConsents returns, per product, whether its most recent action was a
// grant (i.e. consent is currently active).
func activeConsents(records []ConsentRecord) map[ProductKey]bool {
	active := map[ProductKey]bool{}
	for _, r := range records {
		active[r.Product] = r.Action == ConsentGranted
	}
	return active
}

// Reconcile appends the grant/withdrawal deltas needed to make the log match the
// connection's currently-enabled product grants (FR 96): a newly-enabled product
// records a grant, a product that has since been disabled/disconnected records a
// withdrawal. A nil connection means "everything withdrawn" (whole-account
// disconnect). It is idempotent — no change appends nothing — and returns the
// records it wrote.
func (l *ConsentLog) Reconcile(conn *Connection) ([]ConsentRecord, error) {
	l.mu.Lock()
	defer l.mu.Unlock()

	existing, err := l.readLocked()
	if err != nil {
		return nil, err
	}
	active := activeConsents(existing)

	subject := ""
	enabled := map[ProductKey]bool{}
	if conn != nil {
		subject = conn.Subject
		for _, p := range AllProducts() {
			if g, ok := conn.Grant(p); ok && g != nil {
				enabled[p] = true
			}
		}
	}

	now := time.Now().UTC()
	var added []ConsentRecord
	for _, p := range AllProducts() {
		switch {
		case enabled[p] && !active[p]:
			added = append(added, ConsentRecord{Product: p, Action: ConsentGranted, DataPath: cloudModelDataPath, Subject: subject, Timestamp: now})
		case !enabled[p] && active[p]:
			added = append(added, ConsentRecord{Product: p, Action: ConsentWithdrawn, DataPath: cloudModelDataPath, Timestamp: now})
		}
	}
	if len(added) == 0 {
		return nil, nil
	}
	if err := l.appendLocked(existing, added...); err != nil {
		return nil, err
	}
	return added, nil
}

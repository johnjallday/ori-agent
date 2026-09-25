package personalassistant

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"time"

	"github.com/johnjallday/ori-agent/internal/folderdigest"
)

// The folder-digest sidecar records what the assistant has offered about
// the user's folders and what the user answered (FR23, FR27). It lives beside
// the knowledge sidecar under <HQ>/.ori/ and is written with the same
// discipline. It stores folder keys and base names only, never full paths.
const (
	FolderDigestSchemaVersion = 1
	folderDigestFileName      = "folder-digest-v1.json"
	folderDigestLockName      = "folder-digest-v1.lock"
	folderDigestMaxBytes      = 256 * 1024
	// folderDigestMaxOffers bounds the offer history; settled offers past
	// the cap are pruned oldest first.
	folderDigestMaxOffers     = 64
	folderDigestMaxDecisions  = 256
	folderDigestMaxTombstones = 1024
	folderDigestMaxReceipts   = 128
	folderDigestMaxQueue      = 16
	folderDigestMaxName       = 255
	folderDigestMaxReason     = 200
)

// FolderOfferStatus is where an offer is in its life.
type FolderOfferStatus string

const (
	// FolderOfferPending is the one question currently asked (FR24).
	FolderOfferPending FolderOfferStatus = "pending"
	// FolderOfferLater is hidden and may return after LaterUntil (FR23).
	FolderOfferLater FolderOfferStatus = "later"
	// FolderOfferAwaitingOutcome follows a yes while the outcome runs (FR29).
	FolderOfferAwaitingOutcome FolderOfferStatus = "awaiting_outcome"
	// FolderOfferResolved is a yes whose outcome completed.
	FolderOfferResolved FolderOfferStatus = "resolved"
	// FolderOfferDeclined is a no; its subject is tombstoned.
	FolderOfferDeclined FolderOfferStatus = "declined"
	// FolderOfferClosed had nothing to decide (an empty or declined root).
	FolderOfferClosed FolderOfferStatus = "closed"
)

// Decisions the user can give an offer.
const (
	FolderDecisionYes   = "yes"
	FolderDecisionNo    = "no"
	FolderDecisionLater = "later"
)

// Outcomes a yes can choose.
const (
	FolderChoiceProject = "project"
	FolderChoiceTidy    = "tidy"
	FolderChoiceHome    = "home" // resolved portfolio, not a project folder
)

// FolderCandidateRecord is one thing the assistant could offer about a
// folder: a project inside it (or the folder itself), or a tidy of its loose
// files. Names are base names; RelPath is the subfolder's own name or empty
// for the root, which is all a display needs and all FR27 allows.
type FolderCandidateRecord struct {
	Key   string `json:"key"`
	Name  string `json:"name"`
	Kind  string `json:"kind"`
	Shape string `json:"shape,omitempty"`
	// Marker is the marker's plain label ("git repository"); MarkerName is
	// the table row it came from (".git"), for revalidation and tools.
	Marker     string `json:"marker,omitempty"`
	MarkerName string `json:"marker_name,omitempty"`
	// DominantExtension is the candidate's most common file kind (".tex"),
	// for tool candidates.
	DominantExtension string `json:"dominant_ext,omitempty"`
	Reason            string `json:"reason"`
	IsRoot            bool   `json:"is_root,omitempty"`
	RelPath           string `json:"rel_path,omitempty"`
	LooseFiles        int    `json:"loose_files,omitempty"`
	LooseKinds        int    `json:"loose_kinds,omitempty"`
}

// FolderReceiptRow is one server-observed part of a confirmed setup. No row
// contains a local filesystem path; the browser renders every field as text.
type FolderReceiptRow struct {
	Kind   string `json:"kind"`
	Name   string `json:"name"`
	Detail string `json:"detail,omitempty"`
	Route  string `json:"route,omitempty"`
}

// FolderOutcome is what a yes produced.
type FolderOutcome struct {
	Kind        string `json:"kind"`
	WorkspaceID string `json:"workspace_id,omitempty"`
	Route       string `json:"route,omitempty"`
	Blueprint   string `json:"blueprint,omitempty"`
	Remembered  bool   `json:"remembered,omitempty"`
	Note        string `json:"note,omitempty"`
	// Existing marks a tidy that opened a workspace already covering the
	// folder rather than setting a fresh one up, so the card opens that
	// workspace instead of showing a setup that did not happen.
	Existing bool               `json:"existing,omitempty"`
	Receipt  []FolderReceiptRow `json:"receipt,omitempty"`
}

// FolderPortfolioEvidence is the bounded, path-free count seen at scan time.
// It is persisted so a later read cannot turn a project offer into a Home
// offer (or the reverse) after the contents of a folder change.
type FolderPortfolioEvidence struct {
	Shape       string `json:"shape"`
	Projects    int    `json:"projects"`
	ProviderKey string `json:"provider_key"`
}

// FolderOffer is one question about one folder and its answer.
type FolderOffer struct {
	ID         string            `json:"id"`
	Status     FolderOfferStatus `json:"status"`
	Chip       string            `json:"chip,omitempty"`
	FolderKey  string            `json:"folder_key"`
	FolderName string            `json:"folder_name"`
	Verdict    string            `json:"verdict"`
	Reason     string            `json:"reason"`
	Partial    bool              `json:"partial,omitempty"`
	ScannedAt  time.Time         `json:"scanned_at"`
	// Subject is what the offer asks about: the named project, or the root
	// for a dump, ambiguous, or empty verdict.
	Subject FolderCandidateRecord `json:"subject"`
	// Queue holds the remaining candidates a later offer can ask about
	// once this one is decided (FR24).
	Queue         []FolderCandidateRecord  `json:"queue,omitempty"`
	ProjectsCount int                      `json:"projects_count,omitempty"`
	Portfolio     *FolderPortfolioEvidence `json:"portfolio,omitempty"`
	// The proactive capability decision is frozen when this scan is offered.
	CapabilitySuppressed bool           `json:"capability_suppressed,omitempty"`
	CapabilityRevived    bool           `json:"capability_revived,omitempty"`
	LooseFiles           int            `json:"loose_files,omitempty"`
	LooseKinds           int            `json:"loose_kinds,omitempty"`
	Decision             string         `json:"decision,omitempty"`
	Choice               string         `json:"choice,omitempty"`
	Outcome              *FolderOutcome `json:"outcome,omitempty"`
	RequestID            string         `json:"request_id,omitempty"`
	CreatedAt            time.Time      `json:"created_at"`
	DecidedAt            *time.Time     `json:"decided_at,omitempty"`
	LaterUntil           *time.Time     `json:"later_until,omitempty"`
	ResolvedAt           *time.Time     `json:"resolved_at,omitempty"`
}

// FolderDecision is the durable record of one answer (FR23).
type FolderDecision struct {
	OfferID      string    `json:"offer_id"`
	FolderKey    string    `json:"folder_key"`
	CandidateKey string    `json:"candidate_key"`
	Name         string    `json:"name"`
	Verdict      string    `json:"verdict"`
	Decision     string    `json:"decision"`
	Choice       string    `json:"choice,omitempty"`
	At           time.Time `json:"at"`
}

// FolderDomainDecline is scoped to a capability domain, not a path. A first
// single-project no can revive exactly once on a stronger portfolio signal.
type FolderDomainDecline struct {
	Domain        string    `json:"domain"`
	FirstEvidence string    `json:"first_evidence"` // single_project | portfolio
	Count         int       `json:"count"`          // one or two; two is final
	At            time.Time `json:"at"`
}

// FolderTombstone is a durable "never ask about this again" (FR23).
type FolderTombstone struct {
	Key       string    `json:"key"`
	Name      string    `json:"name"`
	CreatedAt time.Time `json:"created_at"`
}

// FolderReceipt makes a retried click return the earlier result (FR33).
type FolderReceipt struct {
	RequestID string    `json:"request_id"`
	OfferID   string    `json:"offer_id"`
	Action    string    `json:"action"`
	At        time.Time `json:"at"`
}

// FolderDigestDocument is the sidecar's content.
type FolderDigestDocument struct {
	SchemaVersion  int                   `json:"schema_version"`
	Version        int64                 `json:"version"`
	Owner          KnowledgeOwner        `json:"owner"`
	Offers         []FolderOffer         `json:"offers,omitempty"`
	Decisions      []FolderDecision      `json:"decisions,omitempty"`
	Tombstones     []FolderTombstone     `json:"tombstones,omitempty"`
	DomainDeclines []FolderDomainDecline `json:"domain_declines,omitempty"`
	Receipts       []FolderReceipt       `json:"receipts,omitempty"`
	// FirstPromptShownAt persists the one-time hand-over after HQ activation.
	FirstPromptShownAt *time.Time `json:"first_prompt_shown_at,omitempty"`
	Present            bool       `json:"-"`
}

// Pending returns the one pending offer, if any.
func (d *FolderDigestDocument) Pending() *FolderOffer {
	for i := range d.Offers {
		if d.Offers[i].Status == FolderOfferPending {
			return &d.Offers[i]
		}
	}
	return nil
}

// Awaiting returns the most recent confirmed offer whose setup is still in
// progress. Unlike a pending card, it must survive a page or server restart.
func (d *FolderDigestDocument) Awaiting() *FolderOffer {
	var latest *FolderOffer
	for i := range d.Offers {
		o := &d.Offers[i]
		if o.Status == FolderOfferAwaitingOutcome && (latest == nil || o.CreatedAt.After(latest.CreatedAt)) {
			latest = o
		}
	}
	return latest
}

// Offer finds an offer by id.
func (d *FolderDigestDocument) Offer(id string) *FolderOffer {
	for i := range d.Offers {
		if d.Offers[i].ID == id {
			return &d.Offers[i]
		}
	}
	return nil
}

// Tombstoned reports whether a candidate key was declined before.
func (d *FolderDigestDocument) Tombstoned(key string) bool {
	for _, t := range d.Tombstones {
		if t.Key == key {
			return true
		}
	}
	return false
}

// DeclineFor finds this domain's saved no, including a migrated app-card no.
func (d *FolderDigestDocument) DeclineFor(domain string) *FolderDomainDecline {
	for i := range d.DomainDeclines {
		if d.DomainDeclines[i].Domain == domain {
			return &d.DomainDeclines[i]
		}
	}
	return nil
}

// Receipt finds a stored request receipt.
func (d *FolderDigestDocument) Receipt(requestID string) *FolderReceipt {
	for i := range d.Receipts {
		if d.Receipts[i].RequestID == requestID {
			return &d.Receipts[i]
		}
	}
	return nil
}

// FolderKey is the durable identity of a folder: a hash of its canonical
// path, so records can be listed without exposing the directory layout
// (FR27).
func FolderKey(canonicalPath string) string {
	sum := sha256.Sum256([]byte(filepath.Clean(canonicalPath)))
	return hex.EncodeToString(sum[:])
}

var errFolderDigestInvalid = errors.New("personal assistant: folder digest document invalid")

// validateFolderDigest keeps the sidecar bounded and path-free.
func validateFolderDigest(doc FolderDigestDocument) error {
	if doc.SchemaVersion != FolderDigestSchemaVersion {
		return fmt.Errorf("%w: schema", errFolderDigestInvalid)
	}
	if len(doc.Offers) > folderDigestMaxOffers || len(doc.Decisions) > folderDigestMaxDecisions ||
		len(doc.Tombstones) > folderDigestMaxTombstones || len(doc.Receipts) > folderDigestMaxReceipts {
		return fmt.Errorf("%w: limits", errFolderDigestInvalid)
	}
	pending := 0
	seen := map[string]bool{}
	for _, offer := range doc.Offers {
		if offer.ID == "" || seen[offer.ID] {
			return fmt.Errorf("%w: offer id", errFolderDigestInvalid)
		}
		seen[offer.ID] = true
		switch offer.Status {
		case FolderOfferPending:
			pending++
		case FolderOfferLater, FolderOfferAwaitingOutcome, FolderOfferResolved, FolderOfferDeclined, FolderOfferClosed:
		default:
			return fmt.Errorf("%w: offer status", errFolderDigestInvalid)
		}
		if offer.CapabilitySuppressed && offer.CapabilityRevived {
			return fmt.Errorf("%w: capability offer", errFolderDigestInvalid)
		}
		if err := validateFolderKey(offer.FolderKey); err != nil {
			return err
		}
		if err := validateFolderName(offer.FolderName); err != nil {
			return err
		}
		if err := validateFolderReason(offer.Reason); err != nil {
			return err
		}
		if err := validateFolderCandidate(offer.Subject); err != nil {
			return err
		}
		if offer.Outcome != nil {
			if len(offer.Outcome.Receipt) > 32 {
				return fmt.Errorf("%w: receipt rows", errFolderDigestInvalid)
			}
			for _, row := range offer.Outcome.Receipt {
				if !strings.Contains("|workspace|folder|blueprint|agent|task|schedule|directory|", "|"+row.Kind+"|") ||
					row.Kind == "" || len(row.Name) > 512 || len(row.Detail) > 512 || len(row.Route) > 1024 ||
					(row.Route != "" && (!strings.HasPrefix(row.Route, "/") || strings.HasPrefix(row.Route, "//"))) {
					return fmt.Errorf("%w: receipt row", errFolderDigestInvalid)
				}
			}
		}
		if offer.Portfolio != nil {
			row, ok := folderdigest.CapabilityForShape(folderdigest.Shape(offer.Portfolio.Shape))
			if !ok || row.Offer == nil || row.Offer.HomeProviderKey != offer.Portfolio.ProviderKey ||
				offer.Portfolio.Projects < folderdigest.PortfolioMinProjects || offer.Portfolio.Projects > 5000 ||
				offer.Subject.Kind != FolderChoiceProject || !offer.Subject.IsRoot || offer.Subject.Shape != offer.Portfolio.Shape {
				return fmt.Errorf("%w: portfolio", errFolderDigestInvalid)
			}
		}
		if len(offer.Queue) > folderDigestMaxQueue {
			return fmt.Errorf("%w: queue", errFolderDigestInvalid)
		}
		for _, c := range offer.Queue {
			if err := validateFolderCandidate(c); err != nil {
				return err
			}
		}
	}
	if pending > 1 {
		return fmt.Errorf("%w: more than one pending offer", errFolderDigestInvalid)
	}
	if len(doc.DomainDeclines) > 16 {
		return fmt.Errorf("%w: domain declines", errFolderDigestInvalid)
	}
	domains := map[string]bool{}
	for _, decline := range doc.DomainDeclines {
		if decline.Domain == "" || len(decline.Domain) > 64 || domains[decline.Domain] ||
			(decline.FirstEvidence != "single_project" && decline.FirstEvidence != "portfolio") ||
			decline.Count < 1 || decline.Count > 2 || decline.At.IsZero() {
			return fmt.Errorf("%w: domain decline", errFolderDigestInvalid)
		}
		domains[decline.Domain] = true
	}
	for _, t := range doc.Tombstones {
		if err := validateFolderKey(t.Key); err != nil {
			return err
		}
		if err := validateFolderName(t.Name); err != nil {
			return err
		}
	}
	for _, r := range doc.Receipts {
		if r.RequestID == "" || len(r.RequestID) > 128 || r.OfferID == "" {
			return fmt.Errorf("%w: receipt", errFolderDigestInvalid)
		}
	}
	return nil
}

func validateFolderCandidate(c FolderCandidateRecord) error {
	if err := validateFolderKey(c.Key); err != nil {
		return err
	}
	if err := validateFolderName(c.Name); err != nil {
		return err
	}
	if c.RelPath != "" {
		if err := validateFolderName(c.RelPath); err != nil {
			return err
		}
	}
	if c.Kind != FolderChoiceProject && c.Kind != FolderChoiceTidy {
		return fmt.Errorf("%w: candidate kind", errFolderDigestInvalid)
	}
	return validateFolderReason(c.Reason)
}

func validateFolderKey(key string) error {
	if len(key) != 64 {
		return fmt.Errorf("%w: folder key", errFolderDigestInvalid)
	}
	if _, err := hex.DecodeString(key); err != nil {
		return fmt.Errorf("%w: folder key", errFolderDigestInvalid)
	}
	return nil
}

// validateFolderName accepts a base name only: no path separator, so a full
// path can never be stored by mistake.
func validateFolderName(name string) error {
	if name == "" || len(name) > folderDigestMaxName || strings.ContainsAny(name, "/\x00\r\n") {
		return fmt.Errorf("%w: folder name", errFolderDigestInvalid)
	}
	return nil
}

func validateFolderReason(reason string) error {
	if len(reason) > folderDigestMaxReason || strings.ContainsAny(reason, "/\x00\r\n") {
		return fmt.Errorf("%w: reason", errFolderDigestInvalid)
	}
	return nil
}

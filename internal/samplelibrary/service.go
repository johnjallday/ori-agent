package samplelibrary

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/google/uuid"
	"github.com/johnjallday/ori-agent/internal/pathselection"
	"github.com/johnjallday/ori-agent/internal/specialistevents"
	"github.com/johnjallday/ori-agent/internal/workspace"
)

const (
	MaxRoots                = 8
	MaxDepth                = 16
	MaxVisited              = 200000
	MaxEntries              = 100000
	MaxDirectories          = 20000
	MaxLocatorBytes         = 2048
	MaxComponentBytes       = 255
	MaxIssueExamples        = 256
	MaxHashFileBytes  int64 = 512 << 20
	MaxHashTotalBytes int64 = 2 << 30
)

var SupportedExtensions = []string{".aif", ".aiff", ".flac", ".m4a", ".mp3", ".ogg", ".wav", ".wave"}
var (
	ErrInvalidRoot      = errors.New("sample_root_invalid")
	ErrRootConflict     = errors.New("sample_root_conflict")
	ErrRootMissing      = errors.New("sample_root_missing")
	ErrRootChanged      = errors.New("sample_root_changed")
	ErrPermissionDenied = errors.New("sample_permission_denied")
	ErrScanInProgress   = errors.New("sample_scan_in_progress")
	ErrOperationFailed  = errors.New("sample_operation_failed")
)

type Limits struct {
	Depth, Visited, Entries, Directories int
	WallTime                             time.Duration
}

func ProductionLimits() Limits {
	return Limits{MaxDepth, MaxVisited, MaxEntries, MaxDirectories, 60 * time.Second}
}

type RootReview struct {
	Token               string    `json:"token"`
	HomeWorkspaceID     string    `json:"home_workspace_id"`
	ExactPath           string    `json:"exact_path"`
	CatalogRevision     int64     `json:"catalog_revision"`
	ExpiresAt           time.Time `json:"expires_at"`
	SupportedExtensions []string  `json:"supported_extensions"`
	Limits              Limits    `json:"limits"`
	Disclosure          []string  `json:"disclosure"`
}
type RevocationReview struct {
	Token           string    `json:"token"`
	HomeWorkspaceID string    `json:"home_workspace_id"`
	RootID          string    `json:"root_id"`
	ExactPath       string    `json:"exact_path"`
	CatalogRevision int64     `json:"catalog_revision"`
	RootRevision    int64     `json:"root_revision"`
	EntryCount      int       `json:"entry_count"`
	ExpiresAt       time.Time `json:"expires_at"`
	Disclosure      []string  `json:"disclosure"`
}

type AnalysisReview struct {
	Token               string    `json:"token"`
	HomeWorkspaceID     string    `json:"home_workspace_id"`
	RootID              string    `json:"root_id"`
	HashEnabled         bool      `json:"hash_enabled"`
	EmbeddedTagsEnabled bool      `json:"embedded_tags_enabled"`
	CatalogRevision     int64     `json:"catalog_revision"`
	RootRevision        int64     `json:"root_revision"`
	ExpiresAt           time.Time `json:"expires_at"`
	Disclosure          []string  `json:"disclosure"`
}

type SearchOptions struct {
	Query     string
	Extension string
	Sort      string
	Direction string
	Limit     int
}
type SearchResult struct {
	CatalogRevision int64   `json:"catalog_revision"`
	Roots           []Root  `json:"roots"`
	Entries         []Entry `json:"entries"`
	Complete        bool    `json:"complete"`
}

type IndexResult struct {
	State   State       `json:"state"`
	Root    Root        `json:"root"`
	Receipt ScanReceipt `json:"receipt"`
	Issues  []string    `json:"issues,omitempty"`
}
type Service struct {
	store             *Store
	workspaces        workspace.Store
	selections        *pathselection.Store
	protected         []string
	limits            Limits
	now               func() time.Time
	mu                sync.Mutex
	scanning          map[string]context.CancelFunc
	copying           map[string]context.CancelFunc
	beforeCopyPromote func() error
}

func NewService(store *Store, workspaces workspace.Store, selections *pathselection.Store, protectedPaths ...string) *Service {
	return &Service{store: store, workspaces: workspaces, selections: selections, protected: protectedPaths, limits: ProductionLimits(), now: func() time.Time { return time.Now().UTC() }, scanning: map[string]context.CancelFunc{}, copying: map[string]context.CancelFunc{}}
}
func (s *Service) SetLimitsForTest(v Limits) { s.limits = v }

func (s *Service) Snapshot(ctx context.Context, homeID string) (State, []Root, error) {
	if err := s.authorizeHome(homeID); err != nil {
		return State{}, nil, err
	}
	state, err := s.store.Get(ctx, homeID)
	if err != nil {
		return State{}, nil, err
	}
	roots, err := s.store.Roots(ctx, homeID, false)
	return state, roots, err
}
func (s *Service) Entries(ctx context.Context, homeID, rootID string, limit int) ([]Entry, error) {
	if err := s.authorizeHome(homeID); err != nil {
		return nil, err
	}
	root, err := s.store.Root(ctx, homeID, rootID)
	if err != nil || root.State != "active" {
		return nil, ErrRootMissing
	}
	ws, err := s.workspaces.Get(homeID)
	if err != nil {
		return nil, ErrRootMissing
	}
	ref, err := ws.GetDirectoryReference(root.DirectoryReferenceID)
	if err != nil || ref.Purpose != "sample_library" {
		return nil, ErrRootMissing
	}
	_, fingerprint, err := s.validateRoot(ctx, ref.Path, root.ID)
	if err != nil {
		return nil, err
	}
	if fingerprint != root.DirectoryFingerprint {
		return nil, ErrRootChanged
	}
	entries, err := s.store.Entries(ctx, homeID, rootID, limit)
	for i := range entries {
		entries[i] = presentEntry(entries[i])
	}
	return entries, err
}

type CurationReview struct {
	Token           string    `json:"token"`
	ExpiresAt       time.Time `json:"expires_at"`
	Action          string    `json:"action"`
	CatalogRevision int64     `json:"catalog_revision"`
	ObjectRevision  int64     `json:"object_revision"`
	Disclosure      []string  `json:"disclosure"`
}

func (s *Service) saveCurationReview(ctx context.Context, homeID, kind, action, input string, catalogRevision, objectRevision int64, disclosure []string) (CurationReview, error) {
	now := s.now()
	review := RootReviewRecord{Token: uuid.NewString(), HomeWorkspaceID: homeID, InputDigest: input, DisclosureDigest: digestStrings(strings.Join(disclosure, "\n")), CatalogRevision: catalogRevision, RootRevision: objectRevision, CreatedAt: now, ExpiresAt: now.Add(15 * time.Minute)}
	if err := s.store.SaveCurationReview(ctx, kind, review); err != nil {
		return CurationReview{}, err
	}
	return CurationReview{Token: review.Token, ExpiresAt: review.ExpiresAt, Action: action, CatalogRevision: catalogRevision, ObjectRevision: objectRevision, Disclosure: disclosure}, nil
}
func (s *Service) consumeCurationReview(ctx context.Context, token, key string) error {
	res, err := s.store.db.ExecContext(ctx, `UPDATE sample_library_review_receipt SET consumed_at=?,consumed_by_idempotency_key=? WHERE token=? AND (consumed_at IS NULL OR consumed_by_idempotency_key=?)`, s.now(), key, token, key)
	if err != nil {
		return err
	}
	n, _ := res.RowsAffected()
	if n != 1 {
		return ErrRevisionConflict
	}
	return nil
}
func (s *Service) verifyCurationReview(ctx context.Context, homeID, token, kind, key, input string, catalogRevision, objectRevision int64, disclosure []string) error {
	review, err := s.store.Review(ctx, token, kind)
	if err != nil || review.HomeWorkspaceID != homeID {
		return ErrRevisionConflict
	}
	if review.ConsumedAt != nil && review.ConsumedBy != key {
		return ErrIdempotencyConflict
	}
	if review.ConsumedAt == nil && !review.ExpiresAt.After(s.now()) || review.CatalogRevision != catalogRevision || review.RootRevision != objectRevision || review.InputDigest != input || review.DisclosureDigest != digestStrings(strings.Join(disclosure, "\n")) {
		return ErrRevisionConflict
	}
	return nil
}
func (s *Service) ReviewCollection(ctx context.Context, homeID, name, note string, expectedCatalog int64) (CurationReview, error) {
	if err := s.authorizeHome(homeID); err != nil {
		return CurationReview{}, err
	}
	name = strings.TrimSpace(name)
	note = strings.TrimSpace(note)
	if name == "" || len([]byte(name)) > 255 || len([]byte(note)) > 2000 {
		return CurationReview{}, ErrOperationFailed
	}
	name = sanitize(name, 255)
	note = sanitize(note, 2000)
	input := digestStrings(homeID, name, note, fmt.Sprint(expectedCatalog))
	return s.saveCurationReview(ctx, homeID, "collection", "create_collection", input, expectedCatalog, 0, []string{fmt.Sprintf("Create collection “%s”.", name), "Creates catalog organization only; no source file changes."})
}
func (s *Service) CommitCollection(ctx context.Context, homeID, token, name, note, key string, expectedCatalog int64) (State, Collection, error) {
	name = sanitize(strings.TrimSpace(name), 255)
	note = sanitize(strings.TrimSpace(note), 2000)
	input := digestStrings(homeID, name, note, fmt.Sprint(expectedCatalog))
	disclosure := []string{fmt.Sprintf("Create collection “%s”.", name), "Creates catalog organization only; no source file changes."}
	if err := s.verifyCurationReview(ctx, homeID, token, "collection", key, input, expectedCatalog, 0, disclosure); err != nil {
		return State{}, Collection{}, err
	}
	state, item, err := s.CreateCollection(ctx, homeID, name, note, key, expectedCatalog)
	if err == nil && s.consumeCurationReview(ctx, token, key) != nil {
		return State{}, Collection{}, ErrOperationFailed
	}
	return state, item, err
}

func (s *Service) Collections(ctx context.Context, homeID string) ([]Collection, error) {
	if err := s.authorizeHome(homeID); err != nil {
		return nil, err
	}
	return s.store.Collections(ctx, homeID)
}
func (s *Service) CreateCollection(ctx context.Context, homeID, name, note, key string, expectedCatalog int64) (State, Collection, error) {
	if err := s.authorizeHome(homeID); err != nil {
		return State{}, Collection{}, err
	}
	name = strings.TrimSpace(name)
	note = strings.TrimSpace(note)
	if name == "" || len([]byte(name)) > 255 || len([]byte(note)) > 2000 || strings.TrimSpace(key) == "" {
		return State{}, Collection{}, ErrOperationFailed
	}
	name = sanitize(name, 255)
	note = sanitize(note, 2000)
	input := digestStrings(homeID, name, note, fmt.Sprint(expectedCatalog))
	now := s.now()
	collection := Collection{ID: uuid.NewSHA1(uuid.NameSpaceOID, []byte(homeID+"\x00collection\x00"+key)).String(), HomeWorkspaceID: homeID, Name: name, Note: note, Revision: 1, CreatedAt: now, UpdatedAt: now}
	return s.store.CreateCollection(ctx, collection, key, input, expectedCatalog)
}
func (s *Service) ReviewCollectionMember(ctx context.Context, homeID, collectionID, entryID string, expectedRevision int64) (CurationReview, error) {
	if err := s.authorizeHome(homeID); err != nil {
		return CurationReview{}, err
	}
	state, err := s.store.Get(ctx, homeID)
	if err != nil {
		return CurationReview{}, err
	}
	input := digestStrings(homeID, collectionID, entryID, fmt.Sprint(expectedRevision))
	disclosure := []string{"Add the selected active catalog entry to the selected collection.", "Creates no file copy and changes no source file."}
	return s.saveCurationReview(ctx, homeID, "collection", "add_collection_member", input, state.CatalogRevision, expectedRevision, disclosure)
}
func (s *Service) CommitCollectionMember(ctx context.Context, homeID, token, collectionID, entryID, key string, expectedRevision int64) (Collection, error) {
	state, err := s.store.Get(ctx, homeID)
	if err != nil {
		return Collection{}, err
	}
	input := digestStrings(homeID, collectionID, entryID, fmt.Sprint(expectedRevision))
	disclosure := []string{"Add the selected active catalog entry to the selected collection.", "Creates no file copy and changes no source file."}
	if err = s.verifyCurationReview(ctx, homeID, token, "collection", key, input, state.CatalogRevision, expectedRevision, disclosure); err != nil {
		return Collection{}, err
	}
	item, err := s.AddCollectionMember(ctx, homeID, collectionID, entryID, key, expectedRevision)
	if err == nil && s.consumeCurationReview(ctx, token, key) != nil {
		return Collection{}, ErrOperationFailed
	}
	return item, err
}

func (s *Service) AddCollectionMember(ctx context.Context, homeID, collectionID, entryID, key string, expectedRevision int64) (Collection, error) {
	if err := s.authorizeHome(homeID); err != nil {
		return Collection{}, err
	}
	if strings.TrimSpace(key) == "" {
		return Collection{}, ErrOperationFailed
	}
	input := digestStrings(homeID, collectionID, entryID, fmt.Sprint(expectedRevision))
	return s.store.AddCollectionMember(ctx, homeID, collectionID, entryID, key, input, expectedRevision, s.now())
}

func (s *Service) ReviewAnnotation(ctx context.Context, homeID, entryID string, tags []string, pack, source, license string, expectedCatalog, expectedRevision int64) (CurationReview, error) {
	if err := s.authorizeHome(homeID); err != nil {
		return CurationReview{}, err
	}
	normalized, pack, source, license, err := normalizeAnnotationInput(tags, pack, source, license)
	if err != nil {
		return CurationReview{}, err
	}
	input := digestStrings(homeID, entryID, strings.Join(normalized, "\x00"), pack, source, license, fmt.Sprint(expectedCatalog), fmt.Sprint(expectedRevision))
	return s.saveCurationReview(ctx, homeID, "annotation", "set_annotation", input, expectedCatalog, expectedRevision, []string{"Save only the displayed user tags and provenance/license notes.", "Changes no embedded tags and writes no source sidecar."})
}
func (s *Service) CommitAnnotation(ctx context.Context, homeID, token, entryID, key string, tags []string, pack, source, license string, expectedCatalog, expectedRevision int64) (State, Annotation, error) {
	normalized, pack, source, license, err := normalizeAnnotationInput(tags, pack, source, license)
	if err != nil {
		return State{}, Annotation{}, err
	}
	input := digestStrings(homeID, entryID, strings.Join(normalized, "\x00"), pack, source, license, fmt.Sprint(expectedCatalog), fmt.Sprint(expectedRevision))
	disclosure := []string{"Save only the displayed user tags and provenance/license notes.", "Changes no embedded tags and writes no source sidecar."}
	if err = s.verifyCurationReview(ctx, homeID, token, "annotation", key, input, expectedCatalog, expectedRevision, disclosure); err != nil {
		return State{}, Annotation{}, err
	}
	state, item, err := s.SetAnnotation(ctx, homeID, entryID, key, normalized, pack, source, license, expectedCatalog, expectedRevision)
	if err == nil && s.consumeCurationReview(ctx, token, key) != nil {
		return State{}, Annotation{}, ErrOperationFailed
	}
	return state, item, err
}
func normalizeAnnotationInput(tags []string, pack, source, license string) ([]string, string, string, string, error) {
	if len(tags) > 32 || len([]byte(pack)) > 2000 || len([]byte(source)) > 2000 || len([]byte(license)) > 2000 {
		return nil, "", "", "", ErrOperationFailed
	}
	normalized := make([]string, 0, len(tags))
	seen := map[string]bool{}
	for _, tag := range tags {
		tag = strings.TrimSpace(tag)
		if tag == "" || len([]byte(tag)) > 64 {
			return nil, "", "", "", ErrOperationFailed
		}
		tag = sanitize(tag, 64)
		lookup := strings.ToLower(tag)
		if !seen[lookup] {
			seen[lookup] = true
			normalized = append(normalized, tag)
		}
	}
	sort.Slice(normalized, func(i, j int) bool { return strings.ToLower(normalized[i]) < strings.ToLower(normalized[j]) })
	return normalized, sanitize(pack, 2000), sanitize(source, 2000), sanitize(license, 2000), nil
}

func (s *Service) SetAnnotation(ctx context.Context, homeID, entryID, key string, tags []string, pack, source, license string, expectedCatalog, expectedRevision int64) (State, Annotation, error) {
	if err := s.authorizeHome(homeID); err != nil {
		return State{}, Annotation{}, err
	}
	if len(tags) > 32 || len([]byte(pack)) > 2000 || len([]byte(source)) > 2000 || len([]byte(license)) > 2000 || strings.TrimSpace(key) == "" {
		return State{}, Annotation{}, ErrOperationFailed
	}
	normalized := make([]string, 0, len(tags))
	seen := map[string]bool{}
	for _, tag := range tags {
		tag = strings.TrimSpace(tag)
		if tag == "" || len([]byte(tag)) > 64 {
			return State{}, Annotation{}, ErrOperationFailed
		}
		tag = sanitize(tag, 64)
		lookup := strings.ToLower(tag)
		if !seen[lookup] {
			seen[lookup] = true
			normalized = append(normalized, tag)
		}
	}
	sort.Slice(normalized, func(i, j int) bool { return strings.ToLower(normalized[i]) < strings.ToLower(normalized[j]) })
	pack = sanitize(pack, 2000)
	source = sanitize(source, 2000)
	license = sanitize(license, 2000)
	input := digestStrings(homeID, entryID, strings.Join(normalized, "\x00"), pack, source, license, fmt.Sprint(expectedCatalog), fmt.Sprint(expectedRevision))
	return s.store.SetAnnotation(ctx, homeID, Annotation{EntryID: entryID, UserTags: normalized, PackNote: pack, SourceNote: source, LicenseNote: license, UpdatedAt: s.now()}, key, input, expectedCatalog, expectedRevision)
}

func (s *Service) Search(ctx context.Context, homeID, query string, limit int) (SearchResult, error) {
	return s.SearchWithOptions(ctx, homeID, SearchOptions{Query: query, Limit: limit})
}
func (s *Service) SearchForProject(ctx context.Context, projectID string, options SearchOptions) (SearchResult, error) {
	homeID, err := s.homeForProject(projectID)
	if err != nil {
		return SearchResult{}, err
	}
	return s.SearchWithOptions(ctx, homeID, options)
}
func (s *Service) homeForProject(projectID string) (string, error) {
	child, err := s.workspaces.Get(strings.TrimSpace(projectID))
	if err != nil {
		return "", ErrOperationFailed
	}
	link := child.GetAssistantProjectLink()
	if link == nil {
		return "", ErrOperationFailed
	}
	home, err := s.workspaces.Get(link.StationWorkspaceID)
	if err != nil {
		return "", ErrOperationFailed
	}
	state := home.GetAssistantProgramState()
	if state == nil || link.ID != workspace.AssistantProjectLinkID(home.ID, child.ID) || link.Key.Normalize() != state.Key.Normalize() || !containsString(state.LinkedProjectIDs, child.ID) {
		return "", ErrRevisionConflict
	}
	return home.ID, nil
}

func (s *Service) SearchWithOptions(ctx context.Context, homeID string, options SearchOptions) (SearchResult, error) {
	if err := s.authorizeHome(homeID); err != nil {
		return SearchResult{}, err
	}
	query := strings.TrimSpace(options.Query)
	if len([]byte(query)) > 200 {
		return SearchResult{}, ErrOperationFailed
	}
	query = sanitize(query, 200)
	extension := strings.ToLower(strings.TrimSpace(options.Extension))
	if extension != "" && !supported(extension) {
		return SearchResult{}, ErrOperationFailed
	}
	sortKey := strings.ToLower(strings.TrimSpace(options.Sort))
	if sortKey == "" {
		sortKey = "name"
	}
	if sortKey != "name" && sortKey != "modified" && sortKey != "size" {
		return SearchResult{}, ErrOperationFailed
	}
	direction := strings.ToLower(strings.TrimSpace(options.Direction))
	if direction == "" {
		direction = "asc"
	}
	if direction != "asc" && direction != "desc" {
		return SearchResult{}, ErrOperationFailed
	}
	limit := options.Limit
	state, err := s.store.Get(ctx, homeID)
	if err != nil {
		return SearchResult{}, err
	}
	roots, err := s.store.Roots(ctx, homeID, true)
	if err != nil {
		return SearchResult{}, err
	}
	valid := make([]Root, 0, len(roots))
	ids := make([]string, 0, len(roots))
	complete := true
	ws, err := s.workspaces.Get(homeID)
	if err != nil {
		return SearchResult{}, ErrOperationFailed
	}
	for _, root := range roots {
		ref, refErr := ws.GetDirectoryReference(root.DirectoryReferenceID)
		if refErr != nil {
			complete = false
			continue
		}
		_, fingerprint, checkErr := s.validateRoot(ctx, ref.Path, root.ID)
		if checkErr != nil || fingerprint != root.DirectoryFingerprint {
			complete = false
			continue
		}
		valid = append(valid, root)
		ids = append(ids, root.ID)
		if root.Completeness != "complete" {
			complete = false
		}
	}
	entries, err := s.store.SearchEntries(ctx, homeID, query, extension, sortKey, direction, ids, limit)
	if err != nil {
		return SearchResult{}, err
	}
	for i := range entries {
		entries[i] = presentEntry(entries[i])
	}
	return SearchResult{state.CatalogRevision, valid, entries, complete}, nil
}

func presentEntry(entry Entry) Entry {
	entry.Filename = sanitize(entry.Filename, MaxComponentBytes)
	entry.RelativeLocator = sanitize(entry.RelativeLocator, MaxLocatorBytes)
	entry.Content = normalizeFacts(entry.Content)
	return entry
}

func (s *Service) ReviewRoot(ctx context.Context, homeID, selectionToken string) (RootReview, error) {
	if err := s.authorizeHome(homeID); err != nil {
		return RootReview{}, err
	}
	state, err := s.store.Ensure(ctx, homeID)
	if err != nil {
		return RootReview{}, err
	}
	if _, err = s.workspaces.Get(homeID); err != nil {
		return RootReview{}, ErrInvalidRoot
	}
	path, err := s.selections.Resolve(selectionToken)
	if err != nil {
		return RootReview{}, ErrInvalidRoot
	}
	canonical, fingerprint, err := s.validateRoot(ctx, path, "")
	if err != nil {
		return RootReview{}, err
	}
	roots, err := s.store.Roots(ctx, homeID, true)
	if err != nil {
		return RootReview{}, err
	}
	if len(roots) >= MaxRoots {
		return RootReview{}, ErrRootConflict
	}
	disclosure := []string{"Connection performs no scan and registers no watcher.", "Indexing is recursive within the published fixed bounds.", "Metadata is limited to filename, relative locator, extension, size, and filesystem times.", "Agents and linked projects receive no folder grant.", "Revocation removes catalog access but never changes source files."}
	inputDigest := digestStrings(homeID, selectionToken, fingerprint, fmt.Sprint(state.CatalogRevision))
	token := uuid.NewString()
	created := s.now()
	expires := created.Add(15 * time.Minute)
	if err := s.store.SaveRootReview(ctx, RootReviewRecord{Token: token, HomeWorkspaceID: homeID, SelectionDigest: digestStrings(selectionToken), DirectoryFingerprint: fingerprint, InputDigest: inputDigest, DisclosureDigest: digestStrings(strings.Join(disclosure, "\n")), CatalogRevision: state.CatalogRevision, CreatedAt: created, ExpiresAt: expires}); err != nil {
		return RootReview{}, err
	}
	return RootReview{Token: token, HomeWorkspaceID: homeID, ExactPath: canonical, CatalogRevision: state.CatalogRevision, ExpiresAt: expires, SupportedExtensions: append([]string(nil), SupportedExtensions...), Limits: s.limits, Disclosure: disclosure}, nil
}

func (s *Service) CommitRoot(ctx context.Context, homeID, reviewToken, selectionToken, idempotency string) (_ State, _ Root, resultErr error) {
	defer func() {
		recordSampleFailure(specialistevents.SampleRootOutcome, eventActionConnectRoot, resultErr)
	}()
	if err := s.authorizeHome(homeID); err != nil {
		return State{}, Root{}, err
	}
	if strings.TrimSpace(idempotency) == "" {
		return State{}, Root{}, ErrOperationFailed
	}
	review, err := s.store.RootReview(ctx, reviewToken)
	if err != nil || review.HomeWorkspaceID != homeID || review.SelectionDigest != digestStrings(selectionToken) {
		return State{}, Root{}, ErrRevisionConflict
	}
	if existing, ok, replayErr := s.store.ConnectedRootByKey(ctx, homeID, idempotency, review.InputDigest); replayErr != nil {
		return State{}, Root{}, replayErr
	} else if ok {
		state, stateErr := s.store.Get(ctx, homeID)
		return state, existing, stateErr
	}
	if review.ConsumedAt != nil || !review.ExpiresAt.After(s.now()) {
		return State{}, Root{}, ErrRevisionConflict
	}
	path, err := s.selections.Resolve(selectionToken)
	if err != nil {
		return State{}, Root{}, ErrInvalidRoot
	}
	canonical, fingerprint, err := s.validateRoot(ctx, path, "")
	if err != nil {
		return State{}, Root{}, err
	}
	if fingerprint != review.DirectoryFingerprint {
		return State{}, Root{}, ErrRootChanged
	}
	state, err := s.store.Get(ctx, homeID)
	if err != nil || state.CatalogRevision != review.CatalogRevision {
		return State{}, Root{}, ErrRevisionConflict
	}
	now := s.now()
	root := Root{ID: uuid.NewString(), HomeWorkspaceID: homeID, DirectoryReferenceID: uuid.NewString(), DirectoryFingerprint: fingerprint, State: "active", Revision: 1, Completeness: "not_indexed", CreatedAt: now, UpdatedAt: now}
	err = s.workspaces.Update(homeID, func(ws *workspace.Workspace) error {
		if err := ws.AddDirectoryReference(workspace.DirectoryReference{ID: root.DirectoryReferenceID, Name: filepath.Base(canonical), Path: canonical, Purpose: "sample_library"}); err != nil {
			return err
		}
		if !ws.RecordCapabilityResource(workspace.CapabilitySampleLibrary, workspace.CapabilityResource{Kind: workspace.ResourceDirectoryReference, ID: root.DirectoryReferenceID}) {
			return ErrOperationFailed
		}
		return nil
	})
	if err != nil {
		return State{}, Root{}, ErrOperationFailed
	}
	saved, err := s.store.AddRoot(ctx, root, reviewToken, idempotency, review.InputDigest, state.CatalogRevision, now)
	if err != nil {
		_ = s.workspaces.Update(homeID, func(ws *workspace.Workspace) error {
			ws.ForgetCapabilityResource(workspace.CapabilitySampleLibrary, workspace.ResourceDirectoryReference, root.DirectoryReferenceID)
			for i := range ws.DirectoryReferences {
				if ws.DirectoryReferences[i].ID == root.DirectoryReferenceID {
					ws.DirectoryReferences = append(ws.DirectoryReferences[:i], ws.DirectoryReferences[i+1:]...)
					break
				}
			}
			return nil
		})
		return State{}, Root{}, err
	}
	recordSampleEvent(specialistevents.SampleRootOutcome, eventActionConnectRoot, specialistevents.OutcomeSucceeded, 1)
	return saved, root, nil
}

func (s *Service) ReviewRevocation(ctx context.Context, homeID, rootID string) (RevocationReview, error) {
	if err := s.authorizeHome(homeID); err != nil {
		return RevocationReview{}, err
	}
	state, err := s.store.Get(ctx, homeID)
	if err != nil {
		return RevocationReview{}, err
	}
	root, err := s.store.Root(ctx, homeID, rootID)
	if err != nil || root.State != "active" {
		return RevocationReview{}, ErrRootMissing
	}
	ws, err := s.workspaces.Get(homeID)
	if err != nil {
		return RevocationReview{}, ErrRootMissing
	}
	ref, err := ws.GetDirectoryReference(root.DirectoryReferenceID)
	if err != nil {
		return RevocationReview{}, ErrRootMissing
	}
	canonical, fingerprint, err := s.validateRoot(ctx, ref.Path, rootID)
	if err != nil {
		return RevocationReview{}, err
	}
	if fingerprint != root.DirectoryFingerprint {
		return RevocationReview{}, ErrRootChanged
	}
	entryCount, err := s.store.EntryCount(ctx, homeID, rootID)
	if err != nil {
		return RevocationReview{}, err
	}
	disclosure := []string{"Revocation stops catalog access and removes this exact Directory Reference.", "Source files and folders are never opened, edited, moved, or deleted.", "Confirmed child copies and child-owned provenance are preserved.", "Collection memberships remain as unavailable entry identifiers."}
	created := s.now()
	token := uuid.NewString()
	input := digestStrings(homeID, rootID, fingerprint, fmt.Sprint(state.CatalogRevision), fmt.Sprint(root.Revision))
	if err = s.store.SaveRevokeReview(ctx, RootReviewRecord{Token: token, HomeWorkspaceID: homeID, DirectoryFingerprint: fingerprint, InputDigest: input, DisclosureDigest: digestStrings(strings.Join(disclosure, "\n")), CatalogRevision: state.CatalogRevision, RootRevision: root.Revision, CreatedAt: created, ExpiresAt: created.Add(15 * time.Minute)}); err != nil {
		return RevocationReview{}, err
	}
	return RevocationReview{token, homeID, rootID, canonical, state.CatalogRevision, root.Revision, entryCount, created.Add(15 * time.Minute), disclosure}, nil
}
func (s *Service) CommitRevocation(ctx context.Context, homeID, rootID, reviewToken, key string) (_ State, _ Root, resultErr error) {
	defer func() {
		recordSampleFailure(specialistevents.SampleRootOutcome, eventActionRevokeRoot, resultErr)
	}()
	if err := s.authorizeHome(homeID); err != nil {
		return State{}, Root{}, err
	}
	if strings.TrimSpace(key) == "" {
		return State{}, Root{}, ErrOperationFailed
	}
	review, err := s.store.Review(ctx, reviewToken, "revoke")
	if err != nil || review.HomeWorkspaceID != homeID {
		return State{}, Root{}, ErrRevisionConflict
	}
	input := digestStrings(homeID, rootID, review.DirectoryFingerprint, fmt.Sprint(review.CatalogRevision), fmt.Sprint(review.RootRevision))
	if input != review.InputDigest {
		return State{}, Root{}, ErrRevisionConflict
	}
	if ok, replayErr := s.store.RevokeByKey(ctx, homeID, rootID, key, input); replayErr != nil {
		return State{}, Root{}, replayErr
	} else if ok {
		state, e := s.store.Get(ctx, homeID)
		if e != nil {
			return State{}, Root{}, e
		}
		root, e := s.store.Root(ctx, homeID, rootID)
		return state, root, e
	}
	if review.ConsumedAt != nil || !review.ExpiresAt.After(s.now()) {
		return State{}, Root{}, ErrRevisionConflict
	}
	root, err := s.store.Root(ctx, homeID, rootID)
	if err != nil || root.DirectoryFingerprint != review.DirectoryFingerprint {
		return State{}, Root{}, ErrRootChanged
	}
	if err = s.revalidateRootRecord(ctx, homeID, root); err != nil {
		return State{}, Root{}, err
	}
	s.cancelHomeScans(homeID)
	state, root, err := s.store.RevokeRoot(ctx, homeID, rootID, reviewToken, key, input, review.CatalogRevision, review.RootRevision, s.now())
	if err != nil {
		return State{}, Root{}, err
	}
	if err = s.workspaces.Update(homeID, func(ws *workspace.Workspace) error {
		ws.ForgetCapabilityResource(workspace.CapabilitySampleLibrary, workspace.ResourceDirectoryReference, root.DirectoryReferenceID)
		for i := range ws.DirectoryReferences {
			if ws.DirectoryReferences[i].ID == root.DirectoryReferenceID {
				ws.DirectoryReferences = append(ws.DirectoryReferences[:i], ws.DirectoryReferences[i+1:]...)
				break
			}
		}
		return nil
	}); err != nil {
		return state, root, ErrOperationFailed
	}
	recordSampleEvent(specialistevents.SampleRootOutcome, eventActionRevokeRoot, specialistevents.OutcomeRevoked, 1)
	return state, root, nil
}

func (s *Service) ReviewAnalysis(ctx context.Context, homeID, rootID string, hashEnabled, tagsEnabled bool) (AnalysisReview, error) {
	if err := s.authorizeHome(homeID); err != nil {
		return AnalysisReview{}, err
	}
	state, err := s.store.Get(ctx, homeID)
	if err != nil {
		return AnalysisReview{}, err
	}
	root, err := s.store.Root(ctx, homeID, rootID)
	if err != nil || root.State != "active" {
		return AnalysisReview{}, ErrRootMissing
	}
	ws, err := s.workspaces.Get(homeID)
	if err != nil {
		return AnalysisReview{}, ErrRootMissing
	}
	ref, err := ws.GetDirectoryReference(root.DirectoryReferenceID)
	if err != nil {
		return AnalysisReview{}, ErrRootMissing
	}
	_, fingerprint, err := s.validateRoot(ctx, ref.Path, rootID)
	if err != nil {
		return AnalysisReview{}, err
	}
	if fingerprint != root.DirectoryFingerprint {
		return AnalysisReview{}, ErrRootChanged
	}
	disclosure := []string{"Changing consent does not open or scan any file.", "SHA-256 reads are limited to 512 MiB per file and 2 GiB per refresh.", "Embedded-tag reads are limited to supported compiled readers and 2 MiB per file.", "No audio decoding, BPM, key, waveform, transcription, audition, upload, or execution occurs.", "Revoking a reader deletes its active derived catalog values."}
	created := s.now()
	token := uuid.NewString()
	input := digestStrings(homeID, rootID, fmt.Sprint(hashEnabled), fmt.Sprint(tagsEnabled), fmt.Sprint(state.CatalogRevision), fmt.Sprint(root.Revision))
	if err = s.store.SaveAnalysisReview(ctx, RootReviewRecord{Token: token, HomeWorkspaceID: homeID, DirectoryFingerprint: fingerprint, InputDigest: input, DisclosureDigest: digestStrings(strings.Join(disclosure, "\n")), CatalogRevision: state.CatalogRevision, RootRevision: root.Revision, CreatedAt: created, ExpiresAt: created.Add(15 * time.Minute)}); err != nil {
		return AnalysisReview{}, err
	}
	return AnalysisReview{token, homeID, rootID, hashEnabled, tagsEnabled, state.CatalogRevision, root.Revision, created.Add(15 * time.Minute), disclosure}, nil
}
func (s *Service) CommitAnalysis(ctx context.Context, homeID, rootID, reviewToken, key string, hashEnabled, tagsEnabled bool) (_ State, _ Root, resultErr error) {
	action := eventActionDisableAnalysis
	if hashEnabled || tagsEnabled {
		action = eventActionEnableAnalysis
	}
	defer func() {
		recordSampleFailure(specialistevents.SampleAnalysisOutcome, action, resultErr)
	}()
	if err := s.authorizeHome(homeID); err != nil {
		return State{}, Root{}, err
	}
	if strings.TrimSpace(key) == "" {
		return State{}, Root{}, ErrOperationFailed
	}
	review, err := s.store.Review(ctx, reviewToken, "analysis")
	if err != nil || review.HomeWorkspaceID != homeID {
		return State{}, Root{}, ErrRevisionConflict
	}
	input := digestStrings(homeID, rootID, fmt.Sprint(hashEnabled), fmt.Sprint(tagsEnabled), fmt.Sprint(review.CatalogRevision), fmt.Sprint(review.RootRevision))
	if input != review.InputDigest {
		return State{}, Root{}, ErrRevisionConflict
	}
	if ok, replayErr := s.store.AnalysisByKey(ctx, homeID, rootID, key, input); replayErr != nil {
		return State{}, Root{}, replayErr
	} else if ok {
		state, e := s.store.Get(ctx, homeID)
		if e != nil {
			return State{}, Root{}, e
		}
		root, e := s.store.Root(ctx, homeID, rootID)
		return state, root, e
	}
	if review.ConsumedAt != nil || !review.ExpiresAt.After(s.now()) {
		return State{}, Root{}, ErrRevisionConflict
	}
	root, err := s.store.Root(ctx, homeID, rootID)
	if err != nil || root.DirectoryFingerprint != review.DirectoryFingerprint {
		return State{}, Root{}, ErrRootChanged
	}
	if err = s.revalidateRootRecord(ctx, homeID, root); err != nil {
		return State{}, Root{}, err
	}
	saved, savedRoot, err := s.store.SetAnalysis(ctx, homeID, rootID, reviewToken, key, input, hashEnabled, tagsEnabled, review.CatalogRevision, review.RootRevision, s.now())
	if err != nil {
		return State{}, Root{}, err
	}
	outcome := specialistevents.OutcomeRevoked
	if hashEnabled || tagsEnabled {
		outcome = specialistevents.OutcomeSucceeded
	}
	recordSampleEvent(specialistevents.SampleAnalysisOutcome, action, outcome, 1)
	return saved, savedRoot, nil
}

func (s *Service) Index(ctx context.Context, homeID, rootID, idempotency string, expectedCatalog, expectedRoot int64) (_ IndexResult, resultErr error) {
	defer func() {
		recordSampleFailure(specialistevents.SampleRootOutcome, eventActionIndexRoot, resultErr)
	}()
	if err := s.authorizeHome(homeID); err != nil {
		return IndexResult{}, err
	}
	if strings.TrimSpace(idempotency) == "" {
		return IndexResult{}, ErrOperationFailed
	}
	key := homeID + ":" + rootID
	s.mu.Lock()
	if _, busy := s.scanning[key]; busy {
		s.mu.Unlock()
		return IndexResult{}, ErrScanInProgress
	}
	operationCtx, operationCancel := context.WithCancel(ctx)
	s.scanning[key] = operationCancel
	s.mu.Unlock()
	defer func() { operationCancel(); s.mu.Lock(); delete(s.scanning, key); s.mu.Unlock() }()
	inputDigest := digestStrings(homeID, rootID, idempotency, fmt.Sprint(expectedCatalog), fmt.Sprint(expectedRoot))
	if receipt, ok, replayErr := s.store.ScanByKey(ctx, homeID, rootID, idempotency, inputDigest); replayErr != nil {
		return IndexResult{}, replayErr
	} else if ok {
		state, stateErr := s.store.Get(ctx, homeID)
		if stateErr != nil {
			return IndexResult{}, stateErr
		}
		root, rootErr := s.store.Root(ctx, homeID, rootID)
		return IndexResult{State: state, Root: root, Receipt: receipt}, rootErr
	}
	state, err := s.store.Get(ctx, homeID)
	if err != nil {
		return IndexResult{}, err
	}
	root, err := s.store.Root(ctx, homeID, rootID)
	if err != nil || root.State != "active" {
		return IndexResult{}, ErrRootMissing
	}
	if state.CatalogRevision != expectedCatalog || root.Revision != expectedRoot {
		return IndexResult{}, ErrRevisionConflict
	}
	ws, err := s.workspaces.Get(homeID)
	if err != nil {
		return IndexResult{}, ErrRootMissing
	}
	ref, err := ws.GetDirectoryReference(root.DirectoryReferenceID)
	if err != nil || ref.Purpose != "sample_library" {
		return IndexResult{}, ErrRootMissing
	}
	canonical, fingerprint, err := s.validateRoot(ctx, ref.Path, root.ID)
	if err != nil {
		return IndexResult{}, err
	}
	if fingerprint != root.DirectoryFingerprint {
		return IndexResult{}, ErrRootChanged
	}
	claim := ScanReceipt{OperationID: idempotency, HomeWorkspaceID: homeID, RootID: rootID, InputDigest: inputDigest, Status: "claimed", CreatedAt: s.now()}
	if err = s.store.ClaimScan(ctx, claim, claim.CreatedAt.Add(s.limits.WallTime+time.Minute)); err != nil {
		if errors.Is(err, ErrOperationInProgress) {
			return IndexResult{}, ErrScanInProgress
		}
		return IndexResult{}, err
	}
	scanctx, cancel := context.WithTimeout(operationCtx, s.limits.WallTime)
	defer cancel()
	generation := root.Generation + 1
	entries, receipt, issues, safeStarted, err := s.scan(scanctx, canonical, homeID, root.ID, generation, idempotency, root.HashEnabled, root.TagsEnabled)
	if err != nil && !safeStarted {
		receipt.InputDigest = inputDigest
		_ = s.store.FailScan(ctx, receipt)
		return IndexResult{}, err
	}
	root.Generation = generation
	root.Completeness = receipt.Status
	if root.Completeness == "succeeded" {
		root.Completeness = "complete"
	}
	root.UpdatedAt = s.now()
	receipt.InputDigest = inputDigest
	saved, savedRoot, err := s.store.ReplaceGeneration(ctx, root, expectedCatalog, expectedRoot, entries, receipt)
	if err != nil {
		failed := receipt
		failed.Status = "failed"
		failed.ReasonCode = "sample_revision_conflict"
		_ = s.store.FailScan(ctx, failed)
		return IndexResult{}, err
	}
	recordSampleEvent(specialistevents.SampleRootOutcome, eventActionIndexRoot, specialistevents.OutcomeSucceeded, len(entries))
	return IndexResult{saved, savedRoot, receipt, issues}, nil
}

func (s *Service) scan(ctx context.Context, base, homeID, rootID string, generation int64, opID string, hashEnabled, tagsEnabled bool) ([]Entry, ScanReceipt, []string, bool, error) {
	now := s.now()
	receipt := ScanReceipt{OperationID: opID, HomeWorkspaceID: homeID, RootID: rootID, Status: "succeeded", CreatedAt: now}
	var entries []Entry
	var issues []string
	dirs := 0
	var hashedBytes int64
	safe := false
	partial := false
	var walk func(string, string, int) error
	walk = func(abs, rel string, depth int) error {
		if err := ctx.Err(); err != nil {
			partial = true
			return nil
		}
		if depth > s.limits.Depth {
			receipt.Skipped++
			partial = true
			return nil
		}
		if !containedWithoutSymlinks(base, abs) {
			receipt.Errors++
			partial = true
			if !safe {
				return ErrRootChanged
			}
			return nil
		}
		directory, openErr := openReadNoFollow(abs)
		if openErr != nil {
			receipt.Errors++
			partial = true
			if !safe {
				return ErrPermissionDenied
			}
			return nil
		}
		list, err := directory.ReadDir(-1)
		closeErr := directory.Close()
		if err == nil {
			err = closeErr
		}
		if err != nil {
			receipt.Errors++
			partial = true
			if !safe {
				return ErrPermissionDenied
			}
			return nil
		}
		safe = true
		sort.Slice(list, func(i, j int) bool { return list[i].Name() < list[j].Name() })
		for _, de := range list {
			if ctx.Err() != nil {
				partial = true
				break
			}
			if receipt.Visited >= s.limits.Visited {
				partial = true
				break
			}
			receipt.Visited++
			name := de.Name()
			if strings.HasPrefix(name, ".") || !validComponent(name) {
				receipt.Skipped++
				continue
			}
			childRel := filepath.ToSlash(filepath.Join(rel, name))
			if len([]byte(childRel)) > MaxLocatorBytes {
				receipt.Skipped++
				addIssue(&issues, childRel)
				continue
			}
			absChild := filepath.Join(abs, name)
			info, e := os.Lstat(absChild)
			if e != nil {
				receipt.Errors++
				partial = true
				addIssue(&issues, childRel)
				continue
			}
			if info.Mode()&os.ModeSymlink != 0 {
				receipt.Skipped++
				continue
			}
			if info.IsDir() {
				dirs++
				if dirs > s.limits.Directories {
					receipt.Skipped++
					partial = true
					continue
				}
				if e = walk(absChild, childRel, depth+1); e != nil {
					return e
				}
				continue
			}
			if !info.Mode().IsRegular() {
				receipt.Skipped++
				continue
			}
			ext := strings.ToLower(filepath.Ext(name))
			if !supported(ext) {
				receipt.Skipped++
				continue
			}
			if len(entries) >= s.limits.Entries {
				receipt.Skipped++
				partial = true
				continue
			}
			entry := Entry{ID: uuid.NewSHA1(uuid.NameSpaceOID, []byte(rootID+"\x00"+strings.ToLower(childRel))).String(), HomeWorkspaceID: homeID, RootID: rootID, Generation: generation, RelativeLocator: childRel, Filename: sanitize(name, MaxComponentBytes), Extension: ext, SizeBytes: info.Size(), ModifiedAt: info.ModTime().UTC()}
			if hashEnabled {
				if info.Size() > MaxHashFileBytes || hashedBytes+info.Size() > MaxHashTotalBytes {
					partial = true
					receipt.Skipped++
					addIssue(&issues, childRel)
				} else if value, hashErr := hashExactFile(ctx, absChild, info); hashErr != nil {
					partial = true
					receipt.Errors++
					addIssue(&issues, childRel)
				} else {
					entry.SHA256 = value
					hashedBytes += info.Size()
				}
			}
			if tagsEnabled {
				if reader := compiledTagReaders[ext]; reader != nil {
					facts, tagErr := reader(ctx, absChild, info)
					if tagErr != nil {
						partial = true
						receipt.Errors++
						addIssue(&issues, childRel)
					} else {
						entry.Content = facts
					}
				}
			}
			entries = append(entries, entry)
			receipt.Indexed++
		}
		return nil
	}
	err := walk(base, "", 0)
	done := s.now()
	receipt.CompletedAt = &done
	if err != nil {
		receipt.Status = "failed"
		receipt.ReasonCode = "sample_permission_denied"
		receipt.Errors++
		return nil, receipt, issues, safe, err
	}
	if partial {
		receipt.Status = "partial"
		receipt.ReasonCode = "sample_scan_partial"
	}
	return entries, receipt, issues, safe, nil
}

func (s *Service) OnHomeRemoved(homeID string) error {
	s.cancelHomeScans(homeID)
	_, err := s.store.Get(context.Background(), homeID)
	if errors.Is(err, ErrNotFound) {
		return nil
	}
	if err != nil {
		return ErrOperationFailed
	}
	if _, err = s.store.Disable(context.Background(), homeID, s.now()); err != nil {
		return ErrOperationFailed
	}
	return nil
}

func (s *Service) cancelHomeScans(homeID string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	prefix := homeID + ":"
	for key, cancel := range s.scanning {
		if strings.HasPrefix(key, prefix) {
			cancel()
		}
	}
	for key, cancel := range s.copying {
		if strings.HasPrefix(key, prefix) {
			cancel()
		}
	}
}

func (s *Service) revalidateRootRecord(ctx context.Context, homeID string, root Root) error {
	ws, err := s.workspaces.Get(homeID)
	if err != nil {
		return ErrRootMissing
	}
	ref, err := ws.GetDirectoryReference(root.DirectoryReferenceID)
	if err != nil || ref.Purpose != "sample_library" {
		return ErrRootMissing
	}
	_, fingerprint, err := s.validateRoot(ctx, ref.Path, root.ID)
	if err != nil {
		return err
	}
	if fingerprint != root.DirectoryFingerprint {
		return ErrRootChanged
	}
	return nil
}

func (s *Service) authorizeHome(homeID string) error {
	ws, err := s.workspaces.Get(strings.TrimSpace(homeID))
	if err != nil || ws == nil {
		return ErrOperationFailed
	}
	program := ws.GetAssistantProgramState()
	if program == nil || program.SchemaVersion != workspace.AssistantProgramSchemaVersion || program.Declaration == nil || !program.PluginAvailable || !ws.HasInstalledCapability(workspace.CapabilitySampleLibrary) {
		return ErrOperationFailed
	}
	for _, role := range program.Declaration.Roles {
		if role.Scope == workspace.AssistantRoleScopeHome && !role.Required && workspace.NormalizeCapabilityID(role.CapabilityID) == workspace.CapabilitySampleLibrary {
			return nil
		}
	}
	return ErrOperationFailed
}

func (s *Service) validateRoot(ctx context.Context, path, exceptRoot string) (string, string, error) {
	if err := ctx.Err(); err != nil {
		return "", "", ErrOperationFailed
	}
	if strings.TrimSpace(path) == "" || strings.ContainsRune(path, 0) || !filepath.IsAbs(path) {
		return "", "", ErrInvalidRoot
	}
	li, err := os.Lstat(path)
	if err != nil || li.Mode()&os.ModeSymlink != 0 || !li.IsDir() {
		return "", "", ErrInvalidRoot
	}
	canonical, err := filepath.EvalSymlinks(filepath.Clean(path))
	if err != nil {
		return "", "", ErrInvalidRoot
	}
	home, _ := os.UserHomeDir()
	volume := filepath.VolumeName(canonical) + string(os.PathSeparator)
	for _, blocked := range append([]string{home, volume}, s.protected...) {
		if blocked != "" && overlap(canonical, blocked) {
			return "", "", ErrRootConflict
		}
	}
	states, err := s.workspaces.ListActive()
	if err == nil {
		folderStore, _ := s.workspaces.(interface{ GetFolderPath(string) (string, error) })
		for _, ws := range states {
			if folderStore != nil {
				if managed, folderErr := folderStore.GetFolderPath(ws.ID); folderErr == nil && overlap(canonical, managed) {
					return "", "", ErrRootConflict
				}
			}
			for _, ref := range ws.DirectoryReferences {
				if ref.Purpose != "sample_library" {
					if overlap(canonical, ref.Path) {
						return "", "", ErrRootConflict
					}
					continue
				}
				roots, _ := s.store.Roots(ctx, ws.ID, true)
				for _, r := range roots {
					if r.ID != exceptRoot && r.DirectoryReferenceID == ref.ID && overlap(canonical, ref.Path) {
						return "", "", ErrRootConflict
					}
				}
			}
		}
	}
	return canonical, digestStrings(canonical, fileIdentity(li)), nil
}

func fileIdentity(info os.FileInfo) string {
	value := reflect.Indirect(reflect.ValueOf(info.Sys()))
	if value.IsValid() && value.Kind() == reflect.Struct {
		dev, ino := value.FieldByName("Dev"), value.FieldByName("Ino")
		if dev.IsValid() && ino.IsValid() && dev.CanUint() && ino.CanUint() {
			return fmt.Sprintf("%d:%d", dev.Uint(), ino.Uint())
		}
	}
	// Path plus stable directory type is the conservative fallback on platforms
	// whose FileInfo does not expose a device/inode identity.
	return info.Mode().Type().String()
}
func containedWithoutSymlinks(base, target string) bool {
	rel, err := filepath.Rel(base, target)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(os.PathSeparator)) {
		return false
	}
	current := base
	parts := strings.Split(filepath.Clean(rel), string(os.PathSeparator))
	if rel == "." {
		parts = nil
	}
	for _, part := range parts {
		if part == "" || part == "." || part == ".." {
			return false
		}
		current = filepath.Join(current, part)
		info, statErr := os.Lstat(current)
		if statErr != nil || info.Mode()&os.ModeSymlink != 0 || !info.IsDir() {
			return false
		}
	}
	return true
}

func supported(ext string) bool {
	i := sort.SearchStrings(SupportedExtensions, ext)
	return i < len(SupportedExtensions) && SupportedExtensions[i] == ext
}
func overlap(a, b string) bool {
	a = strings.ToLower(filepath.Clean(a))
	b = strings.ToLower(filepath.Clean(b))
	if a == b {
		return true
	}
	sep := string(os.PathSeparator)
	return strings.HasPrefix(a, b+sep) || strings.HasPrefix(b, a+sep)
}
func validComponent(v string) bool {
	return utf8.ValidString(v) && len([]byte(v)) <= MaxComponentBytes && v != "." && v != ".." && !strings.ContainsRune(v, 0)
}
func sanitize(v string, max int) string {
	v = strings.Map(func(r rune) rune {
		if r == 0 || r < 32 || (r >= 0x202A && r <= 0x202E) || (r >= 0x2066 && r <= 0x2069) {
			return -1
		}
		return r
	}, strings.ToValidUTF8(v, "�"))
	for len([]byte(v)) > max {
		v = v[:len(v)-1]
	}
	return v
}
func addIssue(dst *[]string, v string) {
	if len(*dst) < MaxIssueExamples {
		*dst = append(*dst, sanitize(v, MaxLocatorBytes))
	}
}
func hashExactFile(ctx context.Context, path string, expected os.FileInfo) (string, error) {
	file, err := openReadNoFollow(path)
	if err != nil {
		return "", err
	}
	defer func() { _ = file.Close() }()
	current, err := file.Stat()
	if err != nil || !current.Mode().IsRegular() || !os.SameFile(expected, current) || current.Size() != expected.Size() {
		return "", ErrRootChanged
	}
	hash := sha256.New()
	written, err := io.Copy(hash, &contextReader{ctx: ctx, reader: file})
	if err != nil || written != expected.Size() {
		return "", ErrOperationFailed
	}
	return hex.EncodeToString(hash.Sum(nil)), nil
}

type contextReader struct {
	ctx    context.Context
	reader io.Reader
}

func (r *contextReader) Read(buffer []byte) (int, error) {
	select {
	case <-r.ctx.Done():
		return 0, r.ctx.Err()
	default:
		return r.reader.Read(buffer)
	}
}

func digestStrings(parts ...string) string {
	h := sha256.New()
	for _, p := range parts {
		h.Write([]byte(p))
		h.Write([]byte{0})
	}
	return hex.EncodeToString(h.Sum(nil))
}

var _ fs.FileInfo

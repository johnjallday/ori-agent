package sessionhttp

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"

	"github.com/johnjallday/ori-agent/internal/projectlibrary"
	"github.com/johnjallday/ori-agent/internal/projecttemplates"
)

// The seams below let the folder card's one-consent collection setup drive, in
// process, the very reviews and commits the browser drives on the step-by-step
// path: the reviewed Home template (Create Group modal) and the Home library's
// initialize, connect and scan reviews. Each is a review then a commit; none is
// an HTTP route and none is an agent tool.

// ErrPortfolioSetupUnavailable means a seam the run needs is not wired or the
// Home cannot be read.
var ErrPortfolioSetupUnavailable = errors.New("collection setup is unavailable")

// PortfolioHomeReview is what reviewing the provider's Home template disclosed.
type PortfolioHomeReview struct {
	GroupTemplateID string
	Revision        string
	Name            string
	// TemplateID is the trusted template the Home is created from.
	TemplateID string
	Reuse      bool
	HomeID     string
	Token      string
}

// portfolioHomeTemplate is the one managed Home template whose provider is the
// reviewed Home provider — the Create Group modal's own selection.
func (h *Handler) portfolioHomeTemplate(ctx context.Context, providerPluginID string) (groupTemplateView, error) {
	listing := h.groupTemplateListing(ctx)
	if listing.CatalogUnavailable {
		return groupTemplateView{}, ErrPortfolioSetupUnavailable
	}
	var matches []groupTemplateView
	for _, entry := range listing.Templates {
		if entry.Kind == projecttemplates.GroupTemplateKindManaged && entry.Provider != nil && entry.Provider.PluginID == providerPluginID {
			matches = append(matches, entry)
		}
	}
	if len(matches) != 1 || matches[0].Representative == nil {
		return groupTemplateView{}, fmt.Errorf("%w: the reviewed Home template is missing or ambiguous", ErrPortfolioSetupUnavailable)
	}
	return matches[0], nil
}

// ReviewPortfolioHome reviews creating the provider's Home through the same
// handler the Create Group modal posts to.
func (h *Handler) ReviewPortfolioHome(ctx context.Context, providerPluginID string) (PortfolioHomeReview, error) {
	if h == nil {
		return PortfolioHomeReview{}, ErrPortfolioSetupUnavailable
	}
	entry, err := h.portfolioHomeTemplate(ctx, providerPluginID)
	if err != nil {
		return PortfolioHomeReview{}, err
	}
	name := strings.TrimSpace(entry.ProposedGroupName)
	if name == "" {
		name = strings.TrimSpace(entry.Name)
	}
	var response struct {
		Review groupTemplateHomeReview `json:"group_template_review"`
	}
	if err := h.postInProcess(ctx, "/api/workspaces/group-templates/review", groupTemplateHomeRequest{
		GroupTemplateID: entry.ID, Revision: entry.Revision, Name: name,
	}, h.ReviewGroupTemplateHome, &response); err != nil {
		return PortfolioHomeReview{}, err
	}
	return PortfolioHomeReview{
		GroupTemplateID: entry.ID, Revision: entry.Revision, Name: name, TemplateID: entry.Representative.ID,
		Reuse: response.Review.Reuse, HomeID: response.Review.HomeWorkspaceID, Token: response.Review.ReviewToken,
	}, nil
}

// CommitPortfolioHome commits a reviewed Home and returns the Home it made.
func (h *Handler) CommitPortfolioHome(ctx context.Context, review PortfolioHomeReview, key string) (string, error) {
	if h == nil {
		return "", ErrPortfolioSetupUnavailable
	}
	var response struct {
		GroupTemplate struct {
			HomeWorkspaceID        string `json:"home_workspace_id"`
			CreatedByThisOperation bool   `json:"created_by_this_operation"`
		} `json:"group_template"`
	}
	if err := h.postInProcess(ctx, "/api/workspaces/group-templates/commit", groupTemplateHomeRequest{
		GroupTemplateID: review.GroupTemplateID, Revision: review.Revision, Name: review.Name,
		GroupReviewToken: review.Token, IdempotencyKey: key,
	}, h.CommitGroupTemplateHome, &response); err != nil {
		return "", err
	}
	if !response.GroupTemplate.CreatedByThisOperation || strings.TrimSpace(response.GroupTemplate.HomeWorkspaceID) == "" {
		return "", fmt.Errorf("%w: the Home commit did not create the reviewed Home", ErrPortfolioSetupUnavailable)
	}
	return response.GroupTemplate.HomeWorkspaceID, nil
}

// postInProcess sends one JSON body to a handler as the browser would and
// decodes its success payload.
func (h *Handler) postInProcess(ctx context.Context, path string, body any, handle http.HandlerFunc, out any) error {
	encoded, err := json.Marshal(body)
	if err != nil {
		return err
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, path, bytes.NewReader(encoded))
	if err != nil {
		return err
	}
	request.Header.Set("Content-Type", "application/json")
	recorder := httptest.NewRecorder()
	handle(recorder, request)
	if recorder.Code < 200 || recorder.Code > 299 {
		return fmt.Errorf("%s refused (%d): %s", path, recorder.Code, createFailureMessage(recorder.Body.Bytes()))
	}
	return json.Unmarshal(recorder.Body.Bytes(), out)
}

// PortfolioLibraryState is where a Home's library stands for one folder.
type PortfolioLibraryState struct {
	Initialized bool
	Linked      int
	RootID      string
	Scanned     bool
	Listed      int
	Partial     bool
}

// portfolioLibraryScope is the owner's exact Home scope, derived from the Home
// itself as the library routes derive it.
func (h *Handler) portfolioLibraryScope(ownerUserID, homeID string) (projectlibrary.Scope, error) {
	if h == nil || h.workspaceTaskStore == nil || strings.TrimSpace(ownerUserID) == "" {
		return projectlibrary.Scope{}, ErrPortfolioSetupUnavailable
	}
	station, err := h.workspaceTaskStore.Get(strings.TrimSpace(homeID))
	if err != nil || station == nil || station.OwnerUserID != ownerUserID {
		return projectlibrary.Scope{}, ErrPortfolioSetupUnavailable
	}
	state := station.GetAssistantProgramState()
	if state == nil || state.Key.Normalize().OwnerUserID != ownerUserID {
		return projectlibrary.Scope{}, ErrPortfolioSetupUnavailable
	}
	key := state.Key.Normalize()
	return projectlibrary.Scope{OwnerUserID: ownerUserID, HomeID: station.ID, ProviderID: key.PluginID, ProgramID: key.ProgramID}, nil
}

// PortfolioLibraryState reads the Home's library for the folder: whether it is
// on, the active root that covers the folder, and that root's last listing.
func (h *Handler) PortfolioLibraryState(ownerUserID, homeID, folder string) (PortfolioLibraryState, error) {
	scope, err := h.portfolioLibraryScope(ownerUserID, homeID)
	if err != nil {
		return PortfolioLibraryState{}, err
	}
	doc, err := h.assistantLibraryStore().Read(scope)
	if errors.Is(err, projectlibrary.ErrNotInitialized) {
		station, getErr := h.workspaceTaskStore.Get(scope.HomeID)
		if getErr != nil || station == nil {
			return PortfolioLibraryState{}, ErrPortfolioSetupUnavailable
		}
		return PortfolioLibraryState{Linked: len(station.GetAssistantProgramState().LinkedProjectIDs)}, nil
	}
	if err != nil {
		return PortfolioLibraryState{}, err
	}
	state := PortfolioLibraryState{Initialized: true}
	inactive := map[string]bool{}
	if station, getErr := h.workspaceTaskStore.Get(scope.HomeID); getErr == nil && station != nil {
		for _, id := range station.GetAssistantProgramState().ProjectLibraryInactiveRoots {
			inactive[id] = true
		}
	}
	folder = filepath.Clean(folder)
	var root *projectlibrary.Root
	for i := range doc.Roots {
		if doc.Roots[i].RevokedAt == nil && !inactive[doc.Roots[i].ID] && filepath.Clean(doc.Roots[i].Path) == folder {
			root = &doc.Roots[i]
		}
	}
	if root == nil {
		return state, nil
	}
	state.RootID = root.ID
	for i := len(doc.Scans) - 1; i >= 0; i-- {
		scan := doc.Scans[i]
		if scan.RootID != root.ID || scan.RootRevision != root.Revision || scan.ScopeID != "" ||
			(scan.Status != "complete" && scan.Status != "partial") {
			continue
		}
		state.Scanned = true
		state.Partial = listingPartial(scan)
		break
	}
	state.Listed = listedUnder(doc, root.ID)
	return state, nil
}

// listingPartial is true when a listing may have missed project folders. A scan
// that only ran out of room for sub-folder evidence still listed every project.
func listingPartial(scan projectlibrary.Scan) bool {
	return scan.Status == "partial" && scan.PartialReason != "scope_limit_or_invalid"
}

// listedUnder counts the catalog rows observed under one root.
func listedUnder(doc projectlibrary.Document, rootID string) int {
	count := 0
	for _, entry := range doc.Entries {
		for _, observation := range entry.Observations {
			if observation.RootID == rootID {
				count++
				break
			}
		}
	}
	return count
}

// ReviewPortfolioLibrary reviews turning the Home's library on and reports how
// many already-linked projects it would carry in.
func (h *Handler) ReviewPortfolioLibrary(ownerUserID, homeID string) (string, int, error) {
	scope, err := h.portfolioLibraryScope(ownerUserID, homeID)
	if err != nil {
		return "", 0, err
	}
	review, err := h.assistantLibraryStore().ReviewInitialize(scope)
	if err != nil {
		return "", 0, err
	}
	return review.Token, review.LinkedCount, nil
}

// CommitPortfolioLibrary turns the library on with a reviewed token.
func (h *Handler) CommitPortfolioLibrary(ownerUserID, homeID, token, key string) error {
	scope, err := h.portfolioLibraryScope(ownerUserID, homeID)
	if err != nil {
		return err
	}
	_, _, err = h.assistantLibraryStore().CommitInitialize(scope, token, key)
	return err
}

// ReviewPortfolioRoot reviews connecting the collection folder to the library.
// The folder comes only from resolver, which proves it is the folder the offer
// named; the review's server-held path is returned for the run's own check.
func (h *Handler) ReviewPortfolioRoot(ctx context.Context, ownerUserID, homeID, offerID string, resolver projectlibrary.PortfolioRootResolver) (token, folder string, includesScan bool, err error) {
	scope, err := h.portfolioLibraryScope(ownerUserID, homeID)
	if err != nil || h.assistantLibraryRoots == nil {
		return "", "", false, ErrPortfolioSetupUnavailable
	}
	pick, err := h.assistantLibraryRoots.PickFromPortfolio(ctx, scope, offerID, resolver)
	if err != nil {
		return "", "", false, err
	}
	doc, err := h.assistantLibraryStore().Read(scope)
	if err != nil {
		return "", "", false, err
	}
	review, err := h.assistantLibraryRoots.Review(scope, pick, doc.Revision)
	if err != nil {
		return "", "", false, err
	}
	return review.Token, review.RootPath, review.IncludesScan, nil
}

// CommitPortfolioRoot connects a reviewed folder and returns its root.
func (h *Handler) CommitPortfolioRoot(ownerUserID, homeID, token, key string) (string, error) {
	scope, err := h.portfolioLibraryScope(ownerUserID, homeID)
	if err != nil || h.assistantLibraryRoots == nil {
		return "", ErrPortfolioSetupUnavailable
	}
	root, _, err := h.assistantLibraryRoots.Commit(scope, token, key)
	if err != nil {
		return "", err
	}
	return root.ID, nil
}

// ReviewPortfolioScan reviews one listing of the whole root. MetadataOnly is
// true only for a whole-root scan of names and project markers.
func (h *Handler) ReviewPortfolioScan(ownerUserID, homeID, rootID string) (token, reviewedRoot string, metadataOnly bool, err error) {
	scope, err := h.portfolioLibraryScope(ownerUserID, homeID)
	if err != nil || h.assistantLibraryRoots == nil {
		return "", "", false, ErrPortfolioSetupUnavailable
	}
	doc, err := h.assistantLibraryStore().Read(scope)
	if err != nil {
		return "", "", false, err
	}
	review, err := h.assistantLibraryRoots.ReviewScan(scope, rootID, doc.Revision)
	if err != nil {
		return "", "", false, err
	}
	metadataOnly = review.IncludesScan && review.ScopeID == "" && review.MaxEntries > 0 &&
		strings.Contains(review.Scope, "no file contents")
	return review.Token, review.RootID, metadataOnly, nil
}

// CommitPortfolioScan lists the root and reports how many projects the library
// now holds under it and whether the listing may have missed some.
func (h *Handler) CommitPortfolioScan(ctx context.Context, ownerUserID, homeID, rootID, token, key string) (int, bool, error) {
	scope, err := h.portfolioLibraryScope(ownerUserID, homeID)
	if err != nil || h.assistantLibraryRoots == nil {
		return 0, false, ErrPortfolioSetupUnavailable
	}
	scan, _, err := h.assistantLibraryRoots.CommitScan(ctx, scope, rootID, token, key)
	if err != nil {
		return 0, false, err
	}
	if scan.Status != "complete" && scan.Status != "partial" {
		return 0, false, fmt.Errorf("%w: the listing ended %s", ErrPortfolioSetupUnavailable, scan.Status)
	}
	doc, err := h.assistantLibraryStore().Read(scope)
	if err != nil {
		return 0, false, err
	}
	return listedUnder(doc, rootID), listingPartial(scan), nil
}

package agenthttp

import (
	"context"
	"encoding/json"
	"net/http"
	"net/url"
	"regexp"
	"slices"
	"sort"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/johnjallday/ori-agent/internal/assistantcontext"
	orihttp "github.com/johnjallday/ori-agent/internal/http"
	"github.com/johnjallday/ori-agent/internal/sensitive"
	"github.com/johnjallday/ori-agent/internal/workspace"
)

// AssistantWorkspaceSource deliberately has no Save/Update, agent, capability,
// filesystem, memory, or runtime operations. Listings are candidates only.
type AssistantWorkspaceSource interface {
	Get(string) (*workspace.Workspace, error)
	ListActive() ([]*workspace.Workspace, error)
}

type AssistantWorkspaceResolver struct {
	Source AssistantWorkspaceSource
	// Notes lists note titles for the overview. Titles and times only: the
	// overview never carries a note's content. Nil reports notes unsupported.
	Notes AssistantNoteReader
	// Files reports that the host's file readers are connected, so the overview
	// may count the workspace's readable file sources instead of saying
	// unsupported. The resolver itself never touches a file.
	Files bool
	Now   func() time.Time
}

var workspaceReferenceToken = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._:-]{0,127}$`)
var workspacePrivatePath = regexp.MustCompile(`(^|[\s"'(])(/[^\s"'<>]+|[A-Za-z]:\\[^\s"'<>]+)`)

func workspaceContextText(text string, limit int) string {
	if sensitive.ContainsSecretLikeText(text) {
		return "[withheld: secret-like text]"
	}
	text = workspacePrivatePath.ReplaceAllString(text, "$1[private path]")
	return boundedContextText(text, limit)
}

func workspaceReadable(ws *workspace.Workspace, userID string) bool {
	if ws == nil || !workspaceReferenceToken.MatchString(ws.ID) || ws.Status == workspace.StatusMissing || ws.Status == workspace.StatusTrashed {
		return false
	}
	owner := strings.TrimSpace(ws.OwnerUserID)
	return owner == userID || (owner == "" && userID == "local")
}

func contextWorkspaceRef(ws *workspace.Workspace) assistantcontext.WorkspaceRef {
	kind := "project"
	if ws.Kind == "group" {
		kind = "group"
		if state := ws.GetAssistantProgramState(); state != nil && state.Declaration != nil && state.Key.Valid() && state.Declaration.ID == state.Key.ProgramID && state.Key.Normalize().OwnerUserID == normalizedWorkspaceOwner(ws.OwnerUserID) {
			kind = "home"
		}
	}
	slug := ws.FolderSlug
	if !workspaceReferenceToken.MatchString(slug) {
		slug = ""
	}
	return assistantcontext.WorkspaceRef{ID: ws.ID, Slug: slug, Name: workspaceContextText(ws.Name, 120), Kind: kind, Version: ws.Version, UpdatedAt: ws.UpdatedAt}
}

func normalizedWorkspaceOwner(owner string) string {
	if owner = strings.TrimSpace(owner); owner == "" {
		return "local"
	}
	return owner
}

// Resolve returns display/evidence facts, never a grant. The handler validates
// the hired relationship separately; userID is supplied by the server, not JSON.
// This local application currently has no remote principal access adapter.
func (r *AssistantWorkspaceResolver) Resolve(ctx context.Context, userID, prompt string, refs *HomeAssistantRouteContext) assistantcontext.Turn {
	now := time.Now
	if r != nil && r.Now != nil {
		now = r.Now
	}
	out := assistantcontext.Turn{Version: assistantcontext.Version, Status: assistantcontext.Available, ReadAt: now().UTC(), Discovery: assistantcontext.SourceStatus{Status: assistantcontext.Unavailable, Reason: "store_unavailable"}}
	fail := func(status assistantcontext.Availability, reason string) assistantcontext.Turn {
		out.Status, out.Reason, out.Subject, out.Overview = status, reason, nil, nil
		return out
	}
	if userID != "local" {
		return fail(assistantcontext.Denied, "principal_unsupported")
	}
	if ctx.Err() != nil {
		return fail(assistantcontext.Unavailable, "request_cancelled")
	}
	if r == nil || r.Source == nil {
		return fail(assistantcontext.Unavailable, "store_unavailable")
	}
	if refs == nil {
		refs = &HomeAssistantRouteContext{}
	}
	if refs.ContextVersion < 0 || refs.ContextVersion > assistantcontext.Version {
		return fail(assistantcontext.Denied, "context_version_invalid")
	}
	location, reason := r.location(refs, userID)
	if reason != "" {
		status := assistantcontext.Unavailable
		if strings.Contains(reason, "denied") || strings.Contains(reason, "conflict") || strings.Contains(reason, "invalid") {
			status = assistantcontext.Denied
		}
		return fail(status, reason)
	}
	if location != nil {
		ref := contextWorkspaceRef(location)
		out.Location = &ref
	}

	// Summary errors are authoritative: never conceal failure with another
	// listing API. Hydrate candidates before any payload/access decision.
	listed, listErr := workspace.ListActiveSummaries(r.Source)
	if listErr == nil {
		out.Discovery = assistantcontext.SourceStatus{Status: assistantcontext.Available}
		for _, candidate := range listed {
			if candidate.Status == workspace.StatusTrashed || candidate.Status == workspace.StatusMissing || normalizedWorkspaceOwner(candidate.OwnerUserID) != userID {
				continue
			}
			if candidate.Kind == "group" {
				out.GroupCount++
			} else {
				out.ProjectCount++
			}
		}
		out.Discovery.Count = out.GroupCount + out.ProjectCount
		sort.Slice(listed, func(i, j int) bool { return listed[i].ID < listed[j].ID })
		for _, candidate := range listed {
			if ctx.Err() != nil {
				return fail(assistantcontext.Unavailable, "request_cancelled")
			}
			if normalizedWorkspaceOwner(candidate.OwnerUserID) != userID {
				continue
			}
			if candidate.Kind == "group" && len(out.Groups) >= assistantcontext.PreviewLimit {
				continue
			}
			if candidate.Kind != "group" && len(out.Projects) >= assistantcontext.PreviewLimit {
				continue
			}
			fresh, err := r.Source.Get(candidate.ID)
			if err != nil || !workspaceReadable(fresh, userID) {
				out.Discovery.Status, out.Discovery.Reason = assistantcontext.Partial, "candidate_unavailable"
				continue
			}
			ref := contextWorkspaceRef(fresh)
			if fresh.Kind == "group" {
				if len(out.Groups) < assistantcontext.PreviewLimit {
					out.Groups = append(out.Groups, ref)
				}
			} else {
				if len(out.Projects) < assistantcontext.PreviewLimit {
					out.Projects = append(out.Projects, ref)
				}
			}
		}
		if out.GroupCount > len(out.Groups) || out.ProjectCount > len(out.Projects) {
			out.Discovery.Status = assistantcontext.Partial
			if out.Discovery.Reason == "" {
				out.Discovery.Reason = "preview_limit"
			}
		}
		if out.Discovery.Count == 0 {
			out.Discovery.Status = assistantcontext.Empty
		}
	}
	subject := location
	if refs.SubjectWorkspaceID != "" {
		if !workspaceReferenceToken.MatchString(refs.SubjectWorkspaceID) {
			return fail(assistantcontext.Denied, "subject_invalid")
		}
		var err error
		subject, err = r.Source.Get(refs.SubjectWorkspaceID)
		if err != nil || subject == nil {
			return fail(assistantcontext.Unavailable, "subject_unavailable")
		}
		if !workspaceReadable(subject, userID) {
			return fail(assistantcontext.Denied, "subject_denied")
		}
		out.SubjectExplicit = true
	} else if prompt != "" && listErr == nil {
		matches := explicitWorkspaceNames(prompt, listed, userID)
		if len(matches) > 1 {
			for _, candidate := range matches {
				fresh, err := r.Source.Get(candidate.ID)
				if err == nil && workspaceReadable(fresh, userID) && fresh.Name == candidate.Name && len(out.Choices) < assistantcontext.PreviewLimit {
					out.Choices = append(out.Choices, contextWorkspaceRef(fresh))
				}
			}
			return fail(assistantcontext.Unavailable, "subject_ambiguous")
		}
		if len(matches) == 1 {
			var err error
			subject, err = r.Source.Get(matches[0].ID)
			if err != nil || !workspaceReadable(subject, userID) || subject.Name != matches[0].Name {
				return fail(assistantcontext.Unavailable, "subject_changed")
			}
			out.SubjectExplicit = true
		}
	}
	if subject == nil {
		if refs.TaskID != "" {
			return fail(assistantcontext.Denied, "task_without_workspace")
		}
		return out // App-wide. In particular, HQ is never an implicit fallback.
	}
	if !workspaceReadable(subject, userID) {
		return fail(assistantcontext.Denied, "workspace_denied")
	}
	ref := contextWorkspaceRef(subject)
	out.Subject = &ref
	selectedTask := refs.TaskID
	if selectedTask == "" && refs.PagePath != "" {
		// location already validated the page token. Derive an omitted task_id
		// without mutating browser references or adopting another workspace.
		parsed, _ := url.Parse(refs.PagePath)
		parts := strings.Split(strings.TrimPrefix(parsed.EscapedPath(), "/workspaces/"), "/")
		if len(parts) > 2 && taskPageSegment(parts[1]) {
			selectedTask, _ = url.PathUnescape(parts[2])
		}
	}
	// A selected task belongs to the page it is on. When the user names another
	// workspace, that task is not part of the subject and is not looked up in it.
	if location != nil && subject.ID != location.ID {
		selectedTask = ""
	}
	overview, reason := r.overview(ctx, subject, userID, selectedTask, listed, listErr != nil)
	if reason != "" {
		return fail(assistantcontext.Unavailable, reason)
	}
	out.Overview = &overview
	return out
}

// taskPageSegment recognizes a task page. The app's page is
// /workspaces/<slug>/task/<id>; the plural is accepted for older references.
func taskPageSegment(segment string) bool { return segment == "task" || segment == "tasks" }

func (r *AssistantWorkspaceResolver) location(refs *HomeAssistantRouteContext, userID string) (*workspace.Workspace, string) {
	var pageSlug, pageTask string
	if refs.PagePath != "" {
		parsed, err := url.Parse(refs.PagePath)
		if err != nil || parsed.IsAbs() || parsed.Host != "" || !strings.HasPrefix(parsed.Path, "/") {
			return nil, "page_reference_invalid"
		}
		path := parsed.EscapedPath()
		if strings.HasPrefix(path, "/workspaces/") {
			parts := strings.Split(strings.TrimPrefix(path, "/workspaces/"), "/")
			if parts[0] == "" {
				// The workspaces list itself, written with a trailing slash.
				return nil, ""
			}
			pageSlug, err = url.PathUnescape(parts[0])
			if err != nil || !workspaceReferenceToken.MatchString(pageSlug) {
				return nil, "page_reference_invalid"
			}
			if len(parts) > 2 && taskPageSegment(parts[1]) {
				pageTask, err = url.PathUnescape(parts[2])
				if err != nil || !workspaceReferenceToken.MatchString(pageTask) {
					return nil, "task_reference_invalid"
				}
			}
		} else if parsed.Path != "/" {
			// Genuine app-wide pages clear stale selection/page defaults.
			return nil, ""
		}
	}
	id, slug := strings.TrimSpace(refs.WorkspaceID), strings.TrimSpace(refs.WorkspaceSlug)
	if pageSlug == "" && refs.PagePath == "/" && refs.SelectionWorkspaceID != "" {
		id = refs.SelectionWorkspaceID
	}
	if pageSlug != "" {
		resolver, ok := r.Source.(workspace.SlugResolver)
		if !ok {
			return nil, "slug_resolution_unavailable"
		}
		page, err := resolver.ResolveSlug(pageSlug)
		if err != nil || page == nil {
			return nil, "workspace_unavailable"
		}
		// Legacy Guide collectors put the route token in workspace_id. Accept
		// only the exact published page slug alias, never arbitrary ID fallback.
		if (id != "" && id != page.ID && (refs.ContextVersion != 0 || id != page.FolderSlug)) || (slug != "" && slug != page.FolderSlug) {
			return nil, "workspace_reference_conflict"
		}
		if pageTask != "" && refs.TaskID != "" && pageTask != refs.TaskID {
			return nil, "task_reference_conflict"
		}
		if !workspaceReadable(page, userID) {
			return nil, "workspace_denied"
		}
		return page, ""
	}
	if id == "" && slug == "" {
		return nil, ""
	}
	var current *workspace.Workspace
	var err error
	if id != "" {
		if !workspaceReferenceToken.MatchString(id) {
			return nil, "workspace_reference_invalid"
		}
		current, err = r.Source.Get(id)
	} else {
		resolver, ok := r.Source.(workspace.SlugResolver)
		if !ok {
			return nil, "slug_resolution_unavailable"
		}
		if !workspaceReferenceToken.MatchString(slug) {
			return nil, "workspace_reference_invalid"
		}
		current, err = resolver.ResolveSlug(slug)
	}
	if err != nil || current == nil {
		return nil, "workspace_unavailable"
	}
	if slug != "" && slug != current.FolderSlug {
		return nil, "workspace_reference_conflict"
	}
	if !workspaceReadable(current, userID) {
		return nil, "workspace_denied"
	}
	return current, ""
}

// A portable reciprocal link is data about exact membership. In particular,
// names, grouping alone, and half-links are never enough; compatibility for a
// proposed mutation remains the setup service's separate responsibility.
func exactWorkspaceProgramLink(project, home *workspace.Workspace, link *workspace.AssistantProjectLink, state *workspace.AssistantProgramState, userID string) bool {
	if state == nil || state.Declaration == nil || !state.Key.Valid() || state.Key.Normalize().OwnerUserID != userID || state.Key.Normalize() != link.Key.Normalize() || !slices.Contains(state.LinkedProjectIDs, project.ID) || link.ID != workspace.AssistantProjectLinkID(home.ID, project.ID) || link.DeclarationVersion != state.Declaration.SchemaVersion {
		return false
	}
	if link.SchemaVersion != workspace.AssistantProjectLinkLegacySchemaVersion && link.SchemaVersion != workspace.AssistantProjectLinkSchemaVersion {
		return false
	}
	if link.SchemaVersion >= workspace.AssistantProjectLinkSchemaVersion && (home.Kind != "group" || project.ParentID != home.ID) {
		return false
	}
	if (link.HomeProvider == nil) != (state.HomeProvider == nil) {
		return false
	}
	return link.HomeProvider == nil || *link.HomeProvider == *state.HomeProvider
}

func contextTaskPreview(task workspace.Task) assistantcontext.TaskPreview {
	updated := task.UpdatedAt
	if updated.IsZero() {
		updated = task.CreatedAt
	}
	return assistantcontext.TaskPreview{ID: task.ID, Label: workspaceContextText(task.Description, 240), Status: workspaceContextText(string(task.Status), 40), Assignee: workspaceContextText(task.To, 80), UpdatedAt: updated}
}

func (r *AssistantWorkspaceResolver) overview(ctx context.Context, ws *workspace.Workspace, userID, taskID string, listed []workspace.WorkspaceSummary, listingFailed bool) (assistantcontext.Overview, string) {
	out := assistantcontext.Overview{Workspace: contextWorkspaceRef(ws), Description: workspaceContextText(ws.Description, 1200), Sources: map[string]assistantcontext.SourceStatus{}}
	if ws.GoalBrief.Accepted() {
		out.Goal = workspaceContextText(ws.GoalBrief.Summary, 800)
	}
	out.Sources["parent"] = assistantcontext.SourceStatus{Status: assistantcontext.Empty}
	if ws.ParentID != "" {
		parent, err := r.Source.Get(ws.ParentID)
		if err != nil || !workspaceReadable(parent, userID) {
			out.Sources["parent"] = assistantcontext.SourceStatus{Status: assistantcontext.Unavailable, Reason: "parent_unavailable"}
		} else {
			ref := contextWorkspaceRef(parent)
			out.Parent = &ref
			out.Sources["parent"] = assistantcontext.SourceStatus{Status: assistantcontext.Available}
		}
	}
	out.Sources["program_link"] = assistantcontext.SourceStatus{Status: assistantcontext.Empty}
	if link := ws.GetAssistantProjectLink(); link != nil {
		out.Sources["program_link"] = assistantcontext.SourceStatus{Status: assistantcontext.Unavailable, Reason: "membership_unverified"}
		home, err := r.Source.Get(link.StationWorkspaceID)
		if err == nil && workspaceReadable(home, userID) {
			state := home.GetAssistantProgramState()
			if exactWorkspaceProgramLink(ws, home, link, state, userID) {
				out.ProgramLink = &assistantcontext.ProgramLink{Home: contextWorkspaceRef(home), LinkID: link.ID, Revision: link.StateRevision, ProgramID: workspaceContextText(state.Key.ProgramID, 100)}
				out.Sources["program_link"] = assistantcontext.SourceStatus{Status: assistantcontext.Available}
			}
		}
	}
	out.Sources["children"] = assistantcontext.SourceStatus{Status: assistantcontext.Empty}
	if listingFailed {
		out.Sources["children"] = assistantcontext.SourceStatus{Status: assistantcontext.Unavailable, Reason: "listing_failed"}
	} else {
		sort.Slice(listed, func(i, j int) bool { return listed[i].ID < listed[j].ID })
		unread := 0
		for _, candidate := range listed {
			if ctx.Err() != nil {
				return out, "request_cancelled"
			}
			if candidate.ParentID != ws.ID || normalizedWorkspaceOwner(candidate.OwnerUserID) != userID {
				continue
			}
			child, err := r.Source.Get(candidate.ID)
			if err != nil {
				unread++
				continue
			}
			if !workspaceReadable(child, userID) || child.ParentID != ws.ID {
				continue
			}
			status := out.Sources["children"]
			status.Count++
			status.Status = assistantcontext.Available
			out.Sources["children"] = status
			if len(out.Children) < assistantcontext.PreviewLimit {
				out.Children = append(out.Children, contextWorkspaceRef(child))
			} else {
				out.Truncated = true
			}
		}
		// A listed child that could not be read is missing from the count. Say
		// so: a Home whose projects failed to load is not an empty Home.
		if unread > 0 {
			status := out.Sources["children"]
			status.Status, status.Reason = assistantcontext.Partial, "child_unavailable"
			if status.Count == 0 {
				status.Status = assistantcontext.Unavailable
			}
			out.Sources["children"] = status
		}
	}
	for _, instance := range ws.AgentInstances {
		if len(out.Agents) == assistantcontext.PreviewLimit {
			out.Truncated = true
			break
		}
		out.Agents = append(out.Agents, assistantcontext.AgentPreview{Name: workspaceContextText(instance.Name, 80), Role: workspaceContextText(instance.Role, 100), Status: "attached; runtime not inspected"})
	}
	for _, task := range ws.Tasks {
		if !workspaceReferenceToken.MatchString(task.ID) || (task.WorkspaceID != "" && task.WorkspaceID != ws.ID) {
			continue
		}
		preview := contextTaskPreview(task)
		if task.ID == taskID {
			out.SelectedTask = &preview
		}
		if len(out.Tasks) < assistantcontext.PreviewLimit {
			out.Tasks = append(out.Tasks, preview)
		} else {
			out.Truncated = true
		}
	}
	if taskID != "" && out.SelectedTask == nil {
		return out, "task_not_in_subject"
	}
	out.Sources["tasks"] = assistantcontext.SourceStatus{Status: assistantcontext.Available, Count: len(ws.Tasks)}
	if len(ws.Tasks) == 0 {
		out.Sources["tasks"] = assistantcontext.SourceStatus{Status: assistantcontext.Empty}
	}
	out.Sources["notes"] = assistantcontext.SourceStatus{Status: assistantcontext.Unsupported, Reason: "reader_not_connected"}
	if r.Notes != nil {
		// A failed listing is unavailable, never an empty workspace.
		out.Sources["notes"] = assistantcontext.SourceStatus{Status: assistantcontext.Unavailable, Reason: "listing_failed"}
		if listed, err := r.Notes.Notes(ctx, ws.ID); err == nil {
			status := assistantcontext.SourceStatus{Status: assistantcontext.Empty}
			sort.SliceStable(listed, func(i, j int) bool { return listed[i].UpdatedAt.After(listed[j].UpdatedAt) })
			for _, note := range listed {
				if note.WorkspaceID != ws.ID || !workspaceReferenceToken.MatchString(note.ID) {
					continue
				}
				status.Status = assistantcontext.Available
				status.Count++
				if len(out.Notes) < assistantcontext.PreviewLimit {
					out.Notes = append(out.Notes, assistantcontext.NotePreview{ID: note.ID, Title: workspaceContextText(note.Name, 160), UpdatedAt: note.UpdatedAt})
				} else {
					out.Truncated = true
				}
			}
			out.Sources["notes"] = status
		}
	}
	out.Sources["files"] = assistantcontext.SourceStatus{Status: assistantcontext.Unsupported, Reason: "reader_not_connected"}
	if r.Files {
		// Counted from canonical records only: stored attachments, user-visible
		// linked folders and a project-file locator. No folder is listed and no
		// file is opened or even looked at to build the overview.
		files := assistantcontext.SourceStatus{Status: assistantcontext.Empty}
		for _, attachment := range ws.Attachments {
			if attachment.DeletedAt == nil && attachment.File != nil && workspace.AttachmentSourcePath(ws.ID, attachment.File) != "" {
				files.Count++
			}
		}
		for _, ref := range ws.DirectoryReferences {
			if _, ok := subjectFolder(ws, ref.ID); ok {
				files.Count++
			}
		}
		if locator, err := workspace.GetProjectEntryLocator(ws.SharedData); err == nil && locator != nil {
			files.Count++
		}
		if files.Count > 0 {
			files.Status = assistantcontext.Available
		}
		out.Sources["files"] = files
	}
	out.Sources["knowledge"] = assistantcontext.SourceStatus{Status: assistantcontext.Unsupported, Reason: "workspace_memory_has_no_eligible_reader"}
	boundWorkspaceOverview(&out)
	return out, ""
}

// Enforce the serialized-data bound as well as field/category bounds. JSON's
// HTML escaping can expand hostile text; prune instead of cutting invalid JSON.
func boundWorkspaceOverview(out *assistantcontext.Overview) {
	for {
		encoded, err := json.Marshal(out)
		if err != nil || utf8.RuneCount(encoded) <= assistantcontext.OverviewLimit {
			return
		}
		out.Truncated = true
		switch {
		case out.Description != "":
			out.Description = ""
		case out.Goal != "":
			out.Goal = ""
		case len(out.Notes) > 0:
			out.Notes = out.Notes[:len(out.Notes)-1]
		case len(out.Tasks) > 0:
			out.Tasks = out.Tasks[:len(out.Tasks)-1]
		case len(out.Children) > 0:
			out.Children = out.Children[:len(out.Children)-1]
		case len(out.Agents) > 0:
			out.Agents = out.Agents[:len(out.Agents)-1]
		default:
			return // Fixed, bounded identity/status fields fit by construction.
		}
	}
}

// Match canonical names only within the user's own prompt, with word bounds.
// Nested shorter names do not compete with an exact longer name at that span;
// duplicate equal names do. Never derive a target from history/source prose.
func explicitWorkspaceNames(prompt string, listed []workspace.WorkspaceSummary, userID string) []workspace.WorkspaceSummary {
	type match struct {
		start, end int
		item       workspace.WorkspaceSummary
	}
	var found []match
	text := strings.ToLower(prompt)
	for _, candidate := range listed {
		name := strings.ToLower(strings.TrimSpace(candidate.Name))
		if utf8.RuneCountInString(name) < 3 || normalizedWorkspaceOwner(candidate.OwnerUserID) != userID {
			continue
		}
		for offset := 0; offset < len(text); {
			index := strings.Index(text[offset:], name)
			if index < 0 {
				break
			}
			start, end := offset+index, offset+index+len(name)
			word := func(r rune) bool { return unicode.IsLetter(r) || unicode.IsDigit(r) || r == '_' || r == '-' }
			before, _ := utf8.DecodeLastRuneInString(text[:start])
			after, _ := utf8.DecodeRuneInString(text[end:])
			if (start == 0 || !word(before)) && (end == len(text) || !word(after)) {
				found = append(found, match{start, end, candidate})
			}
			offset = end
		}
	}
	var result []workspace.WorkspaceSummary
	seen := map[string]bool{}
	for _, candidate := range found {
		contained := false
		for _, other := range found {
			if other.start <= candidate.start && other.end >= candidate.end && other.end-other.start > candidate.end-candidate.start {
				contained = true
				break
			}
		}
		if !contained && !seen[candidate.item.ID] {
			result = append(result, candidate.item)
			seen[candidate.item.ID] = true
		}
	}
	sort.Slice(result, func(i, j int) bool { return result[i].ID < result[j].ID })
	return result
}

// WorkspaceContextHandler is a non-mutating display refresh. Opening the drawer
// loads metadata only; this is not a model turn, content scan or source grant.
func (h *HomeAssistantAskHandler) WorkspaceContextHandler(w http.ResponseWriter, request *http.Request) {
	if !orihttp.RequireMethod(w, request, http.MethodPost) {
		return
	}
	var req HomeAssistantAskRequest
	if !orihttp.ParseJSONBody(w, request, &req) {
		return
	}
	provider, ok := h.PersonalAssistantContext.(interface {
		ResolvePersonalAssistantRelationship(context.Context, string) (*PersonalAssistantWorkContext, error)
	})
	if !ok || provider == nil {
		orihttp.WriteJSON(w, assistantcontext.Turn{Version: assistantcontext.Version, Status: assistantcontext.Unavailable, Reason: "relationship_reader_unavailable"})
		return
	}
	userID, err := h.currentAssistantUser(request.Context())
	if err != nil {
		orihttp.WriteJSON(w, assistantcontext.Turn{Version: assistantcontext.Version, Status: assistantcontext.Denied, Reason: "principal_unavailable"})
		return
	}
	work, err := provider.ResolvePersonalAssistantRelationship(request.Context(), userID)
	if err != nil || work == nil || !work.ReadyForWork() {
		orihttp.WriteJSON(w, assistantcontext.Turn{Version: assistantcontext.Version, Status: assistantcontext.Unavailable, Reason: "relationship_unavailable"})
		return
	}
	orihttp.WriteJSON(w, h.WorkspaceContext.Resolve(request.Context(), userID, req.Prompt, req.Context))
}

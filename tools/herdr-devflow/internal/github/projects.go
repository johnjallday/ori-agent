package github

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"
)

// The project board this repository's Issues are ranked on.
//
// An Issue is open or closed and nothing else: it cannot say "third in line" or
// "researched enough to build". A ProjectV2 carries those as custom fields, so
// the board is where a groomed backlog actually lives. This file reads it. It
// never writes: ranking is the grooming agent's job and lifecycle is GitHub's,
// and a read command that quietly moved a card would make both untrustworthy.

// DefaultProjectItemLimit bounds one board read, mirroring DefaultIssueLimit.
// A board that outgrows this is reported as truncated rather than silently cut.
const DefaultProjectItemLimit = 100

// maxLinkedProjects bounds the linked-project query. A repository with more
// linked projects than this is already past the point where "the board" means
// anything, and the ambiguity error will say so.
const maxLinkedProjects = 10

// Project is one ProjectV2 linked to a repository.
//
// Owner is carried because it is not always the repository's owner — a
// user-owned project can be linked to an organization's repository — and every
// later `gh project` call has to name the project's owner, not the repo's.
type Project struct {
	Number int
	Title  string
	URL    string
	Owner  string
}

// ProjectItem is one card on the board.
//
// Number is 0 and IsDraft is true for a draft card: an item that exists only on
// the board with no Issue behind it. That is how the grooming agent proposes
// work spanning several Issues, so a draft is a real thing to display, not a
// malformed row to drop.
type ProjectItem struct {
	Number  int
	Title   string
	Status  string
	Size    string
	Why     string
	Rank    int
	HasRank bool
	URL     string
	IsDraft bool
}

// ProjectBoard is one bounded read of a board.
type ProjectBoard struct {
	Repository Repository
	Project    Project
	Items      []ProjectItem
	ObservedAt time.Time
	Complete   bool
	Truncated  bool
}

// linkedProjectsQuery asks which ProjectsV2 are linked to one repository.
//
// The owner is unwrapped through both concrete types because ProjectV2Owner is
// an interface: a project belongs to a User or an Organization, and only the
// login is needed from either.
const linkedProjectsQuery = `query($owner:String!,$name:String!,$first:Int!){` +
	`repository(owner:$owner,name:$name){` +
	`projectsV2(first:$first){nodes{number title url ` +
	`owner{__typename ... on User{login} ... on Organization{login}}}}}}`

// ResolveLinkedProject finds the one project board this repository uses.
//
// The repository link is the single source of truth here, deliberately: a
// configured project number or title would be one more thing to keep in sync,
// and would hardcode one person's board name into a repository other people
// clone. Link the board you mean, and this finds it.
//
// Exactly one linked project is the supported shape. Several is an error that
// names them all rather than a guess: picking by title, by lowest number, or by
// item count would silently read the wrong board, and a backlog that is quietly
// the wrong backlog is worse than one that refuses to load.
func (c *Client) ResolveLinkedProject(ctx context.Context, repository Repository) (Project, error) {
	if repository.Empty() {
		return Project{}, errors.New("a resolved repository is required")
	}
	queryCtx, cancel := context.WithTimeout(ctx, c.timeout)
	defer cancel()

	output, err := c.run(queryCtx,
		"api", "graphql",
		"-f", "query="+linkedProjectsQuery,
		"-F", "owner="+repository.Owner,
		"-F", "name="+repository.Name,
		"-F", fmt.Sprintf("first=%d", maxLinkedProjects),
	)
	if err != nil {
		return Project{}, classify(queryCtx, err)
	}
	if len(output) > MaxOutputBytes {
		return Project{}, &Error{
			Kind:   ErrorMalformed,
			Detail: "GitHub's answer for this repository's linked projects was larger than this command will read",
		}
	}

	var payload struct {
		Data struct {
			Repository struct {
				ProjectsV2 struct {
					Nodes []struct {
						Number int    `json:"number"`
						Title  string `json:"title"`
						URL    string `json:"url"`
						Owner  struct {
							Login string `json:"login"`
						} `json:"owner"`
					} `json:"nodes"`
				} `json:"projectsV2"`
			} `json:"repository"`
		} `json:"data"`
	}
	if err := json.Unmarshal(output, &payload); err != nil {
		return Project{}, &Error{
			Kind:   ErrorMalformed,
			Detail: "GitHub's answer for this repository's linked projects could not be read",
		}
	}

	projects := make([]Project, 0, len(payload.Data.Repository.ProjectsV2.Nodes))
	for _, node := range payload.Data.Repository.ProjectsV2.Nodes {
		projects = append(projects, Project{
			Number: node.Number,
			Title:  sanitize(node.Title),
			URL:    sanitize(node.URL),
			Owner:  sanitize(node.Owner.Login),
		})
	}

	switch len(projects) {
	case 1:
		return projects[0], nil
	case 0:
		return Project{}, &Error{
			Kind: ErrorProjectMissing,
			Detail: fmt.Sprintf(
				"no project board is linked to %s, so there is no backlog to read", repository.Slug()),
		}
	default:
		return Project{}, &Error{
			Kind: ErrorProjectAmbiguous,
			Detail: fmt.Sprintf(
				"%d project boards are linked to %s (%s), so which one is the backlog is ambiguous",
				len(projects), repository.Slug(), describeProjects(projects)),
		}
	}
}

// describeProjects names every candidate, because the fix is choosing between
// them and a count alone would send the reader to the browser to find out what
// they are choosing from.
func describeProjects(projects []Project) string {
	described := make([]string, 0, len(projects))
	for _, project := range projects {
		described = append(described, fmt.Sprintf("#%d %s", project.Number, project.Title))
	}
	return strings.Join(described, ", ")
}

// ListProjectItems reads one board.
//
// `gh project item-list` lowercases custom field names in its JSON, so a field
// named "Why" arrives as "why". That is a property of gh's output, not of the
// board, and it is the reason this decodes into a shape of its own rather than
// trusting field names to round-trip.
func (c *Client) ListProjectItems(
	ctx context.Context, repository Repository, project Project,
) (ProjectBoard, error) {
	if project.Number <= 0 || strings.TrimSpace(project.Owner) == "" {
		return ProjectBoard{}, errors.New("a resolved project is required")
	}
	queryCtx, cancel := context.WithTimeout(ctx, c.timeout)
	defer cancel()

	output, err := c.run(queryCtx,
		"project", "item-list", fmt.Sprintf("%d", project.Number),
		"--owner", project.Owner,
		"--format", "json",
		"--limit", fmt.Sprintf("%d", c.projectItemLimit),
	)
	if err != nil {
		return ProjectBoard{}, classify(queryCtx, err)
	}
	if len(output) > MaxOutputBytes {
		return ProjectBoard{}, &Error{
			Kind:   ErrorMalformed,
			Detail: "GitHub's answer for this board's items was larger than this command will read",
		}
	}

	var payload struct {
		Items []struct {
			Title   string   `json:"title"`
			Status  string   `json:"status"`
			Size    string   `json:"size"`
			Why     string   `json:"why"`
			Rank    *float64 `json:"rank"`
			Content struct {
				Number *int   `json:"number"`
				Title  string `json:"title"`
				URL    string `json:"url"`
				Type   string `json:"type"`
			} `json:"content"`
		} `json:"items"`
		TotalCount int `json:"totalCount"`
	}
	if err := json.Unmarshal(output, &payload); err != nil {
		return ProjectBoard{}, &Error{
			Kind:   ErrorMalformed,
			Detail: "GitHub's answer for this board's items could not be read",
		}
	}

	items := make([]ProjectItem, 0, len(payload.Items))
	for _, raw := range payload.Items {
		item := ProjectItem{
			Title:   sanitize(firstNonEmpty(raw.Content.Title, raw.Title)),
			Status:  sanitize(raw.Status),
			Size:    sanitize(raw.Size),
			Why:     boundedText(raw.Why, maxWhyRunes),
			URL:     sanitize(raw.Content.URL),
			IsDraft: raw.Content.Number == nil,
		}
		if raw.Content.Number != nil {
			item.Number = *raw.Content.Number
		}
		if raw.Rank != nil {
			item.Rank, item.HasRank = int(*raw.Rank), true
		}
		items = append(items, item)
	}

	// TotalCount is what matched, not what was returned, so it is the only
	// honest way to know the listing was cut.
	truncated := payload.TotalCount > len(items)
	return ProjectBoard{
		Repository: repository,
		Project:    project,
		Items:      items,
		ObservedAt: time.Now(),
		Complete:   !truncated,
		Truncated:  truncated,
	}, nil
}

// maxWhyRunes bounds the one-line justification a grooming agent writes. It is
// rendered on a terminal row beside a title, so a field that arrived as an
// essay would push everything else off the screen.
const maxWhyRunes = 240

// Ready returns the board's Ready column in the order it should be worked:
// ranked items first, ascending, then everything unranked by Issue number.
//
// Unranked sorts last rather than first because an absent rank means the
// grooming agent has not placed it yet, and an unplaced item must not out-rank
// one somebody deliberately put at the top.
func (b ProjectBoard) Ready() []ProjectItem {
	ready := make([]ProjectItem, 0, len(b.Items))
	for _, item := range b.Items {
		if strings.EqualFold(item.Status, StatusReady) {
			ready = append(ready, item)
		}
	}
	sort.SliceStable(ready, func(i, j int) bool {
		left, right := ready[i], ready[j]
		if left.HasRank != right.HasRank {
			return left.HasRank
		}
		if left.HasRank && left.Rank != right.Rank {
			return left.Rank < right.Rank
		}
		return left.Number < right.Number
	})
	return ready
}

// StatusReady is the board column meaning "researched and buildable". It is not
// "approved": choosing what to build stays with the person reading the column.
const StatusReady = "Ready"

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return value
		}
	}
	return ""
}

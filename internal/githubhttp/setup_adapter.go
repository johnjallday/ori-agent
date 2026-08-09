package githubhttp

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"

	"github.com/johnjallday/ori-agent/internal/setupwizard"
	agentworkspace "github.com/johnjallday/ori-agent/internal/workspace"
)

// SetupAdapterID is the registry key a blueprint manifest may name for GitHub
// Ops. It matches projecttemplates.ValidSetupWizardAdapters.
const SetupAdapterID = "github_ops"

// maxRepositoryOptions bounds how many repositories the picker offers at once.
// An unbounded dropdown is unusable on a large account and makes the wizard
// payload enormous; past this the user narrows with search instead.
const maxRepositoryOptions = 50

// SetupAdapter answers the Setup Wizard's questions about a GitHub Ops
// workspace.
//
// Like the Calendar adapter, it translates state rather than re-deriving it:
// every answer about the connection comes from the same Connection the
// Settings card uses, so the wizard and the card can never disagree. The one
// thing it does commit is the repository choice, because that is per-workspace
// state with no other home — and it commits it through BindRepo, which is
// idempotent.
type SetupAdapter struct {
	conn       *Connection
	workspaces WorkspaceStore
}

// NewSetupAdapter builds the adapter over the global GitHub connection.
func NewSetupAdapter(conn *Connection, workspaces WorkspaceStore) *SetupAdapter {
	return &SetupAdapter{conn: conn, workspaces: workspaces}
}

// ID implements setupwizard.Adapter.
func (a *SetupAdapter) ID() string { return SetupAdapterID }

// Evaluate reports where each step stands. Read-only: looking never connects
// an account, binds a repository, or writes anything to GitHub.
func (a *SetupAdapter) Evaluate(ctx context.Context, req setupwizard.StepRequest) (setupwizard.StepReadiness, error) {
	if a == nil || a.conn == nil || a.workspaces == nil {
		return setupwizard.StepReadiness{
			Blocked:       true,
			Summary:       "GitHub setup is unavailable in this build.",
			ErrorCategory: setupwizard.ErrorCategoryUnavailable,
		}, nil
	}

	ws, err := a.workspaces.GetFolderWorkspace(req.WorkspaceID)
	if err != nil || ws == nil {
		return setupwizard.StepReadiness{
			Blocked:       true,
			Summary:       "Ori could not read this workspace's GitHub settings.",
			ErrorCategory: setupwizard.ErrorCategoryDomainError,
		}, nil
	}

	switch req.Step.Kind {
	case agentworkspace.SetupStepKindAccountLink:
		return a.accountReadiness(ctx), nil
	case agentworkspace.SetupStepKindCapabilityConfigure:
		return a.repositoryReadiness(ctx, ws), nil
	default:
		return a.overallReadiness(ctx, ws), nil
	}
}

// Confirm commits the repository choice; every other step re-reads state.
//
// Connecting GitHub deliberately does not happen here. The token is entered
// once in Settings, where the connect endpoint validates it before storing;
// routing a credential through a generic wizard action would be a second,
// weaker door into the same capability.
func (a *SetupAdapter) Confirm(ctx context.Context, req setupwizard.StepRequest, action setupwizard.StepAction) (setupwizard.StepReadiness, error) {
	if a == nil || a.conn == nil || a.workspaces == nil {
		return setupwizard.StepReadiness{}, fmt.Errorf("github setup is unavailable")
	}

	if req.Step.Kind == agentworkspace.SetupStepKindCapabilityConfigure {
		if chosen := strings.TrimSpace(action.Option); chosen != "" {
			// Check the shape before spending a request on it, so a
			// malformed reference reports what is wrong with it rather than
			// whatever GitHub says about a nonsense path.
			if _, _, ok := SplitRepo(chosen); !ok {
				return setupwizard.StepReadiness{}, fmt.Errorf(
					"%w: %q is not a repository reference — choose one from the list",
					setupwizard.ErrStepRejected, chosen)
			}
			// Verify the token can actually write issues here BEFORE
			// recording the choice. The picker can only offer repositories
			// the token can read, which is a much wider set -- binding one
			// it cannot write to produces a workspace that triages happily
			// and then fails on the user's first approved change. Checking
			// once, here, is the whole cost; the ongoing readiness check
			// stays read-only.
			if err := a.conn.CheckWriteAccess(ctx, chosen); err != nil {
				// Returned as an error, not as a blocked readiness: the
				// service discards Confirm's readiness and re-evaluates, so
				// an error is the only channel that reaches the user with a
				// reason. ErrStepRejected marks the message as user-safe,
				// which a ConnectionError's message contractually is.
				var connErr *ConnectionError
				if errors.As(err, &connErr) {
					return setupwizard.StepReadiness{}, fmt.Errorf("%w: %s", setupwizard.ErrStepRejected, connErr.Message)
				}
				return setupwizard.StepReadiness{}, fmt.Errorf("%w: Ori could not check that repository", setupwizard.ErrStepRejected)
			}
			if err := a.bind(req.WorkspaceID, chosen); err != nil {
				return setupwizard.StepReadiness{
					Blocked:       true,
					Summary:       "Ori could not save that repository choice.",
					ErrorCategory: setupwizard.ErrorCategoryDomainError,
				}, nil
			}
		}
	}
	return a.Evaluate(ctx, req)
}

// bind persists the repository onto the workspace record.
func (a *SetupAdapter) bind(workspaceID, fullName string) error {
	ws, err := a.workspaces.GetFolderWorkspace(workspaceID)
	if err != nil {
		return err
	}
	if ws == nil {
		return fmt.Errorf("github: workspace %q not found", workspaceID)
	}
	if err := BindRepo(ws, fullName); err != nil {
		return err
	}
	return a.workspaces.Save(ws)
}

// accountReadiness answers "is there a working GitHub connection?".
//
// The connection is global, so this step never asks for a token when one
// already exists — it reports the existing connection and moves on. That is
// the whole point of the account_link kind: a second GitHub workspace links to
// what is already there rather than re-prompting.
func (a *SetupAdapter) accountReadiness(ctx context.Context) setupwizard.StepReadiness {
	identity, err := a.conn.TestConnection(ctx)
	if err == nil {
		return setupwizard.StepReadiness{
			Ready:   true,
			Summary: "GitHub is connected as @" + identity.Login + ".",
		}
	}
	return connectionErrorReadiness(err, "Connect GitHub in Settings, then return here.")
}

// repositoryReadiness answers "has this workspace chosen its repository?", and
// offers the choices when it has not.
func (a *SetupAdapter) repositoryReadiness(ctx context.Context, ws *agentworkspace.Workspace) setupwizard.StepReadiness {
	// A repository cannot be chosen before there is a connection to list it
	// with, so report the missing prerequisite rather than an empty picker.
	if _, err := a.conn.TestConnection(ctx); err != nil {
		return connectionErrorReadiness(err, "Connect GitHub first, then choose a repository.")
	}

	bound, hasRepo := BoundRepo(ws)

	repos, err := a.conn.ListRepositories(ctx, "")
	if err != nil {
		// Losing the ability to list is not the same as having no choice
		// recorded: a workspace that already picked a repo stays ready.
		if hasRepo {
			return setupwizard.StepReadiness{
				Ready:   true,
				Summary: "This workspace triages " + bound + ".",
			}
		}
		return connectionErrorReadiness(err, "Ori could not list your repositories.")
	}

	options := repositoryOptions(repos, bound)

	if hasRepo {
		return setupwizard.StepReadiness{
			Ready:   true,
			Summary: "This workspace triages " + bound + ".",
			Options: options,
		}
	}
	if len(options) == 0 {
		return setupwizard.StepReadiness{
			Summary:       "This GitHub connection cannot reach any repositories yet.",
			ErrorCategory: setupwizard.ErrorCategoryNotConfigured,
		}
	}
	return setupwizard.StepReadiness{
		Summary:       "Choose the one repository this workspace will triage.",
		ErrorCategory: setupwizard.ErrorCategoryNotConfigured,
		Options:       options,
	}
}

// overallReadiness backs the readiness and summary steps: the connection must
// work AND the bound repository must still be reachable with it.
//
// Both halves matter. A token replaced with a narrower one can pass the
// connection check while no longer being able to see the repository this
// workspace was bound to, and reporting that workspace as ready would be a
// lie the user only discovers when triage returns nothing.
func (a *SetupAdapter) overallReadiness(ctx context.Context, ws *agentworkspace.Workspace) setupwizard.StepReadiness {
	identity, err := a.conn.TestConnection(ctx)
	if err != nil {
		return connectionErrorReadiness(err, "Connect GitHub in Settings, then return here.")
	}

	bound, ok := BoundRepo(ws)
	if !ok {
		return setupwizard.StepReadiness{
			Summary:       "No repository is chosen for this workspace yet.",
			ErrorCategory: setupwizard.ErrorCategoryNotConfigured,
		}
	}

	if err := a.conn.CheckRepository(ctx, bound); err != nil {
		var connErr *ConnectionError
		if errors.As(err, &connErr) && connErr.Category == ErrorCategoryInsufficientScope {
			return setupwizard.StepReadiness{
				Blocked: true,
				Summary: "GitHub is connected as @" + identity.Login +
					", but this token cannot reach " + bound + ". Reconnect with a token that has access to it.",
				ErrorCategory: setupwizard.ErrorCategoryPermissionRequired,
			}
		}
		return connectionErrorReadiness(err, "Ori could not check "+bound+".")
	}

	return setupwizard.StepReadiness{
		Ready:   true,
		Summary: "Connected as @" + identity.Login + ", triaging " + bound + ". Nothing is written without your confirmation.",
	}
}

// connectionErrorReadiness translates a ConnectionError into the wizard's own
// vocabulary. The domain's plain-language message is reused verbatim -- it is
// already token-free and written for a human -- with a next step appended.
func connectionErrorReadiness(err error, nextStep string) setupwizard.StepReadiness {
	var connErr *ConnectionError
	if !errors.As(err, &connErr) {
		return setupwizard.StepReadiness{
			Blocked:       true,
			Summary:       "Ori could not check the GitHub connection.",
			ErrorCategory: setupwizard.ErrorCategoryDomainError,
		}
	}

	summary := connErr.Message
	if nextStep != "" {
		summary += " " + nextStep
	}

	switch connErr.Category {
	case ErrorCategoryNotConnected:
		return setupwizard.StepReadiness{
			Summary:       summary,
			ErrorCategory: setupwizard.ErrorCategoryNotConfigured,
		}
	case ErrorCategoryVaultLocked:
		return setupwizard.StepReadiness{
			Blocked:       true,
			Summary:       summary,
			ErrorCategory: setupwizard.ErrorCategoryPermissionRequired,
		}
	case ErrorCategoryInvalidToken, ErrorCategoryInsufficientScope:
		// Blocked, not merely unfinished: a revoked or under-scoped token
		// must never read as "not started yet", which would let a broken
		// workspace look like one the user simply had not set up.
		return setupwizard.StepReadiness{
			Blocked:       true,
			Summary:       summary,
			ErrorCategory: setupwizard.ErrorCategoryPermissionRequired,
		}
	default:
		return setupwizard.StepReadiness{
			Blocked:       true,
			Summary:       summary,
			ErrorCategory: setupwizard.ErrorCategoryDomainError,
		}
	}
}

// repositoryOptions renders repositories as wizard choices, with the current
// selection marked and sorted first so it is never buried in a long list.
func repositoryOptions(repos []Repository, selected string) []setupwizard.StepOption {
	if len(repos) == 0 {
		return nil
	}

	sorted := make([]Repository, len(repos))
	copy(sorted, repos)
	sort.SliceStable(sorted, func(i, j int) bool {
		iSel := strings.EqualFold(sorted[i].FullName, selected)
		jSel := strings.EqualFold(sorted[j].FullName, selected)
		if iSel != jSel {
			return iSel
		}
		return false
	})

	limit := min(len(sorted), maxRepositoryOptions)

	options := make([]setupwizard.StepOption, 0, limit)
	for _, repo := range sorted[:limit] {
		options = append(options, setupwizard.StepOption{
			ID:          repo.FullName,
			Label:       repo.FullName,
			Description: repositoryDescription(repo),
			Selected:    strings.EqualFold(repo.FullName, selected),
		})
	}
	return options
}

func repositoryDescription(repo Repository) string {
	visibility := "Public"
	if repo.Private {
		visibility = "Private"
	}
	switch repo.OpenIssues {
	case 0:
		return visibility + " · no open issues"
	case 1:
		return visibility + " · 1 open issue"
	default:
		return fmt.Sprintf("%s · %d open issues", visibility, repo.OpenIssues)
	}
}

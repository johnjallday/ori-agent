package overview

import (
	"sort"

	"github.com/johnjallday/ori-agent/tools/herdr-devflow/internal/herdr"
	"github.com/johnjallday/ori-agent/tools/herdr-devflow/internal/planning"
	"github.com/johnjallday/ori-agent/tools/herdr-devflow/internal/worktree"
)

// This file turns the feature-first evidence into a complete roster.
//
// A feature-first snapshot answers "what is happening to each feature". It
// cannot answer "which agents are open", because an agent working in the
// repository's `dev` or `main` checkout implements no feature and therefore had
// nowhere to appear. `herdr agent list` showed those agents while `wt herd
// status` did not, which is why the two surfaces disagreed.
//
// The roster is derived, never collected a second time: feature-scoped rows are
// exactly the rows already attached to their feature, and repository-scoped
// rows are the live agents left over once every feature has claimed its own.

// BuildCheckouts describes every working copy of the repository together with
// the panes observed inside it. Occupancy counts panes, agent-bearing or not,
// because "someone is sitting in this checkout" and "an agent is running there"
// are different facts and cleanup depends on the difference.
func BuildCheckouts(inventory worktree.Inventory, evidence AgentEvidence) []Checkout {
	checkouts := make([]Checkout, 0, len(inventory.Checkouts))
	for _, checkout := range inventory.Checkouts {
		row := Checkout{
			Path:     identityPath(checkout.Path),
			Branch:   identityField(checkout.Branch),
			Feature:  checkout.Slug,
			Baseline: checkout.Baseline,
			Source:   checkout.Source,
			Detached: checkout.Detached,
		}
		if evidence.Availability == AvailabilityAvailable {
			for _, agent := range agentsInWorktree(checkout.Path, evidence) {
				row.Occupancy++
				if agent.Agent != "" {
					row.Agents++
				}
			}
		}
		checkouts = append(checkouts, row)
	}
	sort.SliceStable(checkouts, func(i, j int) bool { return checkouts[i].Path < checkouts[j].Path })
	return checkouts
}

// BuildRoster returns the complete agent roster and the findings raised by the
// agents that belong to no feature. It also grades every agent's Overnight Run
// eligibility, stamping the result onto the feature rows it was given so the
// grouped and flat views cannot disagree.
//
// features must already be sorted, because the roster preserves their order:
// the roster is the same evidence regrouped, so a reader comparing the two
// surfaces sees the same agents in the same sequence.
func BuildRoster(features []Feature, inventory worktree.Inventory, evidence AgentEvidence, claude ClaudeReadinessFunc) ([]Agent, []Finding) {
	roster := make([]Agent, 0, len(evidence.Live))
	claimed := map[string]struct{}{}
	for index := range features {
		feature := &features[index]
		for position := range feature.Agents {
			agent := &feature.Agents[position]
			// Eligibility is stamped onto the feature row as well as the roster
			// copy: two surfaces disagreeing about whether an agent may be
			// controlled is exactly the class of drift this snapshot exists to
			// remove.
			agent.Eligibility = evaluateEligibility(*agent, feature, claude)
			roster = append(roster, *agent)
			if agent.Live.Pane != "" {
				claimed[agent.Live.Pane] = struct{}{}
			}
		}
	}
	if evidence.Availability != AvailabilityAvailable {
		// Without a live listing there is nothing to add: saved bridge roles are
		// already in the roster, and inventing repository rows from a failed
		// observation would be exactly the guess this package refuses to make.
		return roster, nil
	}

	repository, findings := repositoryAgents(inventory, evidence, claimed)
	for index := range repository {
		repository[index].Eligibility = evaluateEligibility(repository[index], nil, claude)
	}
	return append(roster, repository...), findings
}

// repositoryAgents finds live agents working in a checkout of this repository
// that implements no feature.
//
// They are reported, never enrolled: a pane open in `dev` is usually a human's
// own terminal, and the bridge has no record of what it is meant to be doing.
func repositoryAgents(inventory worktree.Inventory, evidence AgentEvidence, claimed map[string]struct{}) ([]Agent, []Finding) {
	var rows []Agent
	for _, candidate := range evidence.Live {
		if _, taken := claimed[candidate.PaneID]; taken {
			continue
		}
		// A pane running no agent is occupancy, counted on the checkout row.
		if candidate.Agent == "" {
			continue
		}
		checkout, found := checkoutFor(inventory, candidate, evidence)
		if !found {
			// A pane can report no usable working directory — a workspace
			// hosting a tab per feature deliberately refuses to answer for its
			// panes, because guessing would attribute an agent to a sibling
			// feature's branch. The agent is still ours if its workspace is
			// bound to this repository, and an agent we can see but cannot place
			// must be reported as unplaced rather than left out of the roster.
			if row, ours := unplacedAgent(inventory, candidate, evidence); ours {
				rows = append(rows, row)
			}
			// Otherwise the agent is working somewhere else entirely. Another
			// repository's agents are not this repository's business.
			continue
		}
		if checkout.Slug != "" {
			// A feature worktree claims its own agents; reaching here means the
			// feature row was built without them, which the feature's own
			// findings already describe.
			continue
		}
		rows = append(rows, Agent{
			Scope:              AgentScopeRepository,
			Managed:            false,
			Kind:               identityField(candidate.Agent),
			Live:               liveIdentity(candidate),
			Status:             AgentStatus(normalizeStatus(candidate.AgentStatus)),
			StatusAvailability: AvailabilityAvailable,
			// There is no saved role to compare against, so binding health is
			// not "missing" — nothing was ever bound.
			Binding:       BindingUnavailable,
			BindingDetail: "this agent is working in " + checkoutLabel(checkout) + ", which implements no feature",
			MatchedPath:   identityPath(checkout.Path),
		})
	}
	sort.SliceStable(rows, func(i, j int) bool {
		if rows[i].MatchedPath != rows[j].MatchedPath {
			return rows[i].MatchedPath < rows[j].MatchedPath
		}
		return rows[i].Live.Pane < rows[j].Live.Pane
	})

	var findings []Finding
	for _, row := range rows {
		finding := Finding{
			Code:     FindingAgentUnscoped,
			Severity: SeverityInfo,
			Source:   SourceHerdr,
			Message:  "A live agent is working in a checkout that implements no feature, so it cannot be managed by the bridge.",
			Detail:   "Agent " + row.Live.Session + " in " + row.MatchedPath + ".",
		}
		if row.Scope == AgentScopeUnknown {
			finding.Message = "A live agent in this repository reported no working directory, so it could not be placed in a checkout."
			finding.Detail = "Agent " + row.Live.Session + " in workspace " + row.Live.Workspace + "."
		}
		findings = append(findings, finding)
	}
	return rows, findings
}

// unplacedAgent builds the row for an agent that belongs to this repository but
// could not be placed in one of its checkouts.
//
// Membership comes from Herdr's own workspace-to-repository binding, which is
// authoritative about the repository even when it can no longer identify which
// checkout a single pane is in. The row is deliberately scoped unknown: it is
// visible, and it is not a candidate for anything that needs an exact worktree.
func unplacedAgent(inventory worktree.Inventory, agent herdr.AgentInfo, evidence AgentEvidence) (Agent, bool) {
	if !boundToRepository(inventory, agent, evidence) {
		return Agent{}, false
	}
	return Agent{
		Scope:              AgentScopeUnknown,
		Managed:            false,
		Kind:               identityField(agent.Agent),
		Live:               liveIdentity(agent),
		Status:             AgentStatus(normalizeStatus(agent.AgentStatus)),
		StatusAvailability: AvailabilityAvailable,
		Binding:            BindingUnavailable,
		BindingDetail:      "this agent's working directory could not be resolved, so it could not be placed in a checkout",
	}, true
}

// boundToRepository reports whether Herdr's workspace binding places an agent
// in this repository. Labels are never consulted: they are user-editable and
// two workspaces have been observed sharing one label across two checkouts.
func boundToRepository(inventory worktree.Inventory, agent herdr.AgentInfo, evidence AgentEvidence) bool {
	for _, workspace := range evidence.Workspaces {
		if workspace.WorkspaceID != agent.WorkspaceID || workspace.Worktree == nil {
			continue
		}
		binding := workspace.Worktree
		if _, ok := inventory.CheckoutFor(binding.CheckoutPath); ok {
			return true
		}
		if inventory.SourcePath != "" && worktree.SameRepository(inventory.SourcePath, binding.RepoRoot) {
			return true
		}
	}
	return false
}

// checkoutFor resolves the checkout an agent is working in. Matching is by
// canonical path against Git's own inventory, never by workspace label: labels
// are user-editable and have been observed to drift.
func checkoutFor(inventory worktree.Inventory, agent herdr.AgentInfo, evidence AgentEvidence) (worktree.Checkout, bool) {
	path := agentWorktree(agent, evidence.Workspaces)
	if path == "" {
		return worktree.Checkout{}, false
	}
	return inventory.CheckoutFor(path)
}

// checkoutLabel names a checkout the way an operator refers to it.
func checkoutLabel(checkout worktree.Checkout) string {
	switch {
	case checkout.Branch != "":
		return "the " + identityField(checkout.Branch) + " checkout"
	case checkout.Detached:
		return "a detached checkout"
	default:
		return "another checkout of this repository"
	}
}

// identityPath sanitizes a filesystem path for display, which is legitimately
// longer than a short identity field like a pane ID.
func identityPath(value string) string {
	return planning.Sanitize(value, maxIdentityPathRunes)
}

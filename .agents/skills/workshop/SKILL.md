---
name: workshop
description: Run an open-ended Ori workshop when the user wants to bounce between their own ideas, see mockups, investigate questions, or park a thought for later. On request, turn selected ideas into a proposed PR map. Not a backlog scan or an implementation session.
---

# Workshop

Follow the user's train of thought. Start with their first topic; if none is given, ask “What's on your mind first?” Don't require a theme, agenda, or commitment to ship.

## Follow the topic

- **“Show me.”** Inspect the relevant UI or docs before sketching changes. Show two or three rough alternatives in chat, side by side when legible. Use realistic labels and different interactions, not just different styling. Give one tradeoff per option. Let the user combine or discard them. When a direction sticks, walk through a normal use and one awkward case.
- **“Why does this happen?”** Read the smallest relevant piece of the repo. Cite the path that explains today's behavior; distinguish what you found from what you would change. Nonvisual questions don't need a mockup.
- **“Another thing…”** Leave a short conversational bookmark for the current topic and follow the new one immediately. “Park that” only needs an acknowledgment, not an investigation. On “back to X,” resume from the last choice or open question instead of restarting discovery.

Keep a lightweight list **in the conversation**: topic, explicit picks, and what's still open or parked. Don't print the whole list every turn. “Where are we?” shows it; “wrap up” returns a concise recap the user can paste into a future session. A passing thought is not a decision, and liking a sketch is not approval to build it.

## Only map PRs on cue

On “plan the workspace change,” map only that topic. On “plan the PRs,” use the user's selected outcomes; if selection is unclear, ask which to include rather than promoting every idea. For each proposed PR, give a title, user-visible outcome, included and deferred scope, a concrete demo or acceptance check, and dependencies. Recommend a first PR and identify what can proceed independently. Split by outcomes someone can use and verify, not by screen, mockup, or code layer.

Stop at the proposed map. This is a read-only workshop: don't write notes or prototypes to disk, create Issues, PRDs, task lists, branches, worktrees or PRs, or implement changes. If the user explicitly wants formal planning, hand the selected topic and its decisions to the [task-planning skill](../task-planning/SKILL.md) as a separate step; don't silently turn the workshop into delivery.

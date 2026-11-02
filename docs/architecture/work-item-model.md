# One work-item model

Notes and follow-ups are your commitments, in HQ and the second brain. A ticket is work a workspace
owns. A run is one attempt at a ticket.

That sentence is the whole model. Other documents link here instead of restating it.

## Tickets

A ticket and a workspace's stored task are the same record. Its state is one of six: Backlog, Ready,
In Progress, Review, Done and Cancelled. The legacy task status is an internal projection kept for
old records and the executor; nothing a person reads or decides on comes from it.

- One count. Every surface reads the server's `ticket_counts` and words it the same way ("1 in
  review · 2 in progress · 3 ready", or "No open work").
- One way in. Every person-facing door opens Give work: nobody chosen is Backlog, an agent is Ready,
  and Start now is Ready plus one run. A refused start leaves the ticket Ready and says why.
- Done takes a person. A run can send a ticket to Review; only a person marks it Done.

## Runs

A run is one attempt. Its result, trace and output settings belong to the run and are read from the
ticket's detail or the Runs page (`/workspaces/{slug}/task/{id}`). A schedule belongs to a ticket and
each firing is a run; a firing never reopens a ticket a person closed.

## Commitments

Follow-ups and second-brain notes stay commitments. They reach workspace work through links, never
ownership:

- **Make a ticket** on a follow-up creates one Backlog ticket and links both ways. Closing either one
  leaves the other.
- An open line under `Projects/<name>/` in the second brain becomes a ticket in the workspace whose
  primary folder is exactly that project, when the person accepts the look. Every other line stays a
  follow-up. A ticked or removed line leaves the ticket's state alone and shows a note.
- **Start this** moves a Backlog or Ready ticket on, asks who when nobody is named, and starts one run.

Workspace findings stay a third kind, separate from both.

## Words

People read "ticket" and "run", never "task". `scripts/check-user-facing-task-copy.mjs` enforces
that over templates and browser modules; `scripts/task-copy-allowlist.json` names any file that may
still say it, with its reason. Code, routes and stored fields keep their old names.

# Ori’s REAPER integration

Ori’s REAPER integration is an optional local contribution for organizing and assisting with REAPER projects. It is not an audio plug-in, VST, effect, or instrument, and it is not installed into REAPER’s FX path.

## Start and resume setup

After a settled Personal Assistant recognizes music-production work, accepting its offer opens a guided setup journey. Acceptance records only that choice. It does not install software, connect a folder, create a workspace or agent, open REAPER, or enable live control.

Each later change has its own review and confirmation. Choose **Do this later** at any point. Home’s Today view reports the server-derived setup state and provides **Review setup** or **Continue setup** without repeating completed consequences.

The journey can either:

- connect an existing folder after you choose the exact folder and, when needed, its authoritative `.rpp` file; or
- create a new managed project from the reviewed Reaper Song blueprint.

An existing external project stays where it is. Ori does not move it or write Ori metadata into that external folder.

## File-only and Ori-assisted modes

**File-only** is a complete supported mode. It uses the exact project files you approved and ordinary task confirmations. It does not configure or test Web Remote, stage a runner, grant live-control scopes, or require REAPER to be open.

**Ori-assisted** is optional. Its setup page shows each permission-bearing action immediately before confirmation. REAPER application detection, Web Remote checks, runner registration checks, exchange-root checks, and open-project verification are read-only. Ori does not automate REAPER Preferences, the Action List, macOS privacy prompts, or REAPER configuration files.

A blocked or later-regressed live check affects only live operation. Existing projects, file-only work, staffing, tasks, portfolio records, and sample records remain readable. Return to the project’s setup panel to repair live control or continue in File-only mode.

## Home and project roles

One **Music Production Home** groups exact linked projects without inheriting their folders or runtime grants.

- The Home-scoped **Music Portfolio Manager** can report reviewed project status, maintain Home-owned portfolio fields, and prepare a confirmed handoff to one exact linked project.
- Each project has its own Producer, Mix Engineer, and Songwriter bindings. Their prompts, model choices, memory, task history, grants, and live state are not shared with sibling projects.
- No configured model is required for deterministic setup and catalog operations. Chat or execution is labelled unavailable until a compatible model resolves.

Connecting another project creates an independent resumable child setup. It reuses the same compatible integration and Home, while requiring explicit project connection, mode, and project-team review for the new child.

## Authorized folders and Sample Library

A project receives access only to its exact reviewed project folder. Parentage under Music Production Home is organization, not permission inheritance.

The optional **Sample Library** add-on is separate from the optional Sample Library Manager role. Connecting an exact sample folder does not scan it. **Index metadata** performs one bounded scan when requested. Content analysis is a separate per-folder choice limited to reviewed hashing and embedded-tag readers; Ori does not decode audio, infer BPM/key, render waveforms, transcribe, audition, upload, or execute samples.

Projects can search the active Home catalog without receiving the source-folder grant. A sample handoff previews and copies only the selected files to one exact linked project destination. Source files remain unchanged.

Revoking a sample folder stops future catalog use and removes its active entries without deleting source files or confirmed project copies. Removing the add-on follows the same preservation rule.

## Lifecycle and migration

Disabling or removing the integration pauses plugin-backed execution and marks setup as needing attention. It does not delete Home, linked projects, agents, tasks, external folders, portfolio data, sample metadata, or confirmed copies.

Compatible older plugin-backed workspaces can be attached only through an explicit migration review. Legacy shared rosters remain readable until reviewed; ambiguous or built-in topology is never silently renamed, cloned, moved, or reassigned. Linked projects must be explicitly disconnected before organizational reparenting. Removing Music Production Home uses a dedicated impact review and preserves child projects and external files by default.

## Release-candidate note

The reviewed plugin source lives in `plugins/src/reaper-plugin`. Its local artifact and checksum are generated and verified separately from Ori’s host changes. A candidate marked not release-ready is for isolated review and demos only; this workflow does not publish an artifact or create a GitHub release.

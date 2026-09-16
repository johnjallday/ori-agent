# Ori’s REAPER integration

Ori’s REAPER integration is an optional local contribution for organizing and assisting with REAPER projects. It is not an audio plug-in, VST, effect, or instrument, and it is not installed into REAPER’s FX path.

## Start and resume setup

Setup happens in two quests. Opening either one does not install software, connect a folder, create a workspace or agent, open REAPER, or enable live control. See [Plugin-owned setup quests](plugin-setup-quests.md) for the declaration and rollout contract.

**1. Install Ori REAPER Plugin.** Before the plugin is installed, open it from **Plugins → Available integrations → Guided Setup**, from the accepted music-production assistant's setup card, or with `/?setup=quest&source=host&quest=install_ori_reaper`. It has two steps:

1. **Install Ori REAPER Plugin** — or continue with a verified installed integration. Choose **Install plugin**, check the review, then choose **Install**: Ori downloads the reviewed release, checks its fingerprint, and installs it switched off. Then choose **Enable plugin** and **Enable**. Installed, enabled, and verified are separate states. An existing installation from the official unpinned Git URL offers **Review verified replacement**, including when its version already matches the pin. Review the disclosure, then choose **Replace with reviewed version**. This uses the pinned release, preserves the enabled state, and does not delete workspaces or project files; runtime access may need review again. Cancel changes nothing. **Check Again** refreshes status without installing or replacing anything.
2. **REAPER plugin ready** — shows the installed version, with **Continue: Set up REAPER** as the primary action and **Open Plugins**. Continue opens the plugin's quest in the same window.

**2. Set up REAPER.** The installed plugin's own quest. Open it from Continue, **Plugins → Guided Setup**, the Reaper Song template's **Open Guided Setup** in the workspace picker or on **Templates**, or the assistant's setup card once the plugin is installed. All entries resume the same saved quest without requiring an accepted Personal Assistant offer. It has two screens:

1. **Build Your Music Production Group** — choose **Build Group** to open the shared Create Workspace dialog in its fixed guided-Group context, with **Music Production** prefilled. Review the name and build the canonical Music Production Home, or reuse the existing Home unchanged. This creates no agents, projects, schedules, or access grants. The ordinary Group creator instead presents a Group Roster before final creation; its reviewed Manager is scoped to that group's files and notes rather than member project access. Use this screen for the setup-specific Home.
2. **Create New Workspace** — the shared creator opens at **Details → Team → Review**, with the group and Reaper Song blueprint selected. Choose **Create New Project** or **Import Existing Project**. Its review covers the exact project files, File-only starting mode, and separately scoped group/project roles. Opening the project application is off by default in this flow.

There is no separate REAPER preparation screen. Web Remote, runner and exact-project checks belong to the new workspace's own Setup Wizard in workspace Settings.

If the plugin is later disabled, removed or unverified, the quest shows one **The integration needs attention** panel with **Open install quest**. If saved progress came from an older quest layout, the quest offers **Start over**, which resets only setup progress and keeps the group, project and team.

Group creation and workspace creation are separately confirmed. Choose **Do this later** at any point. Home’s Today view reports the server-derived setup state and provides **Review setup** or **Continue setup** without repeating completed consequences.

The journey can either:

- connect an existing folder after you choose the exact folder and, when needed, its authoritative `.rpp` file; or
- create a new managed project from the reviewed Reaper Song blueprint.

An existing external project stays where it is. Ori does not move it or write Ori metadata into that external folder.

## File-only and Ori-assisted modes

**File-only** is a complete supported mode. It uses the exact project files you approved and ordinary task confirmations. It does not configure or test Web Remote, stage a runner, grant live-control scopes, or require REAPER to be open.

**Ori-assisted** is optional. Its setup page shows each permission-bearing action immediately before confirmation. REAPER application detection, Web Remote checks, runner registration checks, exchange-root checks, and open-project verification are read-only. Ori does not automate REAPER Preferences, the Action List, macOS privacy prompts, or REAPER configuration files.

A blocked or later-regressed live check affects only live operation. Existing projects, file-only work, staffing, tasks, portfolio records, and sample records remain readable. Return to the project’s setup panel to repair live control or continue in File-only mode.

## Home and project roles

The music group is the canonical **Music Production Home**, not an extra wrapper. Its chosen display name does not change ownership. It groups exact linked projects without inheriting their folders or runtime grants.

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

## Reviewed release and recovery

Ori’s reviewed registry now enables the published macOS arm64 `v0.6.0` release:

- Immutable source commit: `03af9fda3e6b9d8cc3c0496c5e9ef6df99e870b9` (the annotated `v0.6.0` tag’s resolved commit).
- Published asset: `reaper-plugin_v0.6.0_darwin_arm64`, **8,780,098 bytes**.
- SHA-256: `4def4fec14ecf083b0358c686c608514d4b9afff99dd810f1184213312770119`.
- Release: https://github.com/johnjallday/reaper-plugin/releases/tag/v0.6.0 (published September 15, 2026).
- Source CI: https://github.com/johnjallday/reaper-plugin/actions/runs/35020185810.
- Release workflow: https://github.com/johnjallday/reaper-plugin/actions/runs/35020185797.
- Manifest identity at that commit: blueprint `reaper-song` version 7, assistant program `music-producer-assistant` schema 2, setup quest `reaper_setup` version 2 with four steps, required host features `assistant_program_v1`, `specialist_setup_journey_v1`, `setup_quests_v2` and `template_group_requirements_v1`.

For host enablement, the actual published asset and checksum were downloaded and compared against the manifest at the tag’s resolved commit. Size and digest matched; the executable reported `0.6.0`. The previous `v0.5.0` pin was replaced, not merely enabled. A repeatable GitHub-backed check exercises both fresh reviewed install → separate enable and ordinary official-URL install → reviewed same-version replacement, without a development override:

```bash
ORI_TEST_REVIEWED_INTEGRATION_RELEASE=1 go test ./internal/setupjourney \
  -run '^TestReviewedIntegrationPublishedRelease$' -count=1 -v
```

This opt-in check uses temporary plugin stores and inert component registrars. It downloads and verifies release bytes but does not launch a plugin service, open or control REAPER, or touch user workspaces. The ordinary-URL fixture intentionally fails for review if the external default branch changes versions. Release/install verification is not a live-project verification claim.

Older Ori builds may still report an identity mismatch or an unavailable reviewed release even after the plugin is installed. Update Ori first: reinstalling the same unpinned plugin does not enable the host’s release gate. The recovery action accepts only the exact official repository URLs and its previously accepted pins; unrelated/local sources, incompatible formats/platforms, and newer or unrecognized versions do not bypass verification. A replacement is never applied from a status read or review alone.

### Pin history and moving the pin

Plugin **0.6.0** is the first release that declares its setup quest under `setup_quests_v2`: `reaper_setup` version 2 with four steps and blueprint version 7. This Ori host no longer supports `setup_quests_v1`. Installed v0.5.1 and v0.5.2 plugins require it, so they fail closed: their manifests are refused until the plugin is updated. The previous pin, `v0.5.0` at commit `1f494db5a39d8c13f6149943b28e6a506d19631a` (SHA-256 `2bbf6b77418119cb21e827a407c8d5886e3effdb593ec0ad274e20d7d69c2ca9`), declared no quest, so its install quest offered only **Open Plugins**.

A stale pin has one visible symptom worth recognising: the gate never accepts an installation **newer** than the pin and never offers a downgrade, so once a newer release is installed from the official URL the install step reports “Ori could not verify this installation against the reviewed source, format, and version” with only **Manage integration** available. The fix is to move the pin in Ori, not to reinstall the plugin.

Move the pin only through this procedure:

1. Confirm the new tag and release exist on `johnjallday/reaper-plugin`, and record the tag's resolved commit.
2. Download the published `darwin_arm64` asset and its checksum. Compare size and SHA-256 against the manifest at that commit, and confirm the executable reports the new version.
3. In `internal/reviewedintegration/entries.go`, set `ExpectedVersion`, `SourceCommit`, `ExpectedBlueprintVersion` and `RequiredHostFeatures` from that manifest. Update the registry test, the artifact digest in the published-release check, and this section's evidence list.
4. Run the opt-in published-release check above against the new pin.

The locally built candidate is not release evidence. A squash merge upstream changes its commit identity, so always pin the published tag's commit.

Local plugin development remains separate. `scripts/reaper-demo.sh` stages an isolated copy and uses an explicit process-local source override; it labels the copy **not release-verified**. Installing a local directory or setting an arbitrary override is not a production recovery path.

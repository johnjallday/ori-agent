# Ori’s REAPER integration

Ori’s REAPER integration is an optional local contribution for organizing and assisting with REAPER projects. It is not an audio plug-in, VST, effect, or instrument, and it is not installed into REAPER’s FX path.

## Independent Music Production Home (unreleased source contract)

The new source contract separates two optional providers:

- **Music Project Management** owns Music Production Home, its Portfolio Manager and optional Sample Library Manager declarations, Home stages/reflection bounds, and the canonical `music-project-management` skill. It is content-only: no REAPER dependency, project blueprint, Workspace Surface, MCP server, or runtime process.
- **REAPER Plugin** owns the Reaper Song blueprint, Producer/Mix Engineer/Songwriter project team, `.rpp` scaffold and typed inputs, project setup, REAPER skills/capabilities, and optional live-control service. It references the Home by exact provider/program identity rather than owning or bundling it.

Both independent contribution forms require `independent_program_homes_v1`. Either package can be installed first. REAPER-first grouped creation reports the missing Music provider and does not fetch it or create a fallback Home; Music-first can create and staff the Home before any REAPER project exists. An explicitly customized standalone Reaper Song remains Home-free and needs only its three project roles.

This contract has been accepted only with unpublished local candidates: Music Project Management 0.1.0 at `8f4abd0b283fefe23653a2cf81deb800123c4bde` and REAPER 0.8.0 / blueprint v9 at `68488b4d62978f22bfdff26c3554cebb6b4cf396`. It does not change the published reviewed floor below. Delivery order is compatible Ori host, Music package, then compatible REAPER release.

See [Independent Assistant Program Homes](architecture/independent-program-homes.md) for the exact declaration, reciprocal authorization, provenance, availability, and no-migration contract.

## Start and resume setup

Setup happens in two quests. Opening either one does not install software, connect a folder, create a workspace or agent, open REAPER, or enable live control. See [Plugin-owned setup quests](plugin-setup-quests.md) for the declaration and rollout contract.

**1. Install Ori REAPER Plugin.** Before the plugin is installed, open it from **Plugins → Available integrations → Guided Setup**, from the accepted music-production assistant's setup card, or with `/?setup=quest&source=host&quest=install_ori_reaper`. It has two steps:

1. **Install Ori REAPER Plugin** — or continue with a verified installed integration. Choose **Install plugin**, check the review, then choose **Install**: Ori downloads the latest reviewed release, checks its fingerprint, and installs it switched off. The review shows the **Release version** and the **Minimum reviewed version**. Then choose **Enable plugin** and **Enable**. Installed, enabled, and verified are separate states. An existing installation from the official unpinned Git URL, or from an exact commit older than the minimum, offers **Review verified replacement**, including when its version already matches the latest release. Review the disclosure, then choose **Replace with reviewed version**. This installs the latest release’s exact commit, preserves the enabled state, and does not delete workspaces or project files; runtime access may need review again. Cancel changes nothing. **Check Again** refreshes status without installing or replacing anything.
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

The music group is the canonical **Music Production Home**, not an extra wrapper. Its chosen display name does not change ownership. New independent Homes are identified by owner, Music provider, and program ID—not by the REAPER provider, parentage, or display name. They group exact linked projects without inheriting their folders or runtime grants.

- Music Project Management owns the Home-scoped **Music Portfolio Manager**, which can report reviewed project status, maintain Home-owned portfolio fields, and prepare a confirmed handoff to one exact linked project.
- REAPER owns each project's Producer, Mix Engineer, and Songwriter bindings. Their prompts, model choices, memory, task history, grants, and live state are not shared with the Home or sibling projects.
- The Home and project declarations carry separate immutable provider versions, generations, fingerprints, and digests on each exact link. Both providers must be available for a handoff; Home-only edits and project-only staffing retain their narrower provider gates.
- No configured model is required for deterministic setup and catalog operations. Chat or execution is labelled unavailable until a compatible model resolves.

Connecting another project creates an independent resumable child setup. It reuses the same compatible integration and Home, while requiring explicit project connection, mode, and project-team review for the new child.

## Authorized folders and Sample Library

A project receives access only to its exact reviewed project folder. Parentage under Music Production Home is organization, not permission inheritance.

The optional **Sample Library** add-on is separate from the optional Sample Library Manager role. Connecting an exact sample folder does not scan it. **Index metadata** performs one bounded scan when requested. Content analysis is a separate per-folder choice limited to reviewed hashing and embedded-tag readers; Ori does not decode audio, infer BPM/key, render waveforms, transcribe, audition, upload, or execute samples.

Projects can search the active Home catalog without receiving the source-folder grant. A sample handoff previews and copies only the selected files to one exact linked project destination. Source files remain unchanged.

Revoking a sample folder stops future catalog use and removes its active entries without deleting source files or confirmed project copies. Removing the add-on follows the same preservation rule.

## Independent lifecycle and no migration

Disabling or removing REAPER pauses REAPER-owned operations and project-provider actions while preserving the independent Home, manager, linked project records, project teams, tasks, and files. Disabling or removing Music Project Management makes Home coordination and its managed skill unavailable while preserving REAPER project-local and file-only state. Reads do not reinstall either provider or rewrite persisted availability. Reinstalling the same exact providers restores availability without creating another Home, manager, project, link, handoff, grant, or schedule.

The independent contract is fresh-setup only. Existing combined schema-1/2 Homes and links remain readable under their recorded REAPER-owned identity. A same-name legacy Home is not adopted, transferred, copied, relinked, rewritten, or deleted. The older explicit migration path for compatible combined workspaces remains separate and does not convert ownership to Music Project Management.

Linked projects must be explicitly disconnected before organizational reparenting. Removing Music Production Home uses a dedicated impact review and preserves child projects and external files by default.

## Reviewed floor and latest release

Ori’s currently published reviewed registry entry for this integration is a **floor**, not a pin. It describes the older combined release contract and is intentionally unchanged by unpublished independent-Home candidates. A person reviewed the `johnjallday/reaper-plugin` repository and its minimum release; every later stable release from that repository is accepted once Ori’s automatic identity, host-feature, blueprint, program, platform and artifact checks pass. The entry in `internal/reviewedintegration/entries.go` holds:

- Minimum reviewed version `0.6.1`.
- Fallback commit `e11ca2942279af02a9a035039b18b146ff9fc89d` (the annotated `v0.6.1` tag’s resolved commit), installed when the latest release cannot be checked.
- Blueprint `reaper-song` at version 7 **or later**.
- Combined Assistant Program `music-producer-assistant` schema 2 and surface protocol 1, both **exact**: they describe what the currently reviewed release line can run, not the unpublished split candidate.
- Required host features `assistant_program_v1`, `specialist_setup_journey_v1`, `setup_quests_v2` and `template_group_requirements_v1`. A release may require more, as long as this Ori build has them.
- Platform `darwin/arm64`.

### How the latest release is chosen

When the install step is read, Ori resolves the latest stable release at or above the floor:

1. It lists the repository’s GitHub releases (`GET https://api.github.com/repos/johnjallday/reaper-plugin/releases?per_page=30`), sending `GITHUB_TOKEN` or `GH_TOKEN` as a bearer token when one is set. Drafts, prereleases, tags that are not versions after removing a leading `v`, versions with a prerelease suffix such as `-rc.1`, and versions below the floor are skipped. The highest remaining version wins.
2. It resolves that tag to a commit with `git ls-remote --tags` against the reviewed repository, using the peeled commit of an annotated tag.
3. The install step installs `https://github.com/johnjallday/reaper-plugin#sha=<commit>`. The downloaded artifact’s size and SHA-256 are still verified against the manifest at that commit, exactly as before.

The result is cached in the Ori process for one hour, and the daily plugin update check refreshes it. While the releases API is failing, Ori retries at most every five minutes. One lookup has a ten-second budget. The review is bound to the release it shows: if the latest release changes between review and confirmation, the confirmation is refused as stale and must be reviewed again.

### Which installations the guided setup accepts

| Installed from                                           | Version                                 | Guided setup                                                                                                                                                                                     |
| -------------------------------------------------------- | --------------------------------------- | ------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------ |
| An exact official commit (`…#sha=<40 hex>`)              | At or above the floor                   | **Verified** against its own version, with no network lookup. The step stays complete when a newer release appears; that update shows only on the Plugins page and in Home’s **Updates** flyout. |
| An exact official commit                                 | Below the floor                         | **Review verified replacement** with the latest release.                                                                                                                                         |
| The official unpinned URL (`…/reaper-plugin` or `….git`) | Any, up to the latest release           | **Review verified replacement** with the latest release, even at the same version number.                                                                                                        |
| The official unpinned URL                                | Newer than the latest release Ori knows | Blocked with **Manage integration**. Ori never offers a downgrade.                                                                                                                               |
| The configured local development copy                    | At or above the floor                   | Accepted and labelled **Local development copy — not release-verified**.                                                                                                                         |
| Anything else                                            | Any                                     | Blocked with **Manage integration**.                                                                                                                                                             |

A replacement or install is never applied from a status read or a review alone. Older Ori builds, from before the floor, still pin one exact release and report an identity mismatch for a newer installation; update Ori rather than reinstalling the plugin.

### Updates on the Plugins page

For an installation from an exact official commit, the plugin update check reports the latest release as an update when it is newer than the installed version. **Update** on the Plugins page discloses the new release’s trust report and replaces the plugin from that release’s exact commit, keeping its enabled state. Every other plugin keeps following its recorded source.

### When the latest release cannot be checked

If the releases API is unreachable or rate-limited, Ori uses the last release it resolved in this process, or else the floor’s fallback commit. When the step then offers an install or replacement it adds: “The latest release could not be checked. Ori will install the minimum reviewed version.” The actions are unchanged. Installing still downloads the release asset from GitHub, so a fully offline machine cannot install. Restarting Ori clears the cache.

For demos and end-to-end tests, `ORI_INTEGRATION_RELEASES_API` overrides the releases API. It is accepted only as a plain `http` URL on `127.0.0.1`, `::1` or `localhost`; anything else is ignored with a warning. The tag-to-commit step always reads the real repository, so an override can only choose among official tags at or above the floor. To see the fallback note:

```bash
ORI_INTEGRATION_RELEASES_API=http://127.0.0.1:9 ./scripts/demo-server.sh 8931
```

### When the floor moves

A plugin release that keeps its blueprint version, program schema, protocol and required host features needs **no change to Ori**. The split 0.8.0 candidate does change those assumptions, so it cannot ship under the current floor. Move the floor only after the compatible host and Music package are available and the REAPER release has separately verified publication evidence:

1. Confirm the new tag and release exist on `johnjallday/reaper-plugin`, and record the tag’s resolved commit.
2. Download the published `darwin_arm64` asset and its checksum. Compare size and SHA-256 against the manifest at that commit, and confirm the executable reports the new version.
3. In `internal/reviewedintegration/entries.go`, set `MinimumVersion`, `FallbackCommit`, `MinimumBlueprintVersion` and `RequiredHostFeatures` (and `ExpectedProgramSchema` or `ExpectedProtocol` if they changed) from that manifest. Update the registry test, the fallback artifact digest in the published-release check, and the evidence below.
4. Run the checks below.

The locally built candidate is not release evidence. A squash merge upstream changes its commit identity, so the fallback commit is always the published tag’s commit.

### Verification evidence for the fallback release

- Release: https://github.com/johnjallday/reaper-plugin/releases/tag/v0.6.1 (published September 16, 2026).
- Commit: `e11ca2942279af02a9a035039b18b146ff9fc89d`.
- Published asset: `reaper-plugin_v0.6.1_darwin_arm64`, **8,780,098 bytes**, SHA-256 `88c7dfd5ebf6a855ae41994a080c2339f392514f68ff47366463b5a84c5eb8c8`.
- Source CI: https://github.com/johnjallday/reaper-plugin/actions/runs/35142041208.
- Release workflow: https://github.com/johnjallday/reaper-plugin/actions/runs/35142041273.
- Manifest identity at that commit: blueprint `reaper-song` version 7, assistant program `music-producer-assistant` schema 2, setup quest `reaper_setup` version 2 with four steps, and the four required host features listed above.

The published asset and checksum were downloaded and compared against the manifest at the tag’s resolved commit. Size and digest matched; the executable reported `0.6.1`.

### Rerun the checks

```bash
# Fresh install → separate enable, and official-URL install → reviewed
# replacement, once with the fallback forced and once with the live resolver.
ORI_TEST_REVIEWED_INTEGRATION_RELEASE=1 go test ./internal/setupjourney \
  -run '^TestReviewedIntegrationPublishedRelease$' -count=1 -v

# A candidate checkout or exact-commit source meets the host contract and the floor.
ORI_REVIEWED_PLUGIN_CANDIDATE='https://github.com/johnjallday/reaper-plugin#sha=<commit>' \
  go test ./internal/plugin ./internal/reviewedintegration \
  -run 'TestReviewedCandidateHostContract|TestReviewedCandidateMeetsTheFloor' -count=1 -v
```

The fallback run points the resolver at an unreachable loopback API and checks the fallback asset’s exact size and digest above. The live run resolves the latest release and checks that the installed record carries that release’s commit and that the artifact’s SHA-256 equals the digest its own manifest declares. Both use temporary plugin stores and inert component registrars: they download and verify release bytes but do not launch a plugin service, open or control REAPER, or touch user workspaces. The official-URL fixture fails for review if the external default branch is ahead of the latest release. Unauthenticated GitHub API calls are limited to 60 an hour, so export `GITHUB_TOKEN` for repeated runs. Release/install verification is not a live-project verification claim.

### Release history

- **0.5.0** (commit `1f494db5a39d8c13f6149943b28e6a506d19631a`, SHA-256 `2bbf6b77418119cb21e827a407c8d5886e3effdb593ec0ad274e20d7d69c2ca9`) declared no setup quest, so its install quest offered only **Open Plugins**. v0.5.1 and v0.5.2 require the retired `setup_quests_v1`, so this host refuses their manifests until the plugin is updated.
- **0.6.0** (commit `03af9fda3e6b9d8cc3c0496c5e9ef6df99e870b9`, SHA-256 `4def4fec14ecf083b0358c686c608514d4b9afff99dd810f1184213312770119`) is the first release that declares `reaper_setup` version 2 under `setup_quests_v2`, with blueprint version 7. It is below the floor, so an exact-commit installation is offered a reviewed replacement with the latest release.
- **0.6.1** only drops the retired agent `type` key from the blueprint roles (reaper-plugin#9, following ori-agent#490). It is the current floor. Before the floor, Ori pinned each release exactly and needed a pull request (#494, #502) for every plugin release.

Local plugin development remains separate. `scripts/reaper-demo.sh` stages an isolated copy and uses an explicit process-local source override; it labels the copy **not release-verified**. Installing a local directory or setting an arbitrary override is not a production recovery path.

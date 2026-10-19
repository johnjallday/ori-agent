# Backup & Recovery

**Status:** implementation in progress. Manual setup and a first real observation
have been exercised through the product on an approved disposable USB fixture.
Separate periodic opt-in, checks, pause/resume, restart and revocation have also
been exercised against an invented external backup. Recovery reports and full
delivery acceptance remain incomplete.

Ori observes explicitly approved backup metadata and guides recovery. The external
application (Time Machine or restic) performs backups and restores. Completing setup, choosing folders or
connecting metadata is **not proof that your files are protected**. Core guidance
and status do not require a model.

## Supported integrations

Two observe-only providers, chosen in the console's **Settings**: **Time
Machine**, built into macOS (listed first on a Mac that can read it), and a
**restic** repository on an external drive. Neither lets Ori run, schedule,
pause or restore a backup. Other providers (cloud, NAS, Carbon Copy Cloner,
iCloud Drive) are not supported.

### Time Machine

- Apple Silicon, validated host macOS **26.3.1**, product build **25D771280a**;
  other builds and platforms show Time Machine as unavailable
  (`platform_unsupported` off Apple-silicon macOS).
- No credential, repository or extra software. Ori runs only the system
  `/usr/bin/tmutil` read verbs `destinationinfo` and `status`, sandboxed, and
  reads Time Machine's preferences as data. It never mounts a destination,
  changes settings or exclusions, or starts, stops or restores a backup.
- **Full Disk Access** for the app that runs Ori is needed to see completed
  backups, capacity and exclusion lists. Without it Ori still sees whether a
  destination exists and each folder's own exclusion mark, and says
  "Time Machine needs Full Disk Access" — never "not set up" and never a
  failed backup.

### restic

- Official **restic 0.19.1**, Apple Silicon, repository format **2**.
- Validated host: macOS **26.3.1**, product build **25D771280a**. Other builds,
  binaries and platforms remain unavailable until separately validated.
- Selected folders on internal Apple Fabric APFS storage; one independent,
  physical USB APFS destination and one existing repository inside its actual
  mounted volume root. Cloud/NAS, other Macs and whole-Mac recovery are excluded.
- The server operator supplies the exact trusted executable through
  `ORI_BACKUP_RESTIC_EXECUTABLE` in its launch environment. Ori does
  not download/install software, search for alternative binaries or run commands
  from workspace/template/model text. Missing containment never permits an
  unsandboxed fallback.

## Protect your folders: where to start

When a folder you showed Ori (the folder a workspace was created from or
connected to, or an active library root of a Home) has no backup Ori can see,
Today shows **one** line, for example "3 project folders have no backup Ori can
see", naming up to three workspaces. **Check protection** opens the **Protect
your folders** chooser when no Backup & Recovery workspace exists, otherwise
the owning console's Overview at its coverage section. **Not now** hides the
line; it returns once, when five project folders are uncovered, and a second
"Not now" is final. Downloads, File Janitor folders, a workspace's own Ori
folder and folders Ori only scanned never count.

The chooser lists **Time Machine (built into this Mac)** first on a Mac, then
**A restic repository on an external drive** with what you'll need: an external
drive with free space, restic 0.19.1 installed by you, an encrypted repository
you prepare, a Vault secret for its password, and about 30 minutes. Opening it
reads nothing and lists no drives; nothing is created until you choose. Either
choice opens the usual Backup & Recovery create flow (Time Machine arrives
preselected in Settings). The assistant's readiness card opens the same chooser.

Each folder-backed workspace also shows a read-only **Protection** line under
its Project Folder: "Protected by Time Machine in Backup & Recovery, last
completed backup 14 h ago", "Not in any backup Ori observes", or "Protection
unknown: backup evidence is unavailable". Its entry agent can state the same
stored fact ("Protection is set up in the Backup & Recovery workspace.") but
gains no tool for backups. A decline never hides this line. Covered means a
configured source contains the folder and a completed backup was observed; it
is computed from stored evidence only and never checks a drive.

## Set up Time Machine in Ori

1. Turn on Time Machine with a backup disk in **System Settings → General →
   Time Machine** yourself. Ori does not choose, mount or format a disk.
2. To let Ori see completed backups, turn on **Full Disk Access** for the app
   that runs Ori in **System Settings → Privacy & Security → Full Disk Access**.
3. In the Backup & Recovery console, **Settings → Time Machine**, choose the
   folders Ori should check, review them, and approve **Check Time Machine
   once**. There is no Vault secret or repository to select.
4. The Overview then shows the destination, the last completed backup and, per
   folder, whether Time Machine includes or excludes it. If the read found no
   destination it says **Time Machine is not set up on this Mac**, links to the
   Time Machine settings, and offers the restic path instead.
5. Recovery links Apple's guide
   [Restore items backed up with Time Machine](https://support.apple.com/guide/mac-help/restore-items-backed-up-with-time-machine-mh11422/mac);
   your dated user report works as for restic.

## Set up restic outside Ori

1. **Check your drive.** Connect a suitable existing drive using macOS. Account
   for its existing contents and confirm its identity, actual mounted root and
   free space. Do not erase/reformat it just to satisfy this guide. Ori does not
   mount, unlock, format or eject drives.
2. **Check space outside Ori.** Use Finder Get Info or your existing tools to
   estimate the selected data. Allow multiple versions and future growth. Ori
   does not inspect source contents or predict whether the next backup will fit.
3. **Install the supported application yourself**, following the version-pinned
   official documentation below. An unsupported or unavailable prerequisite is a
   limitation, not a reason to disable containment or grant broad root access.
4. **Prepare an encrypted, versioned repository outside Ori.** Review the exact
   destination first. Ori does not initialize repositories. Keep password/key
   recovery information securely **separate from the drive**: losing every usable
   password/key can make encrypted backups unrecoverable. Never put secrets or
   executable recovery commands in chat or recovery notes.
5. **Choose the intended folders and exclusions, then run the first backup in
   the external application.** Inspect its own result and warnings. A snapshot
   does not prove the latest attempt fully succeeded, every current file is
   included, or recovery works.
6. **Choose the backup schedule and retention outside Ori.** Ori creates neither
   backup jobs nor OS daemons. Account for sleep, disconnection and full storage.
   Pausing/closing Ori stops Ori's checks, **not** the external backup schedule.

## Connect restic metadata in Ori

The owning workspace console has **Overview, History, Recovery and Settings**;
rich connection controls live in Settings, not the generic setup wizard.

1. Choose the source folders, actual external volume root and existing repository
   through the user controls. A selection is not permission and grants no agent
   filesystem tools. A same-named folder or replacement volume is not accepted as
   the saved destination merely because its spelling matches.
2. Explicitly select one existing workspace-scoped Vault secret reference. Unlock
   its Vault outside this console if needed. Ori does not discover credentials,
   ask for plaintext passwords in an API/prompt/note, or unlock a Vault for you.
3. Review the exact selection and explicitly approve **one metadata read**.
   Selection/review alone creates no connection receipt. A failed candidate stays
   inactive; retrying the same completed request does not run another check.
4. After a successful read, an empty repository says **No completed backup
   observed**. Intended scope remains distinct from provider-declared historical
   paths. Latest-attempt success, exhaustive coverage and recoverability remain
   unavailable rather than guessed.
5. Manual-only use remains valid. Installation/connect does not enable periodic
   checks. See the separate policy review below. Disconnection is not automatically
   a failed backup; unknown capacity remains unknown.

Opening setup, pressing Done, viewing status/history or asking the advisor a
question does not start a provider read, decrypt a backup password or enable
monitoring. A missing tool/permission stays unavailable; older successful evidence
retains its original timestamp through later failures.

## Optional periodic metadata checks

In Settings, **Ori metadata checks** proposes a cadence, expected completed-snapshot
age, observation freshness, disconnected grace and low-space warning threshold.
Review the exact saved connection and policy, then make the separate initially
unchecked periodic-consent choice. Defaults and review alone enable nothing.
Cadences are 15 minutes, 1 hour, 6 hours or 24 hours; freshness cannot be shorter
than cadence. A low-space warning is point-in-time evidence, not a fit prediction.

**Pause periodic checks** cancels/fences the old periodic read and drains owned
work before completing. An ambiguous drain remains fenced and retryable. This does
not revoke independent manual one-read access or stop external backups. Resume
requires a fresh policy review and explicit consent. **Keep manual-only checks**
clears periodic consent. Changed scope/identity/credentials require local review;
copied/imported settings cannot start checks. Normal restart resumes only still-valid
local consent. There is no OS daemon and no promise of checks while Ori/the Mac is off.

Freshness, observed-completion recency, last-check result and destination presence
are independent. Stale/overdue status is recomputed from stored time without a new
read. Manual-only freshness uses 24 hours; a periodic/paused policy uses its reviewed
freshness window. Future timestamps are quarantined, not credited as protection.
History retains at most 100 metadata attempts, with 25 per page; these are not backup
run outcomes. Sleep/restart coalesces missed checks rather than replaying a backlog.

## Recovery and removal

Use vetted guidance in **Recovery** and the external application to restore a
sample to a **separate location**. Do not overwrite your working source as a test.
Ori does not restore or inspect recovered file contents. Recovery now offers
an explicit dated **User reports passed/failed** form, never “Ori verified.”
Choose a known snapshot reference or leave its association unknown. Notes are
predefined choices—not free text—to keep passwords, recovery keys, commands and
file contents out of the ledger. Confirm that you are recording your own result.
Edits retain the earlier version, and reconfiguration makes old reports historical.
The newest 20 versions are retained. A passed sample does not prove every file
is recoverable.

Revoke/remove stops or fences Ori's access and drains owned checks. Removing Ori
configuration clears selected paths/credential references while retaining bounded,
generation-labelled local history. It never deletes backups/source files or
changes the external application's schedule. Copied/imported state does not carry
local access consent.

**External-drive-only backup is not off-site protection.** Plan a separate
strategy outside this v1 integration. Ori does not repair/prune repositories,
perform integrity scans, rotate keys, purchase software or execute backup/restore
operations.

## Today, Daily Brief and the advisor

Today and Daily Brief use your **existing saved selected/all workspace scope**.
They read stored evidence only: opening, repeating or generating a summary never
checks a drive or creates a task. Today shows at most five primary concerns,
one per configuration, linking to the owning console. Resolved concerns leave
the projection. Paused Ori checks do not become failed backups; an externally
overdue completion or your failed sample-test report can still need attention.
Unreadable or omitted sources remain explicit gaps, not a healthy all-clear.

Daily Brief includes at most 20 backup sources with observation dates, evidence
age, completion recency and honestly labelled user reports. Backup information
uses compiled, fact-only wording: when it is present or unavailable, the brief
is rendered without an LLM. Other briefs retain their optional model path.
A saved brief describes its generation time, not a new metadata check.

The Personal Assistant readiness card only navigates to the Protect your folders
chooser or an in-scope owning workspace. It cannot install, connect, enable monitoring or delegate.
The Backup Advisor shares the same health/report projection in its exact
workspace. Without an isolated model it still explains compiled stored facts;
with one, the model may select an explanation topic only. Paths, passwords,
raw provider labels and report notes never enter these summary payloads.

## Official version-pinned references

- [Installation](https://github.com/restic/restic/blob/v0.19.1/doc/020_installation.rst)
- [Repository preparation](https://github.com/restic/restic/blob/v0.19.1/doc/030_preparing_a_new_repo.rst)
- [Backup and exclusions](https://github.com/restic/restic/blob/v0.19.1/doc/040_backup.rst)
- [Recovery](https://github.com/restic/restic/blob/v0.19.1/doc/050_restore.rst)

The engineering/privacy boundary is in
[the integration contract](architecture/backup-and-recovery-contract.md).
No page or link in this guide executes an installation or recovery action.

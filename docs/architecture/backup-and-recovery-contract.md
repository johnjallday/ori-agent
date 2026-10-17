# Backup & Recovery — integration contract v1

**Status: contract frozen; runtime NOT activated or ready for delivery.** The
selected metadata interface has software and designated physical-USB probe
evidence. Pure identity/metadata tests, native descriptor/broker-transport tests,
bounded child-process tests and scoped Vault resolver tests pin its foundations.
The compiled read adapter now has end-to-end fixture evidence. This still does
not establish domain admission, local activation, monitoring or finished UI. The implementation/acceptance
gates at the end remain mandatory.

## Authority and evidence

The approved v1 is one ordinary workspace, one Backup Advisor, selected folders
on one Mac, and one external-drive destination. The external backup application
owns execution, scheduling, encryption, retention and restores. Ori observes
explicitly approved metadata and stores its own observations/user reports. It
must not install tools, create a backup schedule, execute a backup/restore,
mount/unlock/format a drive, repair/prune a repository, or inspect file contents.
No File Janitor grants, folder creation or filing responsibilities are inherited.

Provider prerequisites are distinct from Ori readiness. Choosing a source,
creating a workspace, granting read access or completing the wizard is not a
backup. An external-only copy is not off-site protection. A same-physical-device
copy must never be accepted as protection against device failure.

## Selected provider and provenance

- Official **restic 0.19.1**, macOS arm64, repository **format 2** was tested.
  Other binary versions/platforms and format 1 are not validated support.
- Canonical source: <https://github.com/restic/restic>, release tag `v0.19.1`,
  commit `6aa3a516ce654808a1f28f9fa21e9b7c8e6e90bf`.
- Archive SHA-256:
  `7be0a144ccc377880f294204aa271d76e4b79554b42a751151d425ce6ebac143`.
- Executable SHA-256:
  `06582569ff2f10e1935a6f12187c76db02f0bae99e6e54098f2cdc374766d768`.
- Official `SHA256SUMS` signature verified against the documented public-key
  fingerprint `CF8F18F2844575973F79D4E191A6868BD3F7A907`, using Ori's existing
  pinned `golang.org/x/crypto/openpgp` dependency in a disposable research helper.
  No GPG installation/keyring change or application dependency was added.
- Host for the software probe: macOS 26.3.1 (a), Darwin arm64. The binary was
  staged only in ignored worktree-local test state after explicit approval.
  This is not an installed product dependency or a release-floor update.

Restic was compared with CCC 7.2.2 and Time Machine before selection. Its
structured metadata and inspectable implementation favor this narrow adapter;
its CLI setup/recovery and absence of a built-in scheduler are real usability
limitations. Users perform those operations outside Ori. No companion source
change, published connector, plugin, SDK import or reviewed integration floor is
required for the tested metadata interface.

Official references:

- [Versioned repository setup](https://github.com/restic/restic/blob/v0.19.1/doc/030_preparing_a_new_repo.rst)
- [JSON and exit-code contract](https://github.com/restic/restic/blob/v0.19.1/doc/075_scripting.rst)
- [Selected sources and excludes](https://github.com/restic/restic/blob/v0.19.1/doc/040_backup.rst)
- [Independent restore](https://github.com/restic/restic/blob/v0.19.1/doc/050_restore.rst)
- [Signed release verification](https://github.com/restic/restic/blob/v0.19.1/doc/020_installation.rst)

## Proven metadata boundary

The candidate observer has only two fixed provider operations: `cat config` and
`snapshots`, with explicit local repository selection, JSON output, **no lock**
and **no cache**. These are compiled operations, not stored commands or an
arbitrary `cat` interface. Other `cat` variants expose keys/content and are not
allowed. No CLI command, argument, environment override or executable path may
come from model text, provider labels, template/workspace data or generic wizard
actions.

The authorized test harness initialized repositories and created backups solely
to prepare invented fixtures. Its write authority is not an application feature.
The observer was run separately with macOS sandbox rules denying all writes and
network access; it could read config/key/snapshot metadata but not source files
or repository data/index trees. Negative controls verified that source reads,
repository writes and loopback networking were denied. The loader additionally
needed read access to the literal root directory, not its descendants.

Apple marks `sandbox-exec` deprecated. V1 deliberately requires this measured
containment on the tested macOS/architecture only, with the fixed launcher and
negative controls described below. Other environments remain unavailable; there
is no unsandboxed fallback. The compiled read composition now has bounded fixture controls and a designated
external-fixture acceptance read. Domain consent/lifecycle integration remains a
separate activation gate.

The test password was generated in memory, delivered over a pipe, and never
written to evidence or passed as an argument/environment variable. Production
uses the exact scoped Vault reference/resolver described below; the experiment
itself did not create Vault permissions or inspect real secrets.

### What the observations mean

| Evidence | Meaning | Does NOT prove |
| --- | --- | --- |
| Config ID + format | The readable repository identifies itself with that ID/version | Physical volume identity, source independence, or clone detection |
| Published snapshot ID | A snapshot record was published | A fully successful backup or the latest attempted run's result |
| Snapshot `time` | Backup start label, which an external operator can override | Completion time or reliable freshness by itself |
| Optional `summary.backup_end` | Provider-recorded completion timestamp | No skipped/unreadable files, or verified recovery |
| `paths` / `excludes` | Provider-declared scope for that historical snapshot | Current source intent, current schedule configuration, every file included, or all dynamic exclusions |
| File/byte counts | Provider-reported counts when present; known zero differs from absent | Current protected-data size or destination free space |
| No successful read | Evidence currently unavailable | No backup exists or a backup failed |
| User restore report | User reports passed/failed for a dated configuration/snapshot | Ori-verified recovery or recoverability of all files |

**Observed failure modes matter:**

1. Corrupt snapshot enumeration returned **exit 0**, stderr, and a valid JSON
   array containing only the readable snapshot. Any stderr invalidates a fresh
   successful observation, even when exit is zero. Do not accept partial arrays.
2. An external backup that could not read one source file returned **exit 3** yet
   published a snapshot containing `backup_end` and counts. Snapshot metadata has
   no trustworthy latest-attempt-success field. Report that outcome unavailable.
3. A concurrent external backup held its own repository lock while the observer
   read the previously published snapshot without a new lock. The new snapshot
   appeared after the external writer completed. This is not an atomic view;
   an external writer's changes cannot be attributed to the read probe.
4. Omitting no-cache attempted a cache write and failed under the sandbox.
   Omitting no-lock attempted a repository write, reported permission failure
   and hit the probe's deadline during retry. Defaults are not read-only.

No failed/partial check may replace previous evidence with an empty healthy
result or update its successful-observation timestamp. The private local store now enforces that
observation transition; the future service must still authorize and fence each
publication. The decoder alone provides neither guarantee.

## Designated physical-volume probe (not product acceptance)

After the user named a USB volume and approved creating `Ori Backup Test`, the
probe queried only that mount and the invented worktree source's backing storage.
It did not enumerate other volumes or inspect existing files/backups. The new
folder was created exclusively; retries require this probe's own random marker
and volume-bound receipt. No raw UUIDs, device nodes, provider output or password
are included in the sanitized report.

Connected checks established:

- APFS on a physical external USB device; source fixtures reside on a different
  internal APFS physical store/whole disk. `diskutil info -plist` on just the
  selected volume supplies `APFSPhysicalStores`; querying those exact stores
  supplies partition UUIDs and current parent whole-disk references. Volume and
  store identities, not the display name, are compared before/after reads.
- A fresh format-2 repository was empty, then exposed one published snapshot of
  one invented included file. A different repository on the same external volume
  had a different config ID. Bad credentials remained unavailable.
- Observer sandbox controls denied source-content reads and repository writes;
  the fixture-writer sandbox also denied a write outside its exact repository.
  Repository-path symlinks and a config symlink escape were refused. An ordinary
  directory was rejected as a mount. These are real controls, **not** a claim of
  a second physical USB same-name swap or a physical leftover-mount experiment.
- Source/repository content hashes, inode, mode, size, mtime and ctime remained
  unchanged during observer-only operations (atime excluded). Existing user
  files were deliberately not read/hashed; their byte-for-byte equality is not
  claimed. All external fixture writes were confined to the approved new folder.

Platform findings that constrain the future verifier:

- `diskutil`'s generic `VolumeSize`/`FreeSpace` were zero on this APFS volume,
  while native filesystem capacity and APFS container fields reported usable
  space. Zero in those generic fields is not evidence of a full/zero-size disk.
  Capacity remains point-in-time shared-filesystem capacity, not protected-data
  size or guaranteed space for the next backup.
- This volume has ownership enforcement disabled. `chmod`-based permission tests
  cannot establish reader isolation there; the kernel sandbox remains necessary.
  No ownership, permissions setting, formatting or mount option was changed.
- The internal Apple Fabric whole disk reported `VirtualOrPhysical=Unknown`,
  despite the explicit internal APFS store mapping. A generic requirement that
  every whole disk report `Physical` would falsely reject this source. The
  destination itself reported `Physical`; ambiguous source mappings must remain
  unavailable rather than inferred independent.
- A scoped whole-disk metadata query exceeded a ten-second exploratory deadline
  and completed in about eleven seconds under a thirty-second bound. Fail closed
  on timeout; do not assume disk queries are instantaneous. The probe uses `df`
  only to resolve the invented source to a device; production should use native
  `statfs`, with bounded metadata queries and cancellation.

The optional test-only reconnect phase holds its invented password in memory and
waits at most ten minutes for user-operated eject/reconnect. It never unmounts,
mounts or unlocks anything. No open drive files or provider processes remain
while it waits. The user-operated phase **passed**: absence was observed without
starting a provider; reconnect matched the volume/store identities, repository
config ID and complete snapshot metadata. No mount was initiated by the probe.
The bounded process exited and discarded its invented password. Neither this
probe nor its polling is registered in the application. This is not task 5.3's
full live product/restore acceptance.

## Decoder contract implemented now

`internal/backuprecovery/restic_metadata.go` is pure parsing: no filesystem,
subprocess, network, vault, workspace or model access. Its private output types
are not HTTP/assistant summaries and cannot accidentally JSON-serialize private
paths/identifiers. Provider hostnames, usernames, tags and raw diagnostics are
not retained in the projection. Unknown additive fields are scanned but ignored;
field lookup is exact-case and never aliases an attacker-controlled key.

Limits currently pinned by tests:

| Input | Limit |
| --- | ---: |
| Config JSON | 16 KiB |
| Snapshot JSON | 1 MiB |
| Snapshots per complete response | 256 |
| Declared sources per snapshot | 16 |
| Declared excludes per snapshot | 64 |
| Source path | 4096 UTF-8 bytes |
| Exclude pattern | 1024 UTF-8 bytes |
| JSON nesting | 8 recursive levels |
| JSON value/key tokens | 32768 |

The observer experiment separately capped each output stream at 1 MiB and each
operation at 20 seconds, with termination/reaping on overflow/deadline. Those
process guarantees are **not** implemented by this decoder. `--latest` is not a
resource bound: upstream enumerates before applying its grouped latest filter.
Production collection/runtime limits, bounded history and retry policy are
specified below; the store/scheduler must enforce them before activation.

Decoding requires exit zero, no stderr, valid UTF-8/JSON, a single document, no
duplicate keys (including ignored objects), full lowercase 64-hex identities,
valid required timestamps, absolute normalized non-root source paths, and
nonnegative integer counts. Missing optional counts/completion stay unknown.
Lone escaped UTF-16 surrogates and replacement characters are conservatively
rejected rather than changing a path silently. Control characters, relative or
traversing source paths, duplicate IDs/sources, malformed rows and exceeded
limits invalidate the whole result. Errors are fixed non-identifying categories.

An empty complete array is “No completed snapshot observed.” Unsupported format,
permission errors, wrong credentials, invalid metadata, overflow and cancellation
must never be presented as that state. Syntax checks grant no path access and
cannot replace physical identity verification. Future/past timestamp health is a
separate evaluator; parsing does not declare a snapshot fresh/healthy.

## Host contract

These decisions specify the implementation and required tests. The private
persistence schema/store and inert template are now implemented; no provider
runtime, domain API or monitoring consent is registered yet. Contract freeze permits the ordered
implementation tasks, not a readiness claim.

### Stable names and setup semantics

| Surface | Chosen identifier |
| --- | --- |
| Built-in template / manifest directory | `backup-and-recovery`, builtin version 1 |
| Capability / definition version | `backup-recovery`, version 1 |
| Provider | `restic`, exact 0.19.1 darwin/arm64 digest above |
| Setup adapter | `backup_recovery` |
| API prefix | `/api/workspaces/{workspaceID}/backup-recovery` |
| Console panel | `backup-recovery` |
| Tabs | `overview` (default), `history`, `recovery`, `settings` |
| Monitor registration | `backup-recovery-monitor:<workspaceID>` |
| Advisor role | `backup-advisor`; display name `Backup Advisor` |

One capability install/configuration per ordinary workspace. The reviewed normal
roster creates the advisor. There is no optional-companion provisioner, Assistant
Home, directory requirement/tool grant, watcher or automation recipe inherited
from File Janitor. A manifest has no command, executable path or dynamic module.

The compiled wizard uses existing `readiness` and `summary` kinds, with fixed
steps `backup-connection` and `backup-summary`. Rich external setup, path
selection, credential-reference selection and consent live in the domain
console. Evaluate and Confirm read stored state only; Confirm cannot activate,
refresh, select a path or accept a browser assertion of readiness. Connection
readiness requires a valid local receipt and an actual successful metadata read
for the current generation. An empty repository can be connected but is labelled
“No completed snapshot observed”; wizard completion never means “protected.”

Add only neutral capability states `ready` and `monitoring`. Preserve the meaning
and priority of existing File Janitor states; do not emit `watching` or
`review_ready` for backups. The capability card is a projection of the domain's
separate states, not the owning health evaluator. Unwired/unsupported is
`unavailable`; missing local consent is `setup_needed`; actionable failures,
stale evidence or no observed backup are `needs_attention`; explicitly paused
checks are `paused`; otherwise use `monitoring` or manual-only `ready`. Neither
new state means a verified successful/recoverable backup.

### Narrow platform and storage support

Initial evidence covers macOS 26.3.1 (a), Apple Silicon, internal Apple Fabric
APFS sources and one physical USB APFS destination. Do not advertise Intel,
exFAT, HFS+, network, disk-image, virtual/pool, or other macOS/provider-version
support based on these tests. Unsupported setups get an actionable limitation,
not a suggestion to format a user's disk. There is no automatic version fallback.

The pure policy in `storage_identity.go` checks normalized non-nil
volume/partition UUIDs, explicit location/transport, mount evidence, and one to
four uniquely identified APFS physical stores. Source and destination must share
neither a volume/partition/store UUID nor a contemporaneous parent whole-disk
reference. Up to sixteen selected sources are independent from the destination;
different folders on the same internal source device are allowed. The special
internal Apple Fabric mapping does not excuse an unknown/virtual external device.

Across reconnect, persistent volume/partition/store identity must match. During
one check, mount device, root inode and live whole-disk mapping must additionally
remain stable. Store ordering is irrelevant. Current device nodes are never
persisted as durable identity. Display names are not identity. Deliberately
cloned hardware/volume/repository identifiers are not cryptographic device
attestation; this policy does not claim to authenticate malicious replacement
hardware. Ambiguous or incomplete OS mappings fail closed.

These private predicates are not permission. The partial native collector now
binds the exact directory to a no-follow search descriptor, canonical F_GETPATH,
native APFS/local statfs and a separately opened mount-root identity. `selected_metadata_darwin_arm64.go` now composes exact mount-root/repository
location, persistent selection and selected-directory identity, supported
physical independence, scoped OS mappings and fresh post-collection checks.
It deduplicates repeated source mounts without caching across the before/after
phases. This collector now runs through the private compiled broker. It is not an
application access grant: reviewed domain admission and full finished-product
acceptance remain mandatory. Nested
foreign mounts, ordinary leftover directories and changed repository-relative
locations must remain rejected. Source selection authorizes directory/storage
metadata only, never source enumeration or file contents. Repo ID alone cannot
replace this gate.

### Stronger process boundary

The follow-up software probe demonstrated that restic's two fixed reads also
work against a private **encrypted-metadata-only replica** containing `config`,
`keys` and `snapshots`: same config and snapshot facts, no data/index/locks copied.
The observer sandbox denied a read of the original repository. FD-relative
`O_NOFOLLOW` staging rejected a config symlink and a hardlinked config before
provider execution. No source content or plaintext provider output was staged.

Use that boundary for the production design rather than giving the restic child
live access to a replaceable `/Volumes/...` path:

1. Admission checks the live owner, capability and generation, plus either the
   current local receipt or an unexpired, single-use reviewed-connect permit.
   The latter authorizes only the first validation read; it cannot schedule work
   or create a durable receipt until validation succeeds.
2. A separately cancellable, bounded compiled broker anchors directories with
   no-follow descriptors and validates native mount/storage identity. It copies
   only bounded encrypted config/key/snapshot metadata to private Ori-owned
   scratch space. No executable operation is supplied by a manifest/model.
3. The broker rejects links, non-regular metadata, cross-device descendants,
   invalid hex filenames, changed files/entry sets, and changed root/storage
   identity. In-flight inconsistency yields unavailable, never a partial read.
   This is a bounded historical metadata view, not a transactional backup engine.
4. The pinned provider child sees only that private metadata replica, its
   password pipe, and the measured system-loader dependencies. All writes,
   network access, original repository and source-content reads are denied.
   It runs only fixed no-lock/no-cache `cat config` and `snapshots` operations.
5. Recheck authority/generation and destination/source identity before publishing
   the observation. Cleanup only the exact owned scratch directory, never a
   source/destination directory. No unsandboxed or direct-repository fallback.

`metadata_anchor_unix.go` implements no-follow ancestor/descriptor reads of only
fixed metadata namespaces, rejecting changed roots, links, special files, foreign
devices and oversized files/entry enumeration. `metadata_bundle.go` defines the
versioned, bounded encrypted-only broker pipe format with exact termination and
no partial-frame acceptance. `read_process.go` owns process-group termination,
pipe limits, deadlines and reaping; tests also stop a pipe-holding descendant
after its parent exits. A test child composes native collection and this pipe.
The private adapter now composes these primitives through fixed `broker-v1` and
`reader-v1` helper modes. Server and embedded-menubar entrypoints intercept those
modes before dotenv/settings/stores/ports/native UI initialization. Each pipeline
owns its process group, pipes and scratch lifetime; overflow, cancellation or any
stderr discards the whole attempted observation before releasing access. No
server goroutine substitutes for the cancellable broker.

#### Compiled read adapter (task 2.3)

The bounded XML plist decoder rejects duplicate/ambiguous keys, unsafe device
references, external entities, truncated/trailing documents and excessive
bytes/tokens/depth/entries. Labels are discarded. A pure scoped query planner
visits only the native-observed volume and its explicit APFS stores/parents;
there is no device inventory or guessed mapping. The private fixed runner now
selects only `/usr/sbin/diskutil info -plist /dev/<validated device>`, with clean
environment and bounded streams/deadline. Fixed OS entrypoints (diskutil,
sandbox-exec and the startup-only product-version reader sw_vers) must be
root-owned, no-follow descriptor/path bound and on local read-only APFS; no
PATH/caller override is accepted. Exact product build `25D771280a` is checked via
fixed `sw_vers -buildVersion`; `kern.osversion` is only the base kernel build
`25D2128`, not the installed security-response build.

The full private adapter passed a designated USB-fixture read through native
selection, encrypted collection, provider reads and final native/config checks.
A deliberately wrong persisted volume ID was rejected before the credential
callback. These are actual compiled-adapter controls, not a second physical
same-name swap, UI consent or finished-product acceptance.

The measured diskutil result omitted the optional `Mounted` key. Absence remains
unreported, never false or an inferred permission. Native descriptor/statfs/root
evidence and the exact diskutil MountPoint establish the current mount; an
explicit contradictory `Mounted=false` still invalidates it. Missing native
mount evidence cannot be repaired by that optional-field handling.

`native_directory_darwin_arm64.go` uses no-follow **O_SEARCH** traversal, native
F_GETPATH/statfs and an independently opened mount root; rechecks bind the held
folder, selected name and current mount facts. It inspects metadata only. The
invented-folder sandbox test proves successful native collection while directory
listing, openat source-file reads and source writes are denied. An earlier
O_EVTONLY experiment allowed listing, then failed to open under the data-denying
sandbox; it is not an isolation mechanism and is no longer used. Native capacity
is overflow-checked, point-in-time filesystem capacity, with unknown distinct
from known zero. These functions must still run in a bounded compiled helper,
not a server goroutine.

Executable verification retains a no-follow descriptor, hashes the complete
fixed release, checks ownership/permissions/size/link count and rejects same-name
replacement even with identical bytes. This is not containment. Executing the
binary through `/dev/fd` failed in a separate software probe on the tested Mac;
there is no descriptor-exec fallback. Instead, the kernel's read-only csops
STATUS/CDHASH operations successfully identified the owned sandboxed restic
process. Its pinned CodeDirectory hash is
`6f23d5d672ad8600f5867ae25464f992835d35db`, supplementing (not replacing) the full
file digest. The launcher can initially have a different image: production must
wait for the exact pinned valid image **before releasing any password**, while
retaining process ownership without reaping/PID reuse. The signing check alone attests only CodeDirectory identity/status.
`mapped_executable_darwin_arm64.go` now additionally queries bounded native
region/vnode metadata for the owned unreaped child, requiring its first executable
mapping to match the retained descriptor's device/inode/size/mode/timestamps.
No process memory contents are read. The complete-file digest is checked again
before releasing password bytes, followed by another signing-identity check.
An unavailable ABI/query/mapping fails closed. The signing boolean alone still
cannot authorize a transfer; version text is not a substitute.

The Go provider phase now stages only the encrypted bundle in exclusive private
scratch, starts the pinned child with an empty password pipe, performs that
handshake and runs only no-lock/no-cache config and snapshot reads. Any stderr,
nonzero exit, overflow, cancellation or changed replica/executable invalidates
the entire result. Cleanup unlinks only the owned manifest through retained
descriptors; it does not recursively delete a selected path. Real software-only
fixtures exercised empty, completed-snapshot and wrong-password outcomes through
this Go path, including original-fixture preservation and replica removal.
The host also pins its own application helper's signing identity, mapped vnode
and complete descriptor digest before sending a request or invoking the scoped
password callback. Helpers do not resolve Vault records themselves. The measured
debug server image is about 148 MB; its separate bound is 256 MiB, while restic
retains its fixed release digest and 128 MiB bound. The private adapter creates
only a fresh job directory inside the composition-owned app data root and
cleans it through a retained root handle after child drain. These entrypoints do
not establish domain admission, monitoring consent or full product acceptance.

Private sandbox text is sent through an owned pipe, never through arguments
containing selected folders. The directory-only, encrypted-metadata broker and scoped-OS-query policies have
independent compiled fixture controls and now compose into the read adapter.
They do not replace the owning service's live permission/lifecycle gates. Search-only traversal now
also avoids ancestor/root listing grants in the encrypted metadata anchor and
executable ancestry checks. Broker controls allow only config/keys/snapshots and
deny source content, repository data/index/locks, root enumeration and writes.

macOS rejected applying a second sandbox to a child which inherited one. The
controller must therefore launch broker and provider as separately sandboxed
children, not attempt to tighten an inherited broker sandbox around restic.
The controller only handles bounded IPC, executable checks and its own private
scratch; original repository reads belong to the broker. A sandbox application
failure remains unavailable: there is no direct/unsandboxed provider fallback.

Nested OS queries must retain the outer helper's process group. The group-leading
helper can cancel/reap its direct child; outer cancellation kills the whole
pipeline even when the helper blocks. A guarded private member mode now enforces
that inheritance; an invented heartbeat regression verifies a blocked helper's
nested child stops at the outer deadline. This is not complete revoke/reset/
shutdown integration or a production admission permit. No capability runtime,
HTTP authority, stored setup reader or monitoring is activated by this adapter.

Trust only the exact signed-release digest, not `restic version` text. Select an
operator-configured absolute executable at server composition, not PATH search,
workspace/template data or an HTTP/model argument. Reject a missing, non-regular,
symlinked, group/world-writable or changed executable; validate its descriptor,
digest and path identity before/after launch. No automatic install/copy/download.
The Apple containment launcher is fixed `/usr/bin/sandbox-exec`; availability and
negative controls are mandatory. Its deprecation is an explicit deployment
limitation: unsupported OS/launcher behavior disables reads rather than silently
weakening isolation. The production self-test must be confined to Ori-owned
invented sentinels, not real user files or network requests.

The child environment is an allowlist with private HOME/TMPDIR/cache paths;
inherit no `RESTIC_*`, proxy, dynamic-loader, password-command, repository or
cloud settings. Credentials go through a closed-after-write pipe, never argv,
environment, disk, errors or logs. Disable stdin interaction after that transfer.

### Credentials, ownership and persistence

The backup API accepts only an explicit `(vault_id, record_id, record_revision)`
reference to a user-selected **workspace-scoped** Vault `secret` record with a
single string `value`. Revision is an opaque SHA-256 fingerprint of the encrypted
payload/nonce and exact record scope, not `updated_at` or a password hash; two
writes at the same timestamp must still invalidate it. Existing Vault controls own secret entry/unlock; the
backup console, wizard, assistant and notes never accept a password. Preserve
password whitespace; bound to 1–4096 UTF-8 bytes and reject NUL/CR/LF for the
single-line pipe transport. No key discovery, record enumeration across vaults,
password-command, generic secret export or new agent/plugin grant is introduced.

Existing `vault.AccessContext` has no domain-service actor; an empty user context
bypasses grants, and `GetRecord` decrypts before a caller could check its workspace.
Do **not** directly expose that API through the backup service or impersonate an
agent/plugin. `vault.DescribeBackupSecret` queries only the chosen vault/record
and returns its revision without decryption. `vault.UseBackupSecret` checks exact
workspace/type/revision and the server-supplied live authorization callback
**before** decryption, uses only an already unlocked selected vault, and rechecks
ciphertext/authorization/lock before a short synchronous pipe transfer. It never
searches other vaults or returns a serializable secret. Buffers are cleared on
exit, without claiming Go-heap zeroization. Callback/storage errors are redacted.
The domain service must hold the reviewed operation's admission and cancellation
through the child/result lifecycle; the resolver is not a grant. Locked/missing/
changed records invalidate availability; replacing the reference needs review. The advisor receives no vault
resolver or vault tool even if other workspace roles have unrelated grants.

Owning records are installation-local, keyed by exact owner + stable workspace
ID + capability install identity; never by workspace/folder display names. The
private store uses transactionally updated DB state, with encrypted private fields
for paths, volume/repository IDs and secret references. Reuse the existing secure
key-management primitive with separate authenticated domain data, not a plaintext
fallback. Current `workspace.LocalConfigStore` admits only `agent`/`bindings`
kinds: it is a security pattern, not an existing backup persistence adapter.

Separate the following concepts in storage:

- Configuration generation (fresh random ID on scope/identity/reference change),
  configuration revision, and observation sequence. Polling does not invalidate
  an unrelated user settings revision; stale config mutations still conflict.
- Intended sources versus provider-declared historical scope; connection versus
  source/destination availability; last attempted check versus last successful
  read; published snapshot versus optional completion; user restore reports.
- Explicit local activation receipt and optional monitoring consent. Neither is
  inferred from a portable install record, `enabled` flag or imported history.

The receipt binds owner/workspace/install/config generation, reviewed input
fingerprint, local continuity attachment/import operation, installation secret
and canonical data-root identity. Keep it outside portable workspace/checkpoint
content. A copied data root cannot reuse the original root's activation; missing
keys/receipts or attachment/owner mismatch are inert, not repaired on read.
Normal restart may resume only this exact still-valid local consent. Do not
claim protection against an administrator copying the entire machine/keychain
or deliberately rolling back all trusted local authority.

The owning schema is `backup_recovery_local`, one encrypted state row per exact
workspace/owner, `backup_recovery_receipts`, a distinct local activation row
bound to that state, and `backup_recovery_authority`, a path/secret/grant-free
workspace epoch counter. The counter also fences reviews made before any encrypted
configuration exists; lifecycle triggers must not depend on a saved state row. Implementation allocated migration 81 after inspecting the
current schema. Use the existing `workspace_config_dek` secure key with AES-GCM randomized nonces and domain-
separated authenticated data `ori.backup-recovery.local.v1` including owner,
workspace, configuration generation and schema version. The receipt binds a
random local grant nonce, config fingerprint, data-root canonical path/device/
inode and continuity attachment/import identity. It is committed/revoked
transactionally with the owning state, never reconstructed from an enabled flag.

The existing capability install has no UUID: bind its canonical ID, version and
nonzero `installed_at` fingerprint together with the random local grant nonce.
The timestamp alone is not permission. An absent/tombstoned/replaced install,
changed owner or new import operation cannot reuse a receipt. Capability removal
must delete/fence the old receipt before a same-ID reinstall can activate.

Initial connection consumes a one-read permit without durable activation. A
confirmed replacement fences the old generation/monitor first; failures leave
the new candidate inactive and prior observations historical. Cancelling before
confirmation leaves the original configuration intact. Review tokens stay
process-local (restart expires them); operation outcomes and revisions are
persisted so lost-response retries do not launch duplicate reads.

Corrupt/future/foreign state remains intact and unavailable. No silent reset.
Private directories/files use 0700/0600. Register every new DB/local domain and
its credential/receipt lifecycle with the reset inventory before enabling it.
No backup state or secret is exported through canonical workspace continuity.

#### Persistence foundations implemented (task 2.1)

`local_state.go` bounds the encrypted schema; `local_store.go` performs exact
canonical-owner/install/attachment checks, transactional revisions and encrypted
state/receipt publication. `local_root*.go` binds the composition-owned app data
root's canonical location and native identity; it never inspects backup sources.
Private persistence DTOs are not API/model DTOs. Reads create neither keys nor
receipts. Missing/corrupt/future/foreign state is left intact and unavailable.
Activation transactions require a successful-read observation for the current
generation and cannot enable monitoring; consent remains a separate user change.
The real provider/service must supply that evidence; the store cannot attest it.
These private entrypoints are not substitutes for 2.4's reviewed one-read permits.

A separate monotonic **authority epoch** fences workspace ownership/status,
capability replacement, attachment/import changes and native provisioning. The
migration's triggers discard access receipts and advance that epoch without
rewriting encrypted history. Reviews/publication bind the epoch as well as live
identity: an initial read with no receipt cannot survive an archive→restore or
uninstall→reinstall round trip, even when the initial review has not saved any
configuration. The epoch is recorded independently of encrypted state; advancing
it creates neither an encryption key nor access permission. All three local
tables are reset-inventoried. Missing schema fences disable store construction.
Partial legacy schemas are not silently repaired or given weaker authority.

`vault.WorkspaceConfigCipher` now centralizes the existing secure-key creation
lock for both workspace configuration and backup state. Separate package locks
could otherwise race to generate and overwrite the shared key. Backup writes
never mint a missing key over any existing encrypted owner. No new secret backend,
plaintext fallback, filesystem grant or credential tool is introduced.

Focused tests cover bounded/canonical serialization, unknown counts, failed-read
preservation, separate user revision/observation sequence, concurrent stale
writers, atomic state/receipt rollback, wrapped-error redaction, lifecycle ABA,
reset admission, copied roots/workspaces and imported enabled intent. They use
invented local state, not a production provider or finished-product acceptance.

### HTTP and lifecycle rules

All routes use the existing authenticated/local user provider and exact active
workspace ownership (foreign/archived/trash/deleted returns not found), installed
capability and continuity admission. Do not inherit File Janitor's case-folded
owner comparison or its raw-error logging. Stored GET projections never probe a
disk or decrypt a backup password. Domain endpoints are:

| Method / suffix | Authority and behavior |
| --- | --- |
| GET `/` | Redacted stored status, timestamps, current revision and limits |
| GET `/history` | Stored history, stable bounded cursor, max 25 records |
| GET `/settings` | Owner-only saved selections/reference availability; no secret |
| GET `/guide` | Compiled model-free, version-pinned guidance after exact workspace scope check |
| POST `/selection` | Explicit native folder choice; owner/workspace/role-scoped opaque token, no grant or filesystem-tool binding |
| POST `/credential` | Describe only the explicitly chosen scoped Vault record/revision; no decryption or listing |
| POST `/review` | User-selected scoped picker tokens and vault reference; no provider start |
| POST `/connect` | Exact review token/revision; explicit metadata-read consent, one guarded validation |
| POST `/refresh` | Explicit single-flight manual check, independent of periodic consent |
| POST `/monitoring/review` | Review cadence/freshness/expected-age/offline/space settings |
| POST `/monitoring` | Consume exact reviewed settings token; explicit opt-in or pause |
| POST `/revoke` | Fence/cancel/drain, discard local access receipt; retain labelled history |
| POST `/recovery-report` | Revision/idempotency-bound user report only; no restore execution |
| DELETE `/` | Fence/cancel/drain then remove Ori configuration only |

Use existing origin/host protection plus exact scheme/host/**port** same-origin
validation on mutations; the current connections guard ignores ports and is not
alone sufficient. Require JSON and a non-simple same-origin action header;
reject cross-site/null origins and unsupported methods before body parsing.
Non-browser local clients must satisfy the same host/user/action contract, not
bypass it through absent Origin. Require the actual peer to be loopback; forwarded
peer/host/proto headers cannot override that or actual TLS/Host. The compiled
action header is `X-Ori-Backup-Action: user-control`. API responses are no-store;
raw backend/provider errors and request bodies are not logged or returned.

Requests reject unknown/duplicate keys, trailing JSON, executable/path overrides
and excessive bodies. Selection tokens are owner/workspace scoped, short-lived,
opaque and bound to the exact reviewed config. No generic wizard path payload.
Mutations require an exact expected configuration revision and UUID idempotency
key, with the request digest scoped by owner/workspace/generation/action. A retry
of the same completed operation returns its outcome without running a second
read; changed payload/key reuse conflicts. Pending/expired tokens do not grant
access. Config changes, revoke, ownership/attachment change and reset invalidate
outstanding review tokens and in-flight results.

Manual refresh additionally echoes the stored sequence and server time. Before
starting it, a dedicated admission transaction reserves the next sequence without
changing settings revision, successful-read time or attempt/observation facts.
Thus an identical old request cannot become another probe after bounded outcome
eviction or interrupted execution. Old request times expire after 24 hours;
future times beyond five minutes reject. Connect instead consumes a five-minute,
single-use process-local review. At most four reviews per workspace and 128
server-wide are retained. Native choice tokens have the existing 30-minute TTL
and separate owner/workspace/source-versus-volume-versus-repository scopes;
legacy/unscoped tokens cannot be upgraded. Same-path known identities are retained
through review, not reset merely by choosing the same mount spelling again.

Revoke/removal persist a new inactive generation before cancellation, but their
operation is not marked complete until owned work drains. A four-second bounded
wait can return still-in-progress while retaining the fence and lifetime/finite
ownership; retry cannot restart a read. Clearing configuration removes selected
paths/credential reference but keeps bounded, generation-labelled local history.
Neither operation touches the external repository, source or backup schedule.

All external work enters `resetstate.WorkGate` before spawn and holds admission
through publication/cleanup. Monitoring also owns a registered lifetime permit.
Service construction without tracked production admission is unavailable. Under
one configuration lock, close admission and bump/fence generation **before**
cancelling work; drain child processes before releasing local authority. Recheck
live owner/install/attachment/generation in the final state transaction. An old
result may neither publish success nor restore a revoked setting. Removal failure
stays visibly fenced/retryable rather than releasing ownership early. Shutdown
stops scheduling, cancels/drains children and releases lifetime ownership before
stores/leases close. Reset uses the existing irreversible fence, not a new gate.

### Limits and independent status dimensions

In addition to the decoder limits, enforce these bounds:

| Resource/policy | Bound / proposed default |
| --- | --- |
| Intended sources / physical stores per volume | 16 / 4 |
| Encrypted config / key file / snapshot file | 64 KiB / 64 KiB / 1 MiB |
| Key entries / snapshot entries / total staged bytes | 16 / 256 / 16 MiB |
| Broker wire format / bytes | `ORI-BACKUP-METADATA-1`, at most 16 MiB + 32 KiB framing |
| Broker lifetime | At most 75 s, also bounded by the complete-check deadline |
| Provider operation / scoped OS query / complete check | 20 s / 30 s / 120 s |
| Provider stdout / stderr | Decoder's operation bound / 16 KiB (any stderr fails) |
| Active checks | 1 per config, 2 server-wide; no unbounded per-config queue |
| Automatic retries | At most 1 after 5 min for transient read failure; none for identity/consent errors |
| Cadence choices | 15 min, 1 h (proposed), 6 h, 24 h |
| Expected snapshot completion age | 48 h proposed; allowed 1 h–30 days |
| Observation freshness | 3 h proposed (manual-only: 24 h); allowed 15 min–7 days, not shorter than cadence |
| Disconnected grace | 24 h proposed; allowed 0–30 days |
| Low-space warning | 10% proposed; allowed 1–50%; unknown remains unknown |
| Tolerated timestamp lead | 5 min; further-future facts are quarantined, not fresh |
| Successful/failed attempt history | Newest 100 entries, deterministic sequence ordering |
| Restore reports | Newest 20; at most 512 UTF-8 bytes per private note |
| Request / settings / entire private state | 16 KiB / 128 KiB / 2 MiB |
| Review token lifetime / outstanding reviews | 5 min / 4 per workspace |
| Idempotency outcomes | 64 per workspace, expire after 24 h |

Defaults are proposals displayed before consent, not automatic activation. Use
server UTC timestamps plus monotonic in-process deadlines; no local-time/DST
schedule. Restart/sleep catch-up coalesces to one due check, never replays missed
intervals. Clock rollback/future observations become unknown/stale until coherent,
not healthy. A previous success timestamp never advances on an error. Recompute
freshness/overdue projections from stored facts at read time without disk access.

Keep these fields independently visible:

- Connection: not configured / needs local review / configured / unsupported.
- Destination: not checked / present / absent / identity mismatch / unavailable,
  always with the check time. Within disconnect grace is not backup failure.
- Monitoring: manual only / active / paused / unavailable, with consent revision.
- Observation: never observed / current / stale / last attempt failed, retaining
  both last attempt and last successful read times.
- Snapshot: none observed / published with completion / completion unavailable /
  timestamp suspect. Missing completion must not borrow the snapshot start label.
- Recency: unknown / within expected age / overdue, only when trustworthy
  completion evidence exists; still not latest-run success or full coverage.
- Coverage: intended versus historical declared scope and exclusions, optional
  counts; current exhaustive coverage and latest attempted run remain unavailable.
- Recovery: not tested / user reports passed / user reports failed, dated and
  associated with config/snapshot where known. Reconfiguration makes old reports
  historical. A user report is never relabelled “Ori verified.”

Today, Daily Brief and the advisor read only the owning service's bounded stored
projection, scoped by current owner/workspace/capability/activation visibility.
Return fixed reason codes, timestamps, counts and safe console routes; no paths,
volume/repository IDs, raw output, vault references, freeform notes or untrusted
provider labels. At most one current item per problem/config generation, max 20
backup items per projection. A source failure is a named gap, not an empty healthy
source. Asking a question cannot install, activate, schedule or refresh anything.

## Current inert template integration

The version-1 built-in definition and template now use the normal reviewed team
creator for one `backup-advisor` role, including explicit creation/assignment and
stale-plan rejection. They declare no directory requirements, tool bindings,
automation or optional companion provisioner. Installing the capability is
idempotent metadata and is unavailable in a Home, group or inactive workspace.
It does not construct a provider, activate access or schedule work. The neutral
`ready` / `monitoring` status vocabulary preserves existing status priorities.

Normal managed-workspace scaffolding still creates its own `workspace-files`
binding and self-folder reference, as for other ordinary workspaces. No selected
source/repository root is added. **This is not advisor isolation:** the compiled
role guard now refuses generic core/toolbox/native/task execution, including
alias, caller-selected-node, nil-resolver and missing/unassigned workspace
fallbacks. A shared definition genuinely attached in another role keeps that
workspace's independent policy. Prompts and template defaults are not authority.

The compiled `backup_recovery` wizard adapter accepts only the two fixed
readiness/summary steps. Confirm repeats a stored-only read; it accepts no
connection, refresh, path selection or approval option. Server registration now
uses the canonical-user stored reader. Missing service/tool/valid evidence stays
unavailable; Confirm cannot establish permission. Opening setup does not auto-run
either optional starter task.

### Server composition and lifecycle (tasks 2.8–2.9)

The host composes the service after the canonical database, workspace, Vault,
selection store and shared WorkGate exist. It registers domain HTTP, stored
wizard/advisor readers and bounded capability status/removal. Only the operator's
`ORI_BACKUP_RESTIC_EXECUTABLE` selects a binary location; the existing pinned
image/platform checks remain mandatory. Missing configuration leaves the real
stored-only console/guide available, never an alternate executable search.

Normal non-private workspace creation has no continuity attachment row, as
recognized by `workspacecontinuity.CheckExecution`. Its `legacy_native` origin
is separately bound from an explicit native attachment. This compatibility
refinement creates/adopts no attachment and grants no read: exact canonical
owner, ordinary active workspace, install, root, epoch, fresh reviewed consent
and successful activation remain required. Attachment insertion changes that
binding; delete/round trips advance the independent epoch and cannot reuse an
old review/receipt. Foreign/corrupt encrypted state remains intact/unavailable.

Generic capability catalog/removal also enforces canonical owner, actual local
peer and exact Origin, with bounded exact JSON and the non-simple control header
for backup mutations. Removal requires a generation/revision/UUID-bound domain
prepare action; a context-free runtime callback cannot initiate it. Its durable
pending barrier fences and drains first and prevents fresh admission before the
later install-record write. Identical retries drain without probing; changed
payloads or a reinstalled authority reject. An optional final removal fence runs
under the canonical workspace update lock, so a stale callback cannot drop a
new install. Expiring an outcome cannot expire the admission barrier.

Advisor conversations intercept generic execution before commands/files/skills/
utilities/planning/native dispatch. An optional verified-isolated model selects
only a fixed explanation topic. Host code alone renders re-authorized **latest**
stored facts and compiled guidance, after rechecking actor and live role. Model
prose, commands, tools and factual claims never reach display or execution.

Service close closes admission/cancels and joins all entered work before releasing
provider/lifetime ownership. An ambiguous close retains stores/lease ownership.
Server loop stop and dependency close are separate once-only phases around a
retryable finite-work join; reset never closes stores after a drain error.

## Implementation and acceptance gates

Freeze resolves design/feasibility, not all later implementation tests:

- **2.1:** encrypted persistence, exact receipt/root/attachment binding, schema
  bounds, conflicts, copied-state behavior and reset inventory.
- **2.2–2.5:** inert IDs/roster, real native OS collector and sandboxed broker/
  provider composition, trusted-executable checks, exact-origin/owner/selection/
  revision/idempotency enforcement, and stored-only wizard readiness. No provider
  or capability runtime binding may precede its required permission/execution
  tests; an inert wizard adapter with no reader remains unavailable.
- **2.5–2.9 implemented:** stored wizard/advisor bridges, model-free guide,
  console, effective role isolation, guarded server composition and manual
  revoke/removal/shutdown admission. Focused tests include the actual server's
  ordinary creator, inert install, missing tool, stored guide, versioned removal
  and retryable shutdown. They do not constitute browser/provider acceptance.
- **2.10:** affected-package/frontend validation is in progress; full delivery
  CI remains a separate gate.
- **2.11 exercised:** real no-model UI creator, stored missing-tool wizard/guide,
  native picker cancellation/choices, unavailable selected reference, cancelled
  review and an explicitly approved first product read of a newly prepared
  disposable external snapshot. This does not establish periodic monitoring,
  a sample restore, another physical same-name swap or full 5.3 acceptance.
- **3–3.5:** periodic consent/scheduling, pause, lifecycle/ownership races and
  monitor/reset integration remain separate gates. No scheduler is bound.
- **4:** stored-only scoped Today/Brief/advisor projections and user-reported
  recovery, with no provider calls or filesystem/secret authority in readers.
- **5.3:** full real-product live acceptance and a sample external restore.
  Connected identity/source independence and actual user-operated unplug/replug
  already have probe evidence, but a second physical same-name swap has **not**
  been performed. Wrong-identity and ordinary-directory fixtures are controls,
  not substitute hardware/product acceptance. Record remaining hardware cases
  separately and obtain any additional device permission when needed.
- **5.8–5.12:** full repository/browser/security gates, manual guide, final demo
  and explicit PR approval. No current result is a delivery-readiness claim.

Ordinary unit CI requires neither restic nor an attached drive. The software
probe is not a restore test or full repository validation; the physical probe is
not the finished product. Any new evidence contradicting this contract reopens
the affected design gate rather than silently weakening it.

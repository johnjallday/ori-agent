# Herdr Standalone Wake Service v1 Contract

Status: Accepted
Decision date: 2026-07-31
Planning source: `tasks/prd-herdr-standalone-overnight-wake.md`

## Purpose

This document fixes the installation, platform, identity, path, horizon, and
compatibility values that the standalone Herdr wake implementation must use.
These values are part of the privilege boundary. They are constants, not
operator-configurable settings and not fields accepted through daemon IPC.

## Fixed v1 values

| Item | V1 value |
| --- | --- |
| Installer | One explicit command-line root installer path for source and packaged distributions |
| Administrator boundary | `/usr/bin/sudo -k -- <staged-herdr-wake> install ...`; no shell |
| Minimum macOS | macOS 12 Monterey |
| Architectures | `darwin/arm64` and `darwin/amd64` |
| Maximum wake horizon | 168 hours from the daemon's current clock |
| LaunchDaemon label | `com.ori.herdr-wake` |
| `pmset` event type | `wakeorpoweron` |
| `pmset` owner | `com.ori.herdr-wake` |
| Protocol version | `1` |
| Root-state schema version | `1` |

The daemon derives the event type and owner internally. A request cannot
override either value.

## Installation mechanism

V1 uses one installer mechanism whether `herdr-wake` came from a developer
build or a packaged Ori distribution:

1. `wt herd wake install` resolves the matching `herdr-wake` artifact, copies
   it into a newly created user-private staging directory, and calculates its
   digest.
2. Before elevation, the CLI prints the fixed label, destinations, enrolled
   user ID, digest/build, capability, and uninstall command. It requires the
   PRD's explicit confirmation.
3. The CLI directly executes `/usr/bin/sudo` with `-k`, `--`, the staged
   `herdr-wake` executable, and a typed root-install verb. It does not invoke a
   shell, preserve arbitrary environment values, accept an askpass program, or
   construct a command string.
4. The elevated `herdr-wake` process verifies that it is root, validates the
   staged executable and invoking/enrolled user, and installs only the fixed
   files in this contract. It then uses fixed `/bin/launchctl` argument vectors
   to bootstrap the job in the `system` domain.
5. Installation is successful only after ownership/mode checks, launchd
   health, peer-credential authorization, version compatibility, and the
   bounded schedule/read-back/exact-cancel self-test all pass.

`--yes` may suppress Herdr's own confirmation for an explicitly requested
non-interactive install, but it does not add `sudo -n`, create a sudoers rule,
or bypass administrator authorization. A caller without an available macOS
administrator path fails closed. A system administrator may run the same
typed installer verb for a named allowed UID; this does not create a second
installation mechanism.

V1 does not use `osascript`, `AuthorizationExecuteWithPrivileges`,
`SMJobBless`, a setuid binary, a Keychain password, a stored authorization
token, or `SMAppService`. Apple's `SMAppService` LaunchDaemon registration is
macOS 13+ and expects the helper inside an application's main bundle. Herdr
must also support a source-built CLI, so adding it now would create two
security-sensitive lifecycle implementations. A future packaged-only
registration path requires a separate reviewed migration while retaining the
same daemon capability and ownership rules.

## Platform floor

The wake service supports macOS 12 Monterey and newer on Apple Silicon and
Intel. This matches the repository's Go 1.25 toolchain floor. The selected
Unix-domain peer credentials, launchd system domain, and `pmset` operations do
not require `SMAppService`.

The CLI must return one stable unsupported result before staging files,
requesting elevation, calling launchd, or inspecting `pmset` on an older macOS
release or a non-macOS host. Hardware wake support is still checked by the
installer self-test; the OS version alone never proves that a Mac can wake
reliably.

If a future Go toolchain or required system API raises the platform floor,
that is an explicit compatibility change. It must update this contract,
installer checks, help/status output, build targets, and the tested platform
matrix together.

## Wake horizon

The daemon accepts an absolute `wake_at` no later than 168 hours after its own
current clock. It rejects a timestamp exactly beyond that boundary before
state is written or `pmset` is called.

Seven days covers:

- the next wall-clock start of an Overnight Run;
- its next-morning absolute deadline and verified Claude reset;
- a practical one-time continuation scheduled across a weekend.

The user-level client may impose a shorter deadline. The daemon never expands
the caller's horizon, infers a provider reset, or treats an allowed timestamp
as authorization to sleep. Absolute protocol timestamps are UTC instants;
only the fixed `pmset` adapter converts the selected instant to macOS's local
calendar format.

## Installed layout

All destinations are fixed constants compiled into the installer and daemon.

| Purpose | Path | Required owner and mode |
| --- | --- | --- |
| Daemon executable | `/Library/PrivilegedHelperTools/com.ori.herdr-wake` | `root:wheel`, `0555` |
| LaunchDaemon plist | `/Library/LaunchDaemons/com.ori.herdr-wake.plist` | `root:wheel`, `0644` |
| Control socket | `/var/run/com.ori.herdr-wake.sock` | enrolled UID:`wheel`, `0600` |
| Private state directory | `/var/db/com.ori.herdr-wake` | `root:wheel`, `0700` |
| Installation metadata | `/var/db/com.ori.herdr-wake/install.json` | `root:wheel`, `0600` |
| Candidate/reconciliation state | `/var/db/com.ori.herdr-wake/state.json` | `root:wheel`, `0600` |
| Mutation lock | `/var/db/com.ori.herdr-wake/state.lock` | `root:wheel`, `0600` |
| Bounded audit trail | `/var/db/com.ori.herdr-wake/audit.jsonl` | `root:wheel`, `0600` |

`/var/run` is intentionally used for the ephemeral socket; the daemon
recreates it after boot. The daemon binds the fixed path as root, changes only
that socket to the enrolled UID with mode `0600`, and then authenticates every
accepted stream using kernel-reported peer credentials. Root and exactly the
enrolled UID are the only accepted peer UIDs. The UID in a request body is
never authentication.

The user-owned socket does not make privileged state user-owned. Its parent is
not writable by the enrolled user, and the daemon refuses an unexpected file
type, owner, or mode rather than unlinking an unsafe path. Private state never
contains prompts, transcripts, worktree paths, credentials, authorization
data, or environment values.

The LaunchDaemon runs as root in the `system` launchd domain. Its
`ProgramArguments` contain only the fixed executable and daemon verb. It does
not run the full Ori server or the full Herdr devflow CLI.

## Protocol and compatibility policy

Every request and response carries:

- `protocol_version`;
- a bounded request ID;
- the sender build version;
- exactly one typed operation or result.

V1 compatibility is deliberately strict:

1. The only accepted wire version is integer `1`. The client and daemon must
   match it exactly; there is no range negotiation or downgrade.
2. A version-mismatched request receives a bounded
   `incompatible_protocol` refusal containing the daemon protocol and build
   versions. It performs no state mutation and no `pmset` call.
3. Helper and daemon build versions are always reported, but different build
   versions are compatible when both implement protocol `1`. Any change that
   alters authorization, operation meaning, validation, fixed ownership,
   persisted behavior, or required safety evidence must increment the
   protocol rather than claiming v1 compatibility.
4. Reinstall idempotency compares the installed daemon artifact digest and
   fixed install metadata. A build-version string is not used as a security
   identity.
5. Root state uses schema version `1`. The daemon may migrate a specifically
   documented older schema atomically, but must refuse an unknown or newer
   schema without rewriting it.
6. Unknown operations, sources, purposes, fields, and result variants are
   refused. There is no fallback to Ori wake state, an older socket, or an
   unversioned protocol.

The health path remains safe enough to report an incompatibility, but health
does not waive exact protocol matching for any state-changing request.

## Change control

Changing any fixed label, `pmset` identity, destination, maximum horizon,
authentication rule, or compatibility rule requires:

- an update to this contract and the PRD;
- explicit install/update migration behavior;
- compatibility and preservation tests;
- a review that existing Herdr and non-Herdr wake events remain safe.

No runtime config or protocol payload may override a value in the fixed-v1
table.

## Primary references

- [Apple `SMAppService` documentation](https://developer.apple.com/documentation/servicemanagement/smappservice)
- [Go 1.25 macOS support floor](https://go.dev/doc/go1.25#darwin)
- Local macOS `launchctl(1)`, `launchd.plist(5)`, `pmset(1)`, and `sudo(8)`
  manuals used by the implementation and fixture tests

# Time Machine metadata fixtures

Captured on the validated host (macOS 26.3.1 build 25D771280a, arm64,
`tmutil version 4.0.0`) during the folder-protection spike, from read-only verbs
only. They contain no identifiers: this host had **no Time Machine destination
configured**, and the process that ran them had **no Full Disk Access**.

- `destinationinfo_none.plist` — `tmutil destinationinfo -X` with no
  destination: an empty dictionary, exit 0. The same empty dictionary, also
  exit 0, comes back when the sandbox denies the preference read, so an empty
  answer alone never means "not set up".
- `status_idle.plist` — `tmutil status -X` with `backupd` reachable: idle,
  `Percent` −1.
- `status_ipc_denied.plist` — the same verb when the sandbox denies
  `com.apple.backupd.status.xpc`: a fabricated idle answer **without** `Percent`,
  exit 0. A status without `Percent` is unavailable, never "idle".

Fixtures for a configured destination, completed backups and exclusions are
written by hand in the documented shape and say so in their names (`invented_*`).
They do not prove behavior on a Mac with Time Machine configured.

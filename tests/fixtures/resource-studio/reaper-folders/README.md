# Disposable REAPER folders for candidate acceptance

`./scripts/reaper-demo.sh test --suite studio` copies these into the sandbox
HOME before Ori starts, so the REAPER candidate's real readers inspect them
instead of anyone's REAPER setup:

- `resource/` becomes `$HOME/Library/Application Support/REAPER`
- `ori-scripts/` becomes `$HOME/Ori Scripts/reaper`

The harness adds what Git should not carry: a `reaper.ini` whose Web Remote
port nothing listens on (so the plugin list reads as REAPER closed) and a
symlinked script pointing outside the folder.

Expected inspection:

| Source | Found | Skipped |
|---|---|---|
| Project templates | 3 (`Vocals.RPP`, `Band/Vocals.RPP`, `Mix bus.RPP`) | 1 hidden |
| Track templates | 2 | none |
| REAPER Scripts | 3 (Lua, EEL, Python; `notes.txt` is passed over) | 1 symlink, 1 registered outside the folder |
| Ori script library | 1 | none |
| REAPER plugin list | 0, REAPER closed | none |

`Tools/Normalize items.lua` is registered in `reaper-kb.ini`; the other two
REAPER scripts are not. The Lua and EEL scripts and the Ori library script
carry tagged descriptions.

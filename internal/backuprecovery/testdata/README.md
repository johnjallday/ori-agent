# Invented restic metadata fixtures

These are synthetic, non-identifying examples of the documented restic 0.19.1
repository-format-2 JSON shape. They are not copied from personal backups and do
not prove hardware behavior, restore success, or full file coverage.

`completed_snapshot.json` describes a published snapshot with completion metadata.
The same shape can be emitted after `backup` exits 3 (unreadable source files).
It deliberately has **no successful-run field**. Hostnames, usernames and tags
are untrusted, unnecessary fields which the private decoder must discard.

The independent, authorized software-only probe used the official release on
invented local data. It confirmed that corrupt snapshot enumeration can return
exit 0 with stderr and a partial JSON array. Any stderr invalidates the whole
observation; an empty successful array is distinct from an unavailable read.

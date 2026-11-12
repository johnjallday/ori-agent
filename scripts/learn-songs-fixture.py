"""Writes the "learn the songs" demo songs into a demo sandbox's Music folder.

Usage: python3 -I scripts/learn-songs-fixture.py <ori-demo-sandbox> [long-takes] [mib-each]

Run it through `./scripts/smoke.sh learn-songs-fixture`, which also creates the
sandbox's Downloads folder, the six standard songs and the Beats folder, and seeds
the folder agents. It adds to <sandbox>/Music/REAPER Projects:

  Night Drive   92 BPM; tracks Kick, Bass, Synth Pad, Lead Vocal; markers Intro,
                Verse 1, Bridge, Outro and a Chorus region 0:30-0:45
  Glass Hour    128 BPM; tracks Drums, Bass, Arp; a Drop region and a Breakdown marker
  Big Session   150 tracks and 140 markers, past both caps (128 kept of each)
  Broken Take   a file that is not a project of its format (recorded "not read")
  Huge Session  a sparse file over the 32 MiB cap (never opened; no disk used)
  Long Take N   optional, in <sandbox>/Beats/REAPER Projects (so Music stays the
                clean fixture): <long-takes> valid projects of about <mib-each>
                MiB each (default 30), so reading Beats takes long enough for
                the card's per-song progress line to be seen. At most 480 MiB in
                all (15 x 30): one pass reads at most 512 MiB.
  Logic Sketch  a Logic Pro bundle: a song in a format with no offline grammar

The sandbox path must contain "/ori-demo."; nothing outside it is written.
"""
import os
import sys
import time

sandbox = sys.argv[1] if len(sys.argv) > 1 else ""
if "/ori-demo." not in sandbox or not os.path.isdir(sandbox):
    sys.exit("usage: learn-songs-fixture.py <ori-demo-sandbox> [long-takes] [mib-each]")
root = os.path.join(sandbox, "Music", "REAPER Projects")
GUID = "{C0FFEE00-0000-0000-0000-000000000000}"


def when(month, day):
    return time.mktime((2026, month, day, 10, 0, 0, 0, 0, -1))


def marker(index, at, name, region=False):
    return f'  MARKER {index} {at} "{name}" {1 if region else 0} 0 1 R {GUID} 0\n'


def track(name, length=None):
    lines = f'  <TRACK\n    NAME "{name}"\n'
    if length:
        lines += f"    <ITEM\n      POSITION 0\n      LENGTH {length}\n    >\n"
    return lines + "  >\n"


def project(tempo, markers, tracks):
    return (
        '<REAPER_PROJECT 0.1 "7.27/macOS-arm64" 1760000000\n'
        f"  TEMPO {tempo} 4 4\n" + "".join(markers) + "".join(tracks) + ">\n"
    )


def write(song, text, saved, sparse_to=None):
    folder = os.path.join(root, song)
    os.makedirs(folder, exist_ok=True)
    path = os.path.join(folder, song + ".rpp")
    with open(path, "w", encoding="utf-8") as handle:
        handle.write(text)
    if sparse_to:
        os.truncate(path, sparse_to)
    os.utime(path, (saved, saved))
    os.utime(folder, (saved, saved))


def write_long(song, saved, megabytes):
    """A valid project of about `megabytes` MiB (one track holding many items).
    Stays under the 32 MiB read cap."""
    folder = os.path.join(sandbox, "Beats", "REAPER Projects", song)
    os.makedirs(folder, exist_ok=True)
    path = os.path.join(folder, song + ".rpp")
    item = '    <ITEM\n      POSITION {p}\n      LENGTH 1\n      <SOURCE WAVE\n        FILE "Media/a.wav"\n      >\n    >\n'
    chunk = "".join(item.format(p=i) for i in range(1000))
    target = megabytes << 20
    with open(path, "w", encoding="utf-8") as handle:
        handle.write('<REAPER_PROJECT 0.1 "7.27/macOS-arm64" 1760000000\n  TEMPO 110 4 4\n')
        handle.write(f'  MARKER 1 10 "Start of {song}" 0 0 1 R {GUID} 0\n')
        handle.write(f'  <TRACK\n    NAME "{song}"\n')
        written = 0
        while written < target:
            handle.write(chunk)
            written += len(chunk)
        handle.write("  >\n>\n")
    os.utime(path, (saved, saved))
    os.utime(folder, (saved, saved))


write(
    "Night Drive",
    project(
        "92",
        [
            marker(1, "0", "Intro"),
            marker(2, "12.5", "Verse 1"),
            marker(3, "30", "Chorus", True),
            marker(3, "45.25", "", True),
            marker(4, "120", "Bridge"),
            marker(5, "200.5", "Outro"),
        ],
        [track("Kick", 221), track("Bass", 200), track("Synth Pad"), track("Lead Vocal", 180)],
    ),
    when(10, 9),
)
write(
    "Glass Hour",
    project(
        "128",
        [marker(1, "60", "Drop", True), marker(1, "92", "", True), marker(2, "100", "Breakdown")],
        [track("Drums", 184), track("Bass", 184), track("Arp")],
    ),
    when(9, 18),
)
# Past both caps: 150 track names (128 kept) and 140 markers (128 kept).
write(
    "Big Session",
    project(
        "100",
        [marker(i + 1, str(i * 5), f"Marker {i + 1:03d}") for i in range(140)],
        [track(f"Track {i + 1:03d}", 240 if i == 0 else None) for i in range(150)],
    ),
    when(8, 5),
)
long_count = int(sys.argv[2]) if len(sys.argv) > 2 else 0
long_size = int(sys.argv[3]) if len(sys.argv) > 3 else 30
# The library's fact pass reads at most 512 MiB per scan (defaultSongFactsBudget); a
# folder over it ends "stopped" with the rest unread, which hides the summary this
# fixture exists to show. Keep the total well under it: 15 of 30 MiB is about 470.
if long_count * long_size > 480:
    sys.exit(f"{long_count} x {long_size} MiB is over the 512 MiB read budget of one pass; use at most 480 MiB in all (for example 15 x 30)")
for n in range(1, long_count + 1):
    write_long(f"Long Take {n}", when(5, min(n, 28)), long_size)

# A file that is not a project of its format, and one over the size cap.
write("Broken Take", '<Ableton MajorVersion="5">\n', when(7, 2))
write("Huge Session", project("90", [], [track("Kick", 100)]), when(6, 20), sparse_to=(32 << 20) + 1)

# A Logic Pro bundle: a song in a format Ori cannot read offline.
logic = os.path.join(root, "Logic Sketch", "Logic Sketch.logicx")
os.makedirs(logic, exist_ok=True)
with open(os.path.join(logic, "ProjectData"), "wb") as handle:
    handle.write(b"logic")
for path in (os.path.join(root, "Logic Sketch"), logic):
    os.utime(path, (when(4, 1), when(4, 1)))

print("wrote", root)
for name in sorted(os.listdir(root)):
    print(" ", name)

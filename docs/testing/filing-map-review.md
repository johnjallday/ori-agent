# Review files from the depot

Run the two Chromium journeys against a fresh build and disposable HOME/data directory:

```sh
./scripts/e2e-fresh.sh --port 8974 --sandbox-env GROUNDS_SANDBOX tests/hq-grounds.spec.ts -- --workers=1
```

Use a free port, installed Go/npm dependencies and Playwright Chromium. No API key, model or real Downloads folder is needed. Screenshots are under `test-results/hq-grounds-*/` after the run.

## Try it by hand

1. Start `./scripts/demo-server.sh --open 8974`. Use a disposable HQ and install Filing. Approve **only a synthetic folder**, use manual scans and Filing by kind. Give the folder three settled files (`brief.pdf`, `index.csv`, `map.png`); choose **Scan now**.
2. Open **Map → Grounds**. Tab to **Filing · 3 to review** and press Enter. The dialog shows the approved folder, beside the outlined building. Tab stays in Filing; scrolling over the map does not move it.
3. In **All files**, untick `index.csv`. Wait for **File 2 files**. Press Esc instead: all three files are still loose, the depot has focus and its position is unchanged. Reopen and use browser Back and Forward: the return visit is still beside the depot.
4. With the review open, resize to phone width, then zoom to 200%. Choices reflow into rows with labels. Scroll to the last file and the File control; **Back to map** stays available without swiping. Return to the map and reopen: the exclusion is still saved.
5. Tap **File 2 files** exactly once. Expand **Filed files** on the receipt: `brief.pdf` and `map.png` moved to `Filed/Documents` and `Filed/Images`; `index.csv` is still loose. A pending File request must finish before dismissal.
6. **Overview → Stations → Filing** and a fresh `?mode=map&panel=file-janitor&tab=review` link open the ordinary console, not a depot panel. Other stations keep their usual actions.

This changes the review surface, not folder authorization, scan policy, destinations or saved station positions.

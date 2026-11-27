# Verify the Filing station

Use a disposable HOME, not your own Documents or Downloads. From the feature
worktree in zsh, `source scripts/wt.sh` and run
`ORI_DEMO_SPLIT_PROFILE=1 ORI_DEMO_NO_CODEX=1 wt demo`. Open the printed local
URL. Use only synthetic files under that demo's `home/Documents` and
`home/Downloads`. No model is needed to build, place, file or undo. A live
first-lines read requires a configured direct Claude API model and sends excerpts
only after the separate read approval. Do not use personal files to test it.

## Documents: from the pile to Undo

1. In the demo Documents folder, make `Orders`, `Photos`, `Trips` and `Work`.
   Add eight `order-N.txt` files, four `note-N.txt` files, and `estimate.txt`.
   Let new files settle before the scan. In a personal HQ choose **Create a
   workspace from Documents**, confirm the reviewed draft, open it and press
   **Look around Documents**.
2. Open the workspace **Map**, not Home. The manager can show 13 loose files.
   **Not now** and bringing the offer back do not install or scan. Open **Build
   a Filing station**, review its folder and scope, leave watching unchecked,
   and press **Build the station**. The open depot shows the first scan. All
   source bytes stay put; there is no `Filed/` directory.
3. Open the depot. Choose **Into my folders**. The eight order files start on
   Orders. Add Trips by name; select `estimate.txt` and activate **Move here**
   by keyboard. Drag a note to Trips; place a second note on **New folder** and
   name it Invoices. Before File there are 11 ready, two loose and no Invoices
   on disk. A taken folder name must not silently adopt the existing folder.
4. Press **File 11 files**. Inspect the folders and receipt. Under **Start from
   these placements next time?** only the order pattern is checked. Press
   **Done**, add three more `order-N.txt` files and `postcard.txt`, and **Scan
   now**. If the earlier review is pinned, select **Show the newer scan**.
   Three chips say **From last time**. The Map shows four waiting; none moved.
5. Open **History → Undo all**. The eleven original files return. A created
   directory that cannot be safely removed stays in place with an explanation.
   Rename Orders in this disposable folder. **Settings → Saved placements**
   marks the destination broken; Remove changes the preference, not the files.

On a hired Downloads folder, **Set it up** recommends Filing but does not
build it. With no destination shelves, choose the suggested **By kind** after
Build. `Filed/` appears only when you press File. **Send to another workspace**
opens the separate connection card; it is not permission to send.

Also try selecting and moving by tap at a narrow width and 200% scale. Deny an
optional read: no file opens and the picture stays unchanged. Replace a source
between review and File: it must not overwrite anything. Look in Filing History
for an earlier kept Documents review; its own File and Put back still check the
current folder. No automatic filing, folder moves, cleanup by path, or change
to the Home map is in scope.

## Repeatable isolated checks

```sh
ORI_DEMO_SPLIT_PROFILE=1 ./scripts/e2e-fresh.sh \
  --sandbox-env ORI_HQ_FIRST_VISIT_SANDBOX tests/hq-documents-first-look.spec.ts
./scripts/filing-first-run-check.sh matrix
./scripts/filing-first-run-check.sh upgrade
```

For the first command, `ORI_STATION_SAVED_REVIEW=1` seeds a historical kept
view before Build; `ORI_STATION_THEME=light ORI_STATION_REDUCED=1` repeats the
combined journey with reduced motion and a light theme. The ordinary run uses
a controlled response to test cloud disclosure and decline, not a live provider.

For the report’s **Set it up** route, use `ORI_STATION_ADVICE=partial` or
`complete` with the first command. The manager’s recommendation finishes without
changing its toolbox, installing Filing or leaving a clock running. Only the
complete look continues through **Build** and File/Undo. To check an already
affected workspace, use `ORI_STATION_ADVICE=legacy-seed` with `--rev f7c2c5d15`,
then `ORI_STATION_ADVICE=legacy-check` with `--sandbox <printed sandbox>` on
the current build. The old receipt is visible without changing its permissions.

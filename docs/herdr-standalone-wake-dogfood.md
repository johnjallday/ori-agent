# Herdr Wake Service macOS dogfood

Use a disposable, externally powered Mac with no important unsaved work. These
steps intentionally perform real administrator, launchd, and `pmset` actions;
the automated suite does not.

1. Run `wt herd wake install` and approve the displayed fixed files only after
   confirming the administrator prompt is normal macOS authorization. Do not
   enter a password into any Herdr prompt.
2. Run `wt herd wake status --json` and `wt herd wake doctor`. Confirm the
   installed label is `com.ori.herdr-wake`, the allowed UID is the logged-in
   user, protocol/state versions are compatible, and the self-test passed.
3. Create a harmless continuation with `wt herd continue --at <future-time>
   --wake`, inspect it with `wt herd schedule show <id>`, then cancel it. Check
   that the result retains direct verification and withdrawal evidence.
4. Before and after that cycle, create or observe a non-Herdr scheduled wake.
   Verify it remains present when Herdr registers an earlier candidate, a later
   candidate, cancels one, and after restarting the wake daemon.
5. Confirm a future Overnight Run on a supported plan-backed exact session;
   check its `overnight_scheduled_start` candidate is directly verified and
   withdrawn at start. Test `--stay-awake` separately and confirm its
   user-level assertion is reported without a reset wake or deliberate sleep.
6. Exercise `wt herd wake uninstall` only after canceling every active
   wake-enabled continuation and Overnight Run. Verify that the user-level
   dispatcher, Herdr sessions, worktrees, Ori configuration, and the foreign
   wake event remain intact.

If any direct verification, restart reconciliation, or exact withdrawal is
uncertain, leave the Mac awake and run `wt herd wake doctor`; do not retry by
using `pmset cancelall` or by starting Ori as a wake-service substitute.

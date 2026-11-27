# Verify Downloads filing memory

Start an isolated server with a disposable home and data directory. Use a free port and keep the printed sandbox path for the restart:

```sh
sandbox=$(mktemp -d)
ORI_DEMO_SPLIT_PROFILE=1 ./scripts/demo-server.sh 8957 "$sandbox"
```

For chat, start that command with `with-keys` and configure an Anthropic Claude model in the sandbox. For the credential-free browser regression, use `./scripts/e2e.sh --port 8957 tests/filing-memory.spec.ts` instead. The browser test makes its own synthetic folders and workspace.

1. Create `$sandbox/home/Downloads` and `$sandbox/home/Documents/Travel/Receipts`. Put a settled `hotel-receipt.pdf` fixture in Downloads (backdate the fixture's modification time). Create one Downloads workspace with Filing, link **only** Downloads, choose manual review, and add the optional Curator. Do not attach Receipts or turn on monitoring.
2. Open **Workspace → Details → Filing → Review** and scan. Expand the receipt's filename. Click **Choose a folder…**; select Receipts, or paste `~/Documents/Travel/Receipts` and click **Preview folder**. The file should still be in Downloads. The list in this preview, not the other ticks in Review, is what this File button approves.
3. Press **File**. Verify one filed item and the destination on the receipt. Click **Suggest this folder for similar files**. Stop the demo and restart with the same port, sandbox, and model configuration.
4. Ask the Downloads Curator “Where did my hotel receipt go?” Search **Filing → History** for `hotel receipt`. Compare the verified destination and time (chat may use UTC). Neither answer is a claim about where the file is now.
5. Add settled `hotel-receipt-may.pdf` and `signed-lease.pdf`; scan. Only the new receipt should have a suggested destination, explained by the original receipt. **Review suggestion** and cancel, or **Change folder**. Neither file should move. **Forget example** removes the suggestion, not the history record.
6. In History, choose **Undo all** for the original filing. Ask again: the journal should report the verified return. Put a different `signed-lease.pdf` in Receipts; preview and file the Downloads lease there. Expect a refusal and both originals unchanged. Neither History search nor chat should describe that attempt as a successful filing.
7. Check the workspace's linked directories and monitoring: Receipts is not attached or monitored. A renamed/replaced, unsupported, or cross-device destination must not redirect the move. No overwrite or copy fallback is offered. Local Filing and sending over an existing workspace connection still use their original controls.

History searches retained Ori actions only. A suggestion requires its confirmed example and destination identity to remain available. Finder moves, whole-computer searches, recursive destination browsing and automatic filing are not part of this feature.

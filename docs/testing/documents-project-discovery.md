# Documents project discovery: independent walkthrough

**Participant prompt:** “Use Ori to find out when your BIO 101 lab is due.” Start them on the verified Documents workspace. Do not name the folder choice, workspace, blueprint, or agent they should use. Record their first pause, any help or abandonment, their answer, and how they checked it. A scripted browser run does not measure these observations.

## Facilitator setup (local macOS)

From the feature worktree in zsh with `with-keys` available:

```sh
with-keys ./scripts/e2e-documents-projects.sh --seed
# Use exactly the ori-e2e.* sandbox printed above, never your actual HOME.
ORI_DEMO_SPLIT_PROFILE=1 with-keys ./scripts/demo-server.sh 19367 '<printed sandbox>'
```

Open `http://localhost:19367/workspaces/documents?mode=details`. The isolated profile contains an HQ, a verified Documents Manager, and school folders but no BIO 101 workspace. The host needs local filesystem access to the sandbox. The live syllabus read uses the configured Claude API model; no email, calendar or other integration is required. For file selection, the fixture's Documents directory is **`<sandbox>/home/Documents`**, not the facilitator's Documents.

## Checks after the uncoached attempt

1. **Find projects in this folder → Documents/School → Look for projects** shows up to two suggestions with actual filenames. The archive is a question, not an assertion that the student is enrolled. Shopping.txt should not be suggested.
2. **Not now** removes a suggestion for this look. **Ignore this folder** on Archived History suppresses it on the next look. **Review setup** names the course folder and first action; **Cancel** creates nothing.
3. Confirm BIO 101. A new managed workspace opens directly at Course setup without importing a blueprint or granting access to the course folder. Repeating the journey opens the same workspace.
4. Select `School/BIO 101/Syllabus.pdf`. **Not now** must leave the source list empty. Select it again, review the provider statement, consent and choose **Read selected syllabus**. Review the parsed file, continue, inspect the skill, then **Trust and enable → Run intake**.
5. Read **Next stated deadline** and compare its quote with the original PDF. The lab is due December 14 of the year printed there. The final presentation is TBD. No Apply or calendar write is needed; leave proposals pending.

`./scripts/e2e-documents-projects.sh` exercises discovery, repeat and decline without a paid run. `with-keys ./scripts/e2e-documents-projects.sh --live` also exercises the answer. Every invocation gets a fresh split HOME/data. Stop the interactive sandbox server with Ctrl-C. Discovery does not watch folders, read documents, infer enrollment, move originals or update a profile.

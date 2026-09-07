# Reset test installation

`resetfixture.NewSeeded(t)` creates a **new test-owned** installation with real
settings, completed setup/identity, one agent, one folder workspace, one SQLite
conversation/message, and an upload. `New(t)` provides just isolation, memory
secrets and preservation sentinels. Neither accepts an existing root or URL.

- Data directory, CWD and HOME are separate, newly created directories. Roots
  for workspaces, vaults, templates, legacy workflow templates and CLI discovery
  stay under the fixture. Environment variables are cleared by default (apart
  from Windows OS runtime paths); external MCP imports are disabled. PATH is an
  empty fixture directory, so native credential/CLI commands cannot be found.
- Inject `f.Secrets()` into configuration owners. `f.OtherSecrets()` models an
  independent namespace. Values are generated locally, stay in memory, and are
  never logged. Changing HOME alone does **not** isolate macOS Keychain.
- Sibling/prefix-sharing, HOME, workspace, vault and nested-data sentinels are
  checked byte-for-byte at cleanup. `WriteFile` uses `os.Root` confinement,
  including symlink escapes. Only the fixture's newly allocated directory is
  removed; no cleanup takes caller-supplied deletion paths.
- `f.StartHTTP(t, handler)` creates its own loopback listener/OS-assigned port.
  Wire the handler from fixture stores. `Do` takes only root-relative paths,
  disables proxies/redirect following, and refuses requests after close. It
  never reads `PLAYWRIGHT_BASE_URL` or attaches to a running server.
- `NewWriter(t, callback)` models a background save/flush. `Begin` pauses before
  the callback; `Release` runs it; `Wait` reports its error. `Stop` cancels paused
  writes and joins executing work. Callbacks must terminate or use bounded
  contexts. Register store cleanup **before** writer/HTTP cleanup so writers and
  requests drain before stores close and sentinels are checked.

Use sequential tests: `testing.Setenv`/`Chdir` restore process state and forbid
parallel use. Retain the **same fixture** and memory stores across close/reopen;
creating a new fixture is not evidence that reset persisted. Use an external
`foo_test` package when the package under test is one of the seed's dependencies.

```sh
go test ./internal/testutil/resetfixture -count=1
go test ./internal/settingshttp -run ResetFixture -count=1
go test -race ./internal/testutil/resetfixture ./internal/settingshttp -run 'Fixture|Writer' -count=1
```

## What this does not prove yet

This is a fixture for package/lifecycle characterization, **not an OS security
sandbox or a complete browser/server-process harness**. Do not launch arbitrary
executables, default production server initialization or external providers from
it. Tests must explicitly inject memory secrets and construct only the services
under examination. The later destructive Playwright fixture must own its process,
listener and environment too; a user-provided port or opt-in is not ownership.

Vault sentinels are opaque retained files, not decryptable vaults. Full inventory
seeding, legacy vault blockers, controlled secret-store failures, production host
shutdown/restart and browser journeys remain later checklist work. Native
Keychain operations and real credentials are never part of these checks.

The fixture already reproduces cached configuration recreating a removed
`settings.json`. Its real-handler test intentionally exercises settings only:
the current sessions reset removes `data/workspaces` recursively, which violates
the nested retained-folder sentinel. That is a product defect to fix, not a
reason to remove the sentinel or claim full reset safety.

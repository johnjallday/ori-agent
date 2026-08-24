# Workspace Surface demo plugin

This is the smallest installable, domain-neutral Workspace Surface v1 example.
It contributes one Map station and sandboxed modal backed by a long-lived MCP
stdio service. The service is a standard-library Python script with no network
or filesystem behavior.

The example intentionally demonstrates:

- a polled `status.read` operation;
- a bounded `greeting.create` operation;
- a `confirmation_required` operation;
- host-managed state and close intents;
- rejection of an undeclared `service.admin` operation; and
- one artifact digest reused for explicitly listed supported platforms.

## Validate locally

From the Ori repository root:

```bash
go test ./internal/plugin \
  -run '^TestWorkspaceSurfaceExampleValidatesAndInstallsArtifact$' -v
```

That command parses both portable identities plus `.ori-plugin/plugin.json`,
verifies and installs the selected artifact into a private temporary directory,
starts its MCP stdio service, and calls its status operation. It does not alter
your Ori profile.

If `artifacts/demo-service.py` changes, update every artifact's exact `size` and
`sha256` before validation:

```bash
wc -c < examples/plugins/workspace-surface-demo/artifacts/demo-service.py
shasum -a 256 examples/plugins/workspace-surface-demo/artifacts/demo-service.py
```

## Preview and install by local path

Start Ori, then run from the repository root (change the URL if needed):

```bash
ORI_URL=http://127.0.0.1:8765
PLUGIN_PATH="$(pwd)/examples/plugins/workspace-surface-demo"

curl --fail-with-body -sS "$ORI_URL/api/plugins/install" \
  -H 'Content-Type: application/json' \
  --data "$(jq -n --arg source "$PLUGIN_PATH" '{source:$source,confirm:false}')" | jq

curl --fail-with-body -sS "$ORI_URL/api/plugins/install" \
  -H 'Content-Type: application/json' \
  --data "$(jq -n --arg source "$PLUGIN_PATH" '{source:$source,confirm:true}')" | jq

curl --fail-with-body -sS -X POST \
  "$ORI_URL/api/plugins/workspace-surface-demo/enable" | jq
```

Reload a workspace's Operations Map to see **Surface Demo**. Installed plugins
start disabled so installation and activation remain separate decisions.
Disable or uninstall with:

```bash
curl --fail-with-body -sS -X POST \
  "$ORI_URL/api/plugins/workspace-surface-demo/disable" | jq
curl --fail-with-body -sS -X DELETE \
  "$ORI_URL/api/plugins/workspace-surface-demo" | jq
```

## Copying the example

1. Give the Claude/Codex/Ori manifests the exact same lowercase name and exact
   same version.
2. Replace the service and UI, then declare only operations the frame needs.
3. Keep paths relative and inert; never place commands, raw workspace paths, or
   endpoints in a surface/capability/blueprint projection.
4. List every supported OS/architecture artifact with exact digest and size.
5. Copy the SDK rather than reaching into Ori's parent DOM.
6. Validate, preview the full trust report, then test install, disable, update,
   and uninstall while the modal is open.

See [`docs/plugins.md`](../../../docs/plugins.md#workspace-surface-v1) for the
manifest contract, SDK, lifecycle, trust boundary, errors, schemas,
accessibility requirements, and compatibility checks.

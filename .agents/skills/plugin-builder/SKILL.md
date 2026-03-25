---
name: plugin-builder
description: Creates Ori Agent plugins from natural language. Use when the user asks to "create a plugin", "build a plugin", "make a tool that...", or wants to add new capabilities to Ori Agent.
allowed-tools: Read, Write, Edit, Bash, Glob, Grep
---

# Plugin Builder

Build Ori Agent plugins through conversation. Plugins are Go executables that communicate via gRPC.

## Workflow

### 1. Gather Requirements

Ask the user:
- What should the plugin do?
- What inputs does it need?
- What should it return?
- Does it need external APIs or packages?
- Where should I save it?

### 2. Generate Code

Create these files:
- `main.go` - Plugin implementation
- `plugin.yaml` - Tool definition
- `go.mod` - Dependencies

**Show the code to the user and ask for approval before writing.**

### 3. Build

After approval, write files and run:
```bash
go mod tidy
go build -o <plugin-name> .
```

### 4. Handle Errors

If build fails:
1. Analyze the error
2. Fix the code
3. Retry (up to 3 attempts)
4. If still failing, explain and suggest manual fixes

## Updating Existing Plugins

When modifying an existing plugin:
1. Read existing files first
2. Make incremental edits (don't regenerate everything)
3. Preserve manual changes
4. Show diff before applying
5. Rebuild after changes

## Reference

For code templates and examples, see [TEMPLATES.md](TEMPLATES.md).
For common errors and fixes, see [ERRORS.md](ERRORS.md).

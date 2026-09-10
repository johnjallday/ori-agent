# Setup quests for your project templates

A **setup quest** is a saved guide attached to a project template you own. It helps someone connect a project, create its workspace, choose a runtime mode, and review staffing without turning the template into executable code.

Open **Templates**, select a user-owned template, and choose the **Setup quest** tab. Ori shows whether the template is ready and what is missing when it is not.

## What a quest can contain

You can edit:

- the quest title and description;
- one integration selected from Ori's host-reviewed integration list;
- titles and descriptions for the five fixed stages; and
- short copy used when creating a Home and choosing a runtime mode.

Every quest has exactly these stages, once and in this order:

1. Integration
2. Project
3. Workspace
4. Staffing
5. Summary

You cannot add commands, action URLs, arbitrary routes, extra steps, or a plugin owner. Ori generates and preserves the attachment, quest, and step identities.

## When a template is eligible

Ori checks that the template has:

- a valid project connection with at least one usable existing- or new-project path;
- a real skeleton and matching project entry when the new-project path needs them;
- a current Assistant Program with separate Home and exact-project roles;
- a File-only runtime mode; and
- a post-workspace Setup Wizard with a required runtime-mode step.

The editor reports these prerequisites as separate, actionable items. Built-in and plugin-owned templates remain read-only. Duplicate a template you are allowed to customize before adding your own quest.

## Preview and save do not start setup

**Preview** normalizes the same fields as save and shows the five stages. **Save** updates only the template manifest. Neither operation installs software, creates or connects a project, creates a workspace or Home, launches an application, changes runtime settings, staffs agents, or writes setup progress.

Saving uses a revision check. If the template changed in another window, Ori refuses the stale save instead of overwriting it. Removing a quest deletes only its saved guidance; it does not delete or undo integration, project, workspace, Home, or agent state.

## Copy and template-folder import

Duplicating a template copies the editable quest text but assigns a fresh attachment ID, quest ID, and five fresh step IDs. The copied quest is rebound to the new template ID and revalidated. It never shares progress with the source.

Importing a quest-bearing template folder applies the same rule. Ori accepts only a complete, valid template manifest with the same strict inert declaration shape; unsupported or executable fields are rejected. Accepted declarations receive fresh local identities before they can be listed or launched. Ori does not provide a standalone quest export, sharing, or plugin-quest copy flow, and progress, resource IDs, permissions, and integration state are never portable.

## After setup starts

The first durable setup write locks both the quest definition and the template fields that affect execution. This prevents a resumed journey from silently changing underneath the user. Rename-only and other display-only edits can continue, but changing protected setup content requires duplicating the template to create a new identity.

The Templates page always labels an authored quest as **Source · User template**. Installed plugin quests remain labeled as plugin-owned or Ori compatibility setup and continue to use their existing read-only path.

/**
 * Google Account Connection — Settings card controller.
 *
 * Talks to /api/connections/google/{status,connect,disconnect} and renders the
 * Connected / Disconnected states plus per-product rows. Identity-only for now;
 * product Enable actions and the disconnect impact-preview arrive in later groups.
 */
(function () {
  "use strict";

  const PRODUCT_LABELS = { gmail: "Gmail", calendar: "Calendar", drive: "Drive" };
  const GMAIL_SEND_SCOPE = "https://www.googleapis.com/auth/gmail.send";

  // Drive Advanced-setup constants. These mirror internal/drive/preset.go: the
  // server is added to the global MCP registry and connected through the generic
  // remote-MCP OAuth flow, using the operator's own Web client (self-hosted).
  const DRIVE_SERVER_NAME = "google-drive";
  const DRIVE_MCP_URL = "https://drivemcp.googleapis.com/mcp/v1";
  const DRIVE_DOCS_URL =
    "https://developers.google.com/workspace/drive/api/guides/configure-mcp-server";
  const DRIVE_SCOPE_DISCLOSURE =
    "Connecting Drive requests read-only access (drive.readonly). Google may additionally grant drive.file for files you open with Ori. Ori never requests or uses write access, and exposes only search + read tools.";
  const MCP_OAUTH_EVENT = "ori:mcp-oauth";
  const HEALTH_LABELS = {
    not_enabled: "Not enabled",
    connecting: "Connecting…",
    healthy: "Connected",
    scope_upgrade_required: "Scope upgrade required",
    reconnect_required: "Reconnect required",
    advanced_setup_required: "Setup required",
    provider_unavailable: "Unavailable",
    rate_limited: "Rate limited",
    admin_blocked: "Blocked",
    error: "Error",
  };
  // Headings for the credential-vault step that must complete before Gmail
  // authorization can start. Each names the action, so the panel never reads as
  // a dead end.
  const VAULT_ACTION_TITLES = {
    create: "Create a vault first",
    choose: "Choose a vault",
    unlock: "Unlock your vault",
    repair: "That vault is unavailable",
  };
  const STATE_LABELS = {
    connected: "Connected",
    partially_connected: "Partially connected",
    needs_attention: "Needs attention",
    connecting: "Connecting…",
  };

  class GoogleConnectionManager {
    constructor() {
      this.el = {};
    }

    init() {
      if (!document.getElementById("googleConnStatus")) return;
      this.cache();
      this.bind();
      this.refresh();
    }

    cache() {
      const id = (x) => document.getElementById(x);
      this.el = {
        status: id("googleConnStatus"),
        statusText: id("googleConnStatusText"),
        connected: id("googleConnConnected"),
        disconnected: id("googleConnDisconnected"),
        email: id("googleConnEmail"),
        name: id("googleConnName"),
        avatar: id("googleConnAvatar"),
        badge: id("googleConnBadge"),
        products: id("googleConnProducts"),
        vault: id("googleConnVault"),
        migrate: id("googleConnMigrate"),
        driveSetup: id("googleConnDriveSetup"),
        confirm: id("googleConnConfirm"),
        connectBtn: id("googleConnConnectBtn"),
        disconnectBtn: id("googleConnDisconnectBtn"),
        switchBtn: id("googleConnSwitchBtn"),
        error: id("googleConnError"),
      };
    }

    bind() {
      if (this.el.connectBtn) this.el.connectBtn.addEventListener("click", () => this.connect());
      // Disconnect + Switch both preview their impact before acting.
      if (this.el.disconnectBtn) this.el.disconnectBtn.addEventListener("click", () => this.confirmAccountDisconnect());
      if (this.el.switchBtn) this.el.switchBtn.addEventListener("click", () => this.switchAccount());
    }

    async refresh() {
      try {
        const res = await fetch("/api/connections/google/status", { headers: { Accept: "application/json" } });
        if (!res.ok) throw new Error("status " + res.status);
        this.render(await res.json());
      } catch (e) {
        this.showStatusText("Couldn't load Google connection status.");
      }
    }

    render(conn) {
      this.conn = conn || null;
      this.el.status.classList.add("d-none");
      this.hideConfirm();
      const connected = conn && conn.subject && conn.state !== "not_connected" && conn.state !== "disconnecting";
      if (connected) {
        this.el.disconnected.classList.add("d-none");
        this.el.connected.classList.remove("d-none");
        this.el.email.textContent = conn.email || "";
        this.el.name.textContent = conn.display_name || "";
        this.el.avatar.textContent = (conn.email || "G").charAt(0).toUpperCase();
        this.el.badge.textContent = STATE_LABELS[conn.state] || "Connected";
        this.el.badge.setAttribute("data-state", conn.state || "connected");
        this.renderProducts(conn.grants || []);
        // Collapse the Drive setup panel once Drive is connected.
        const drive = (conn.grants || []).find((g) => g.product === "drive");
        if (drive && drive.enabled) this.hideDriveSetup();
        // Offer to migrate any legacy per-workspace Gmail setup (non-blocking).
        this.refreshMigratable();
        // A callback that failed on a local vault step sends the user back here
        // with the exact repair to offer, so they resume instead of rediscovering.
        this.applyCallbackRepairHint();
      } else {
        this.el.connected.classList.add("d-none");
        this.el.disconnected.classList.remove("d-none");
        this.hideDriveSetup();
        this.hideMigrate();
        this.cancelVaultAction();
      }
    }

    renderProducts(grants) {
      this.el.products.innerHTML = "";
      grants.forEach((g) => {
        const row = document.createElement("div");
        row.className = "gc-product-row";

        const name = document.createElement("span");
        name.className = "gc-product-name";
        name.textContent = PRODUCT_LABELS[g.product] || g.product;

        const right = document.createElement("div");
        right.className = "gc-product-right";

        const pill = document.createElement("span");
        pill.className = "gc-pill";
        pill.setAttribute("data-health", g.health || "not_enabled");
        pill.textContent = HEALTH_LABELS[g.health] || (g.enabled ? "Enabled" : "Not enabled");
        right.appendChild(pill);

        // Gmail: enable when off, or offer the explicit send upgrade once healthy.
        if (g.product === "gmail" && !g.enabled) {
          right.appendChild(this.enableButton("Enable", null));
        } else if (g.product === "gmail" && g.health === "healthy" && !(g.granted_scopes || []).includes(GMAIL_SEND_SCOPE)) {
          right.appendChild(this.enableButton("Enable sending", "send"));
        } else if (g.product === "drive" && !g.enabled) {
          // Drive: Advanced setup collects the operator Web client, then connects
          // through the generic remote-MCP OAuth flow (read-only, fail-closed).
          const btn = document.createElement("button");
          btn.type = "button";
          btn.className = "modern-btn modern-btn-secondary gc-enable-btn";
          btn.textContent = "Set up";
          btn.setAttribute("aria-label", "Set up Google Drive");
          btn.addEventListener("click", () => this.toggleDriveSetup());
          right.appendChild(btn);
        }

        // Failure states get an inline Reconnect action (FR 86), not just a pill.
        if (g.enabled && (g.health === "reconnect_required" || g.health === "provider_unavailable")) {
          const rc = document.createElement("button");
          rc.type = "button";
          rc.className = "modern-btn modern-btn-primary gc-enable-btn";
          rc.textContent = "Reconnect";
          rc.setAttribute("aria-label", "Reconnect " + (PRODUCT_LABELS[g.product] || g.product));
          rc.addEventListener("click", () => this.reconnectProduct(g.product));
          right.appendChild(rc);
        }

        // Any enabled product can be disconnected on its own (impact-previewed).
        if (g.enabled) {
          const dc = document.createElement("button");
          dc.type = "button";
          dc.className = "modern-btn modern-btn-secondary gc-disconnect-btn";
          dc.textContent = "Disconnect";
          dc.setAttribute("aria-label", "Disconnect " + (PRODUCT_LABELS[g.product] || g.product));
          right.appendChild(dc);
          dc.addEventListener("click", () => this.confirmProductDisconnect(g.product));
        }

        row.appendChild(name);
        row.appendChild(right);
        this.el.products.appendChild(row);
      });
    }

    enableButton(label, scope) {
      const btn = document.createElement("button");
      btn.type = "button";
      btn.className = "modern-btn modern-btn-secondary gc-enable-btn";
      btn.textContent = label;
      btn.addEventListener("click", () => this.enableGmail(btn, scope));
      return btn;
    }

    /**
     * Starts Gmail enablement. The server resolves the credential vault BEFORE
     * handing back an authorize URL, so a locked/missing/ambiguous vault comes
     * back as a 409 vault_action_required instead of failing after the user has
     * already authorized at Google. `vaultId` carries the user's answer when we
     * re-enter after a create/choose/unlock step.
     */
    async enableGmail(btn, scope, vaultId) {
      this.hideError();
      if (btn) btn.disabled = true;
      try {
        const params = new URLSearchParams();
        if (scope === "send") params.set("scope", "send");
        if (vaultId) params.set("vault_id", vaultId);
        const query = params.toString();
        const url = "/api/connections/google/gmail/enable" + (query ? "?" + query : "");
        const res = await fetch(url, { method: "POST", headers: { Accept: "application/json" } });
        const data = await res.json().catch(() => ({}));
        if (res.status === 409 && data.error === "vault_action_required") {
          // Remember the intent so the user never has to restate it (FR 10).
          this.pendingEnable = { scope: scope || null };
          this.showVaultAction(data);
          return;
        }
        if ((res.status === 409 || res.status === 503) && data.message) {
          this.showError(data.message);
          return;
        }
        if (!res.ok || !data.authorize_url) throw new Error("enable failed");
        this.hideVaultAction();
        window.location.assign(data.authorize_url);
      } catch (e) {
        this.showError("Couldn't start Gmail access. Please try again.");
      } finally {
        if (btn) btn.disabled = false;
      }
    }

    // --- Credential vault: create / choose / unlock / repair -------------------

    /**
     * Reads the repair hint the OAuth callback result page appended
     * (?gc_action=unlock&gc_vault=…) and re-opens that step directly. The hint is
     * consumed once so a later reload doesn't resurrect a stale prompt.
     */
    applyCallbackRepairHint() {
      let params;
      try {
        params = new URLSearchParams(window.location.search);
      } catch (e) {
        return;
      }
      const action = params.get("gc_action");
      if (!action || this.repairHintApplied) return;
      this.repairHintApplied = true;

      const vaultId = params.get("gc_vault") || "";
      // The Google half already succeeded; only the local vault step is left.
      this.pendingEnable = { scope: null };
      if (action === "unlock" && vaultId) {
        this.showVaultAction({
          action: "unlock",
          message: "You're signed in with Google. Unlock the vault to finish enabling Gmail.",
          vault_id: vaultId,
        });
      } else {
        this.startVaultRepair();
      }

      params.delete("gc_action");
      params.delete("gc_vault");
      const query = params.toString();
      window.history.replaceState(
        {},
        "",
        window.location.pathname + (query ? "?" + query : "") + window.location.hash,
      );
    }

    /**
     * Asks the server which vault step is needed. This is a read-only preflight:
     * it starts no OAuth flow and records no choice, so re-opening the prompt
     * after a failed callback never re-contacts Google.
     */
    async startVaultRepair() {
      try {
        const res = await fetch("/api/connections/google/vault", { headers: { Accept: "application/json" } });
        if (!res.ok) return;
        const data = await res.json();
        if (!data || data.action === "ready") {
          // The vault recovered on its own — offer the retry, don't take it.
          this.showVaultAction({
            action: "repair",
            message: "Your vault is available again. Select it to finish enabling Gmail.",
            vaults: data && data.vault_id ? [{ id: data.vault_id, name: data.vault_name || data.vault_id }] : [],
          });
          return;
        }
        this.showVaultAction(data);
      } catch (e) {
        this.showError("Couldn't check your credential vault. Please try again.");
      }
    }

    /**
     * Resumes the remembered enable action with a now-usable vault. Cancelling
     * instead simply drops the intent — Gmail stays disabled and Google is never
     * opened (FR 11).
     */
    resumePendingEnable(vaultId) {
      const pending = this.pendingEnable || {};
      this.hideVaultAction();
      this.enableGmail(null, pending.scope, vaultId);
    }

    cancelVaultAction() {
      this.pendingEnable = null;
      this.hideVaultAction();
    }

    hideVaultAction() {
      if (this.el.vault) {
        this.el.vault.innerHTML = "";
        this.el.vault.classList.add("d-none");
      }
    }

    /** Renders the one step that unblocks authorization. */
    showVaultAction(data) {
      const host = this.el.vault;
      if (!host) {
        this.showError(data.message || "Ori needs a vault to store your Google credentials.");
        return;
      }
      host.innerHTML = "";
      host.classList.remove("d-none");

      const title = document.createElement("div");
      title.className = "gc-vault-title";
      title.textContent = VAULT_ACTION_TITLES[data.action] || "Credential vault";
      host.appendChild(title);

      const note = document.createElement("p");
      note.className = "gc-vault-note";
      note.textContent = data.message || "";
      host.appendChild(note);

      if (data.action === "unlock") this.renderVaultUnlock(host, data);
      else if (data.action === "create") this.renderVaultCreate(host);
      else this.renderVaultChoose(host, data);

      const first = host.querySelector("input, button");
      if (first) first.focus();
    }

    /** Unlock the recorded vault, then resume (FR 8, 10). */
    renderVaultUnlock(host, data) {
      const field = this.vaultField(
        host,
        data.vault_name ? "Password for " + data.vault_name : "Vault password",
        "password",
      );
      const error = this.vaultError(host);
      const actions = document.createElement("div");
      actions.className = "gc-vault-actions";

      const unlock = this.vaultButton("Unlock and continue", "modern-btn-primary", async () => {
        error.textContent = "";
        const res = await this.postJSON("/api/vault/unlock", {
          vault_id: data.vault_id,
          vault_password: field.value,
        });
        if (!res.ok) {
          error.textContent = res.message || "Couldn't unlock that vault. Check the password and try again.";
          field.focus();
          return;
        }
        this.resumePendingEnable(data.vault_id);
      });
      field.addEventListener("keydown", (ev) => {
        if (ev.key === "Enter") {
          ev.preventDefault();
          unlock.click();
        }
      });

      actions.appendChild(unlock);
      actions.appendChild(this.vaultButton("Cancel", "modern-btn-secondary", () => this.cancelVaultAction()));
      host.appendChild(actions);
    }

    /** Inline vault creation, then resume (FR 5, 10). */
    renderVaultCreate(host) {
      const name = this.vaultField(host, "Vault name", "text");
      name.value = "Personal";
      const password = this.vaultField(host, "Vault password", "password");
      const error = this.vaultError(host);
      const actions = document.createElement("div");
      actions.className = "gc-vault-actions";

      actions.appendChild(
        this.vaultButton("Create vault and continue", "modern-btn-primary", async () => {
          error.textContent = "";
          if (!name.value.trim() || !password.value) {
            error.textContent = "Enter a name and password for the new vault.";
            return;
          }
          const res = await this.postJSON("/api/vault/vaults", {
            name: name.value.trim(),
            vault_password: password.value,
          });
          const created = (res.data && (res.data.vault || (res.data.data && res.data.data.vault))) || null;
          if (!res.ok || !created || !created.id) {
            error.textContent = res.message || "Couldn't create that vault. Please try again.";
            return;
          }
          this.resumePendingEnable(created.id);
        }),
      );
      actions.appendChild(this.vaultButton("Cancel", "modern-btn-secondary", () => this.cancelVaultAction()));
      host.appendChild(actions);
    }

    /**
     * Choose among existing vaults (FR 6) — also the repair path when the
     * remembered vault is gone (FR 9). A locked choice routes through unlock
     * rather than failing later.
     */
    renderVaultChoose(host, data) {
      const options = data.vaults || [];
      const error = this.vaultError(host);
      const group = document.createElement("div");
      group.className = "gc-vault-options";
      group.setAttribute("role", "radiogroup");
      group.setAttribute("aria-label", "Choose a vault for Google credentials");

      options.forEach((v, i) => {
        const label = document.createElement("label");
        label.className = "gc-vault-option";
        const radio = document.createElement("input");
        radio.type = "radio";
        radio.name = "gcVaultChoice";
        radio.value = v.id;
        if (i === 0) radio.checked = true;
        label.appendChild(radio);
        const text = document.createElement("span");
        text.textContent = v.name || v.id;
        label.appendChild(text);
        if (v.locked) {
          const pill = document.createElement("span");
          pill.className = "gc-vault-locked";
          pill.textContent = "Locked";
          label.appendChild(pill);
        }
        group.appendChild(label);
      });
      host.appendChild(group);

      const actions = document.createElement("div");
      actions.className = "gc-vault-actions";
      if (options.length) {
        actions.appendChild(
          this.vaultButton("Use this vault", "modern-btn-primary", () => {
            error.textContent = "";
            const picked = group.querySelector("input:checked");
            if (!picked) {
              error.textContent = "Select a vault to continue.";
              return;
            }
            const chosen = options.find((v) => v.id === picked.value);
            if (chosen && chosen.locked) {
              // Unlock first; the server would otherwise refuse at the same point.
              this.showVaultAction({
                action: "unlock",
                message: "Unlock " + (chosen.name || "this vault") + " to continue enabling Gmail.",
                vault_id: chosen.id,
                vault_name: chosen.name,
              });
              return;
            }
            this.resumePendingEnable(picked.value);
          }),
        );
      }
      actions.appendChild(
        this.vaultButton("Create a new vault", "modern-btn-secondary", () => {
          this.showVaultAction({
            action: "create",
            message: "Create a vault to store your Google credentials, then Ori will continue enabling Gmail.",
          });
        }),
      );
      actions.appendChild(this.vaultButton("Cancel", "modern-btn-secondary", () => this.cancelVaultAction()));
      host.appendChild(actions);
    }

    vaultField(host, labelText, type) {
      const wrap = document.createElement("label");
      wrap.className = "gc-vault-field";
      const span = document.createElement("span");
      span.textContent = labelText;
      const input = document.createElement("input");
      input.type = type;
      input.className = "modern-input";
      wrap.appendChild(span);
      wrap.appendChild(input);
      host.appendChild(wrap);
      return input;
    }

    vaultError(host) {
      const p = document.createElement("p");
      p.className = "gc-vault-error";
      p.setAttribute("role", "alert");
      host.appendChild(p);
      return p;
    }

    vaultButton(label, variant, onClick) {
      const btn = document.createElement("button");
      btn.type = "button";
      btn.className = "modern-btn " + variant;
      btn.textContent = label;
      btn.setAttribute("aria-label", label);
      btn.addEventListener("click", onClick);
      return btn;
    }

    /** POSTs JSON and normalizes the vault API's success/error envelope. */
    async postJSON(url, body) {
      try {
        const res = await fetch(url, {
          method: "POST",
          headers: { "Content-Type": "application/json", Accept: "application/json" },
          body: JSON.stringify(body),
        });
        const data = await res.json().catch(() => ({}));
        return {
          ok: res.ok,
          data,
          message: data.error || data.message || (data.data && data.data.message) || "",
        };
      } catch (e) {
        return { ok: false, data: {}, message: "" };
      }
    }

    async connect() {
      this.hideError();
      this.el.connectBtn.disabled = true;
      try {
        const res = await fetch("/api/connections/google/connect", { method: "POST", headers: { Accept: "application/json" } });
        const data = await res.json().catch(() => ({}));
        if (res.status === 409 && data.error === "not_configured") {
          this.showError(data.message || "Google sign-in isn't configured in this build yet.");
          return;
        }
        if (!res.ok || !data.authorize_url) throw new Error("connect failed");
        window.location.assign(data.authorize_url);
      } catch (e) {
        this.showError("Couldn't start Google sign-in. Please try again.");
      } finally {
        this.el.connectBtn.disabled = false;
      }
    }

    showStatusText(t) {
      if (this.el.statusText) this.el.statusText.textContent = t;
    }
    showError(msg) {
      if (this.el.error) {
        this.el.error.textContent = msg;
        this.el.error.classList.remove("d-none");
      }
    }
    hideError() {
      if (this.el.error) {
        this.el.error.removeAttribute("data-tone");
        this.el.error.classList.add("d-none");
      }
    }
    /**
     * Shows an informational outcome that is neither success nor failure — e.g.
     * a migration Ori safely skipped. Reusing showError would mislabel a correct
     * outcome as a fault, so this renders in a neutral tone.
     */
    showNotice(msg) {
      if (!this.el.error) return;
      this.el.error.textContent = msg;
      this.el.error.setAttribute("data-tone", "notice");
      this.el.error.classList.remove("d-none");
    }

    // --- Drive Advanced setup -------------------------------------------------

    toggleDriveSetup() {
      const host = this.el.driveSetup;
      if (!host) return;
      if (host.classList.contains("d-none")) {
        this.renderDriveSetup();
        host.classList.remove("d-none");
        const input = host.querySelector("[data-drive-client-id]");
        if (input) input.focus();
      } else {
        this.hideDriveSetup();
      }
    }

    hideDriveSetup() {
      if (this.el.driveSetup) this.el.driveSetup.classList.add("d-none");
    }

    renderDriveSetup() {
      const host = this.el.driveSetup;
      if (!host) return;
      const email = (this.conn && this.conn.email) || "your Google account";
      host.innerHTML = "";

      const title = document.createElement("div");
      title.className = "gc-drive-title";
      title.textContent = "Set up Google Drive (Developer Preview)";

      const account = document.createElement("p");
      account.className = "gc-drive-note";
      account.textContent = "Using your connected Google account: " + email + ".";

      const scope = document.createElement("p");
      scope.className = "gc-drive-note";
      scope.textContent = DRIVE_SCOPE_DISCLOSURE;

      const help = document.createElement("p");
      help.className = "gc-drive-note";
      help.innerHTML =
        'Enter your own Google Cloud <strong>Web</strong> OAuth client (self-hosted, one-time). ' +
        'Ori stores it only in your vault and never ships it. ' +
        '<a href="' + DRIVE_DOCS_URL + '" target="_blank" rel="noopener noreferrer">Setup guide ↗</a>';

      const idField = this.driveField("Web client ID", "drive-client-id");
      const secretField = this.driveField("Web client secret", "drive-client-secret", "password");

      const err = document.createElement("div");
      err.className = "gc-error d-none";
      err.setAttribute("data-drive-error", "");
      err.setAttribute("role", "alert");

      const actions = document.createElement("div");
      actions.className = "gc-drive-actions";
      const connectBtn = document.createElement("button");
      connectBtn.type = "button";
      connectBtn.className = "modern-btn modern-btn-primary";
      connectBtn.textContent = "Connect Google Drive";
      connectBtn.setAttribute("data-drive-connect", "");
      connectBtn.addEventListener("click", () => this.setupDrive());
      const cancelBtn = document.createElement("button");
      cancelBtn.type = "button";
      cancelBtn.className = "modern-btn modern-btn-secondary";
      cancelBtn.textContent = "Cancel";
      cancelBtn.addEventListener("click", () => this.hideDriveSetup());
      actions.appendChild(connectBtn);
      actions.appendChild(cancelBtn);

      host.append(title, account, scope, help, idField, secretField, err, actions);
    }

    driveField(label, key, type) {
      const wrap = document.createElement("label");
      wrap.className = "gc-drive-field";
      const span = document.createElement("span");
      span.textContent = label;
      const input = document.createElement("input");
      input.type = type || "text";
      input.className = "modern-input";
      input.setAttribute("data-" + key, "");
      input.autocomplete = "off";
      input.spellcheck = false;
      wrap.appendChild(span);
      wrap.appendChild(input);
      return wrap;
    }

    showDriveError(msg) {
      const host = this.el.driveSetup;
      if (!host) return;
      const err = host.querySelector("[data-drive-error]");
      if (err) {
        err.textContent = msg;
        err.classList.remove("d-none");
      }
    }

    async setupDrive() {
      const host = this.el.driveSetup;
      if (!host) return;
      const idInput = host.querySelector("[data-drive-client-id]");
      const secretInput = host.querySelector("[data-drive-client-secret]");
      const connectBtn = host.querySelector("[data-drive-connect]");
      const clientId = (idInput && idInput.value.trim()) || "";
      const clientSecret = (secretInput && secretInput.value.trim()) || "";
      const err = host.querySelector("[data-drive-error]");
      if (err) err.classList.add("d-none");

      if (!clientId || !clientSecret) {
        this.showDriveError("Enter your Web client ID and secret.");
        return;
      }
      if (connectBtn) connectBtn.disabled = true;
      try {
        // 1. Ensure the drivemcp server exists in the global MCP registry.
        //    Idempotent: a pre-existing server just fails to re-add; connect still
        //    proceeds against it.
        await fetch("/api/mcp/servers", {
          method: "POST",
          headers: { "Content-Type": "application/json" },
          body: JSON.stringify({
            name: DRIVE_SERVER_NAME,
            transport: "streamable_http",
            url: DRIVE_MCP_URL,
            enabled: true,
          }),
        }).catch(() => {});

        // 2. Persist the operator Web client to the vault and start OAuth.
        const res = await fetch(
          "/api/mcp/servers/" + encodeURIComponent(DRIVE_SERVER_NAME) + "/connect",
          {
            method: "POST",
            headers: { "Content-Type": "application/json", Accept: "application/json" },
            body: JSON.stringify({ client_id: clientId, client_secret: clientSecret }),
          },
        );
        const data = await res.json().catch(() => ({}));
        if (data.status === "running") {
          // Already authorized (silent refresh) — nothing more to do.
          await this.refresh();
          return;
        }
        if (!data.authorize_url) {
          this.showDriveError(
            data.error === "credentials_required"
              ? "Google rejected those credentials. Check the client ID and secret."
              : "Couldn't start Drive authorization. Please try again.",
          );
          return;
        }
        this.openDriveOAuthPopup(data.authorize_url);
      } catch (e) {
        this.showDriveError("Couldn't set up Drive. Please try again.");
      } finally {
        if (connectBtn) connectBtn.disabled = false;
      }
    }

    openDriveOAuthPopup(url) {
      const popup = window.open(url, "ori-drive-oauth", "width=520,height=680");
      const onMessage = (ev) => {
        if (ev.origin !== window.location.origin) return;
        const d = ev.data;
        if (!d || d.type !== MCP_OAUTH_EVENT) return;
        window.removeEventListener("message", onMessage);
        if (d.success) {
          this.refresh();
        } else {
          this.showDriveError(d.message || "Drive connection was canceled.");
        }
      };
      window.addEventListener("message", onMessage);
      if (!popup) {
        // Popup blocked — fall back to a full-page redirect.
        window.removeEventListener("message", onMessage);
        window.location.assign(url);
      }
    }

    // --- Disconnect / switch with impact preview ------------------------------

    async confirmProductDisconnect(product) {
      const label = PRODUCT_LABELS[product] || product;
      const impacts = await this.fetchImpact(product);
      this.renderConfirm({
        title: "Disconnect " + label + "?",
        body:
          "This removes " + label + " access from this device. Your Google account and other products stay connected.",
        impacts,
        confirmLabel: "Disconnect " + label,
        onConfirm: () =>
          this.doDisconnect("/api/connections/google/product/disconnect?product=" + encodeURIComponent(product)),
      });
    }

    async confirmAccountDisconnect() {
      const impacts = await this.fetchImpact(null);
      this.renderConfirm({
        title: "Disconnect Google?",
        body:
          'This removes all Google access (Gmail, Calendar, Drive) from this device. Workspaces keep their setup and show "Connection required" until you reconnect.',
        impacts,
        confirmLabel: "Disconnect Google",
        onConfirm: () => this.doDisconnect("/api/connections/google/disconnect"),
      });
    }

    async switchAccount() {
      // Switching requires disconnecting the current account first (FR 83);
      // removed workspace links are not auto-restored, so preview the impact.
      const impacts = await this.fetchImpact(null);
      this.renderConfirm({
        title: "Switch Google account?",
        body:
          "To switch accounts you must disconnect the current one first. Removed workspace links are NOT restored automatically — even if you reconnect the same account.",
        impacts,
        confirmLabel: "Disconnect to switch",
        onConfirm: () => this.doDisconnect("/api/connections/google/disconnect"),
      });
    }

    async fetchImpact(product) {
      try {
        const url =
          "/api/connections/google/impact" + (product ? "?product=" + encodeURIComponent(product) : "");
        const res = await fetch(url, { headers: { Accept: "application/json" } });
        if (!res.ok) return [];
        const data = await res.json().catch(() => ({}));
        return (data.products || []).filter((p) => (p.workspaces || []).length > 0);
      } catch (e) {
        return [];
      }
    }

    renderConfirm(opts) {
      const host = this.el.confirm;
      if (!host) return;
      this.hideDriveSetup();
      host.innerHTML = "";

      const title = document.createElement("div");
      title.className = "gc-confirm-title";
      title.textContent = opts.title;

      const body = document.createElement("p");
      body.className = "gc-drive-note";
      body.textContent = opts.body;
      host.append(title, body);

      const affected = (opts.impacts || []).flatMap((p) =>
        (p.workspaces || []).map((ws) => (PRODUCT_LABELS[p.product] || p.product) + " — " + (ws.name || ws.id)),
      );
      if (affected.length) {
        const lead = document.createElement("p");
        lead.className = "gc-drive-note";
        lead.textContent = "Workspaces that will lose access:";
        const ul = document.createElement("ul");
        ul.className = "gc-confirm-list";
        affected.forEach((line) => {
          const li = document.createElement("li");
          li.textContent = line;
          ul.appendChild(li);
        });
        host.append(lead, ul);
      }

      const actions = document.createElement("div");
      actions.className = "gc-confirm-actions";
      const confirmBtn = document.createElement("button");
      confirmBtn.type = "button";
      confirmBtn.className = "modern-btn modern-btn-danger";
      confirmBtn.textContent = opts.confirmLabel;
      confirmBtn.addEventListener("click", () => opts.onConfirm());
      const cancelBtn = document.createElement("button");
      cancelBtn.type = "button";
      cancelBtn.className = "modern-btn modern-btn-secondary";
      cancelBtn.textContent = "Cancel";
      cancelBtn.addEventListener("click", () => this.hideConfirm());
      actions.append(confirmBtn, cancelBtn);
      host.appendChild(actions);

      host.classList.remove("d-none");
      host.scrollIntoView({ block: "nearest" });
    }

    hideConfirm() {
      if (this.el.confirm) this.el.confirm.classList.add("d-none");
    }

    // --- Migrate legacy Gmail setup -------------------------------------------

    async refreshMigratable() {
      const host = this.el.migrate;
      if (!host) return;
      try {
        const res = await fetch("/api/connections/google/migratable", { headers: { Accept: "application/json" } });
        if (!res.ok) return this.hideMigrate();
        const data = await res.json().catch(() => ({}));
        const matches = (data.accounts || []).filter((a) => a.email_matches);
        if (!matches.length) return this.hideMigrate();
        this.renderMigrate(matches);
      } catch (e) {
        this.hideMigrate();
      }
    }

    renderMigrate(accounts) {
      const host = this.el.migrate;
      if (!host) return;
      host.innerHTML = "";
      const title = document.createElement("div");
      title.className = "gc-migrate-title";
      title.textContent = "Move your existing email setup to this account";
      const note = document.createElement("p");
      note.className = "gc-drive-note";
      note.textContent =
        "You have Gmail set up the older per-workspace way for this same account. Move it onto your connected Google account — no re-authorization.";
      host.append(title, note);
      accounts.forEach((a) => {
        const row = document.createElement("div");
        row.className = "gc-migrate-row";
        const label = document.createElement("span");
        label.textContent = a.email + (a.workspace_id ? " · workspace" : "");
        const btn = document.createElement("button");
        btn.type = "button";
        btn.className = "modern-btn modern-btn-secondary gc-enable-btn";
        btn.textContent = "Migrate";
        btn.setAttribute("aria-label", "Migrate " + a.email);
        btn.addEventListener("click", () => this.migrateAccount(a.id, btn));
        row.append(label, btn);
        host.appendChild(row);
      });
      host.classList.remove("d-none");
    }

    hideMigrate() {
      if (this.el.migrate) this.el.migrate.classList.add("d-none");
    }

    reconnectProduct(product) {
      this.hideError();
      if (product === "drive") {
        this.toggleDriveSetup(); // re-run the Drive Web-client authorization
        return;
      }
      if (product === "gmail") {
        this.enableGmail(null, null); // re-run Gmail authorization
        return;
      }
      // Calendar's connector is set up in its Calendar Ops workspace.
      this.showError("Reconnect Calendar from its Calendar Ops workspace setup.");
    }

    async migrateAccount(accountID, btn) {
      this.hideError();
      if (btn) btn.disabled = true;
      try {
        const res = await fetch(
          "/api/connections/google/migrate?account_id=" + encodeURIComponent(accountID),
          { method: "POST", headers: { Accept: "application/json" } },
        );
        const data = await res.json().catch(() => ({}));
        if (res.status === 409) {
          this.showError(data.message || "That account couldn't be migrated.");
          return;
        }
        if (!res.ok) throw new Error("migrate failed");
        if (data.status === "skipped") {
          // Not a failure: Ori kept a record it couldn't prove was redundant.
          // Say so plainly — silently reporting success would be a lie, and
          // reporting an error would suggest something broke.
          this.showNotice(
            data.message ||
              "Ori couldn't confirm this account is a duplicate, so it was left in place. Nothing was deleted.",
          );
        }
        await this.refresh();
      } catch (e) {
        this.showError("Couldn't migrate the account. Please try again.");
      } finally {
        if (btn) btn.disabled = false;
      }
    }

    async doDisconnect(url) {
      this.hideConfirm();
      this.el.status.classList.remove("d-none");
      this.showStatusText("Updating Google connection…");
      try {
        const res = await fetch(url, { method: "POST", headers: { Accept: "application/json" } });
        if (!res.ok) throw new Error("request failed");
      } catch (e) {
        this.showError("Couldn't update the connection. Please try again.");
      }
      await this.refresh();
    }
  }

  const mgr = new GoogleConnectionManager();
  if (document.readyState === "loading") {
    document.addEventListener("DOMContentLoaded", () => mgr.init());
  } else {
    mgr.init();
  }
  window.googleConnectionManager = mgr;
})();

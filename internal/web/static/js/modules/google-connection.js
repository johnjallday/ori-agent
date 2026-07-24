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
        driveSetup: id("googleConnDriveSetup"),
        connectBtn: id("googleConnConnectBtn"),
        disconnectBtn: id("googleConnDisconnectBtn"),
        error: id("googleConnError"),
      };
    }

    bind() {
      if (this.el.connectBtn) this.el.connectBtn.addEventListener("click", () => this.connect());
      if (this.el.disconnectBtn) this.el.disconnectBtn.addEventListener("click", () => this.disconnect());
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
      } else {
        this.el.connected.classList.add("d-none");
        this.el.disconnected.classList.remove("d-none");
        this.hideDriveSetup();
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
          btn.addEventListener("click", () => this.toggleDriveSetup());
          right.appendChild(btn);
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

    async enableGmail(btn, scope) {
      this.hideError();
      if (btn) btn.disabled = true;
      try {
        const url =
          scope === "send"
            ? "/api/connections/google/gmail/enable?scope=send"
            : "/api/connections/google/gmail/enable";
        const res = await fetch(url, { method: "POST", headers: { Accept: "application/json" } });
        const data = await res.json().catch(() => ({}));
        if ((res.status === 409 || res.status === 503) && data.message) {
          this.showError(data.message);
          return;
        }
        if (!res.ok || !data.authorize_url) throw new Error("enable failed");
        window.location.assign(data.authorize_url);
      } catch (e) {
        this.showError("Couldn't start Gmail access. Please try again.");
      } finally {
        if (btn) btn.disabled = false;
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

    async disconnect() {
      this.el.disconnectBtn.disabled = true;
      try {
        const res = await fetch("/api/connections/google/disconnect", { method: "POST", headers: { Accept: "application/json" } });
        if (!res.ok) throw new Error("disconnect failed");
        this.el.status.classList.remove("d-none");
        this.showStatusText("Checking Google connection…");
        await this.refresh();
      } catch (e) {
        this.showError("Couldn't disconnect. Please try again.");
      } finally {
        this.el.disconnectBtn.disabled = false;
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
      if (this.el.error) this.el.error.classList.add("d-none");
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
  }

  const mgr = new GoogleConnectionManager();
  if (document.readyState === "loading") {
    document.addEventListener("DOMContentLoaded", () => mgr.init());
  } else {
    mgr.init();
  }
  window.googleConnectionManager = mgr;
})();

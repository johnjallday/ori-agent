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
      } else {
        this.el.connected.classList.add("d-none");
        this.el.disconnected.classList.remove("d-none");
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

        const pill = document.createElement("span");
        pill.className = "gc-pill";
        pill.setAttribute("data-health", g.health || "not_enabled");
        pill.textContent = HEALTH_LABELS[g.health] || (g.enabled ? "Enabled" : "Not enabled");

        row.appendChild(name);
        row.appendChild(pill);
        this.el.products.appendChild(row);
      });
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
  }

  const mgr = new GoogleConnectionManager();
  if (document.readyState === "loading") {
    document.addEventListener("DOMContentLoaded", () => mgr.init());
  } else {
    mgr.init();
  }
  window.googleConnectionManager = mgr;
})();

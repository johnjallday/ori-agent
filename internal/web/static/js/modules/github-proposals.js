/**
 * GitHub changes awaiting approval — workspace panel.
 *
 * Renders proposals from /api/workspaces/{id}/github/proposals and approves or
 * rejects them.
 *
 * The panel shows each change's LITERAL content: the exact comment text, the
 * exact label names, the exact state change. Approving a summary would not be
 * informed consent, so the summary is only ever a heading above the real thing.
 *
 * Approval sends the proposal's hash back. That is what binds the click to the
 * content that was on screen — if the proposal changed since it rendered, the
 * server refuses and the user re-reads it rather than approving something they
 * never saw.
 */
(function () {
  'use strict';

  const STATUS_LABELS = {
    draft: 'Awaiting your approval',
    executing: 'Applying…',
    applied: 'Applied',
    failed: 'Failed',
    rejected: 'Rejected',
    expired: 'Expired'
  };

  class GitHubProposalsPanel {
    constructor(mount) {
      this.mount = mount;
      this.workspaceId = '';
      this.busy = new Set();
    }

    init() {
      const resolved = window.currentWorkspaceId || document.body?.dataset?.workspaceId || '';
      if (resolved) {
        this.workspaceId = String(resolved);
      } else {
        // Legacy/test fallback. Production workspace routes carry a slug here.
        const match = window.location.pathname.match(/\/workspaces\/([^/?#]+)/);
        if (!match) return;
        this.workspaceId = decodeURIComponent(match[1]);
      }
      this.refresh();
    }

    async request(path, options) {
      const response = await fetch(path, {
        headers: { 'Content-Type': 'application/json' },
        ...options
      });
      let payload = {};
      try {
        payload = await response.json();
      } catch {
        payload = {};
      }
      return { ok: response.ok, payload };
    }

    async refresh() {
      const { ok, payload } = await this.request(
        `/api/workspaces/${encodeURIComponent(this.workspaceId)}/github/proposals`,
        { method: 'GET' }
      );
      if (!ok || !Array.isArray(payload.proposals)) {
        this.mount.hidden = true;
        return;
      }
      this.render(payload.proposals);
    }

    render(proposals) {
      // A workspace with nothing pending shows nothing at all, rather than an
      // empty panel that implies something is missing.
      const pending = proposals.filter(p => p.status === 'draft' || p.status === 'failed');
      const recent = proposals.filter(p => p.status === 'applied').slice(0, 3);
      if (pending.length === 0 && recent.length === 0) {
        this.mount.hidden = true;
        this.mount.innerHTML = '';
        return;
      }

      this.mount.hidden = false;
      this.mount.innerHTML = '';
      this.mount.appendChild(this.buildCard(pending, recent));
    }

    buildCard(pending, recent) {
      const card = el('div', 'ghp-card');

      const header = el('div', 'ghp-header');
      header.appendChild(el('h3', 'ghp-title', 'GitHub changes'));
      if (pending.length > 0) {
        header.appendChild(el('span', 'ghp-badge', `${pending.length} awaiting approval`));
      }
      card.appendChild(header);

      const note = el(
        'p',
        'ghp-note',
        'Nothing below has been sent to GitHub. Each change is applied only when you approve it.'
      );
      card.appendChild(note);

      pending.forEach(p => card.appendChild(this.buildProposal(p)));
      recent.forEach(p => card.appendChild(this.buildApplied(p)));
      return card;
    }

    buildProposal(proposal) {
      const row = el('div', 'ghp-proposal');
      row.dataset.proposalId = proposal.id;

      const head = el('div', 'ghp-proposal-head');
      head.appendChild(el('span', 'ghp-summary', proposal.summary || 'Proposed change'));
      head.appendChild(el('span', 'ghp-status', STATUS_LABELS[proposal.status] || proposal.status));
      row.appendChild(head);

      if (proposal.change && proposal.change.rationale) {
        row.appendChild(el('p', 'ghp-rationale', proposal.change.rationale));
      }

      row.appendChild(this.buildContent(proposal.change || {}));

      if (proposal.status === 'failed' && proposal.last_error) {
        row.appendChild(el('p', 'ghp-error', proposal.last_error));
      }

      const actions = el('div', 'ghp-actions');
      const approve = el(
        'button',
        'modern-btn modern-btn-primary ghp-approve',
        'Approve and apply'
      );
      approve.type = 'button';
      approve.addEventListener('click', () => this.approve(proposal));
      const reject = el('button', 'modern-btn modern-btn-secondary', 'Reject');
      reject.type = 'button';
      reject.addEventListener('click', () => this.reject(proposal));
      actions.append(approve, reject);
      row.appendChild(actions);

      return row;
    }

    // buildContent renders what will actually reach GitHub, verbatim. The
    // comment body goes in a <pre> via textContent so its formatting survives
    // and nothing in it can be interpreted as markup.
    buildContent(change) {
      const box = el('div', 'ghp-content');
      switch (change.kind) {
        case 'comment': {
          box.appendChild(el('div', 'ghp-content-label', 'This comment will be posted:'));
          const pre = el('pre', 'ghp-body');
          pre.textContent = change.body || '';
          box.appendChild(pre);
          break;
        }
        case 'labels': {
          if (Array.isArray(change.add_labels) && change.add_labels.length) {
            box.appendChild(el('div', 'ghp-content-label', 'Labels to add:'));
            box.appendChild(this.buildLabels(change.add_labels, 'add'));
          }
          if (Array.isArray(change.remove_labels) && change.remove_labels.length) {
            box.appendChild(el('div', 'ghp-content-label', 'Labels to remove:'));
            box.appendChild(this.buildLabels(change.remove_labels, 'remove'));
          }
          break;
        }
        case 'state': {
          const verb = change.state === 'closed' ? 'closed' : 'reopened';
          const reason = change.state_reason ? ` (${change.state_reason.replace('_', ' ')})` : '';
          box.appendChild(
            el('div', 'ghp-content-label', `Issue #${change.issue} will be ${verb}${reason}.`)
          );
          break;
        }
        default:
          break;
      }
      const target = el('div', 'ghp-target', `${change.repo || ''} · issue #${change.issue || ''}`);
      box.appendChild(target);
      return box;
    }

    buildLabels(names, mode) {
      const wrap = el('div', 'ghp-labels');
      names.forEach(name => {
        const chip = el('span', `ghp-label ghp-label-${mode}`, name);
        wrap.appendChild(chip);
      });
      return wrap;
    }

    buildApplied(proposal) {
      const row = el('div', 'ghp-proposal ghp-applied');
      const head = el('div', 'ghp-proposal-head');
      head.appendChild(el('span', 'ghp-summary', proposal.summary || 'Change'));
      head.appendChild(el('span', 'ghp-status ghp-status-applied', 'Applied'));
      row.appendChild(head);
      if (proposal.applied_url) {
        const link = el('a', 'ghp-link', 'View on GitHub ↗');
        link.href = proposal.applied_url;
        link.target = '_blank';
        link.rel = 'noopener noreferrer';
        row.appendChild(link);
      }
      return row;
    }

    async approve(proposal) {
      if (this.busy.has(proposal.id)) return;
      this.busy.add(proposal.id);
      this.setRowBusy(proposal.id, true);

      const { ok, payload } = await this.request(
        `/api/workspaces/${encodeURIComponent(this.workspaceId)}/github/proposals/${encodeURIComponent(proposal.id)}/confirm`,
        {
          method: 'POST',
          // The hash binds this approval to the content that was rendered. If
          // the proposal changed since, the server refuses rather than
          // applying something the user did not read.
          body: JSON.stringify({ expected_hash: proposal.hash })
        }
      );

      this.busy.delete(proposal.id);
      if (!ok) {
        this.showRowError(proposal.id, payload.message || 'That change could not be applied.');
        this.setRowBusy(proposal.id, false);
        return;
      }
      this.refresh();
    }

    async reject(proposal) {
      if (this.busy.has(proposal.id)) return;
      this.busy.add(proposal.id);
      this.setRowBusy(proposal.id, true);

      const { ok, payload } = await this.request(
        `/api/workspaces/${encodeURIComponent(this.workspaceId)}/github/proposals/${encodeURIComponent(proposal.id)}/reject`,
        { method: 'POST' }
      );

      this.busy.delete(proposal.id);
      if (!ok) {
        this.showRowError(proposal.id, payload.message || 'That change could not be rejected.');
        this.setRowBusy(proposal.id, false);
        return;
      }
      this.refresh();
    }

    row(proposalId) {
      return this.mount.querySelector(`[data-proposal-id="${CSS.escape(proposalId)}"]`);
    }

    setRowBusy(proposalId, busy) {
      const row = this.row(proposalId);
      if (!row) return;
      row.querySelectorAll('button').forEach(btn => {
        btn.disabled = busy;
      });
    }

    showRowError(proposalId, message) {
      const row = this.row(proposalId);
      if (!row) return;
      let error = row.querySelector('.ghp-error');
      if (!error) {
        error = el('p', 'ghp-error');
        row.insertBefore(error, row.querySelector('.ghp-actions'));
      }
      error.textContent = message;
    }
  }

  function el(tag, className, text) {
    const node = document.createElement(tag);
    if (className) node.className = className;
    if (text !== undefined) node.textContent = text;
    return node;
  }

  function start() {
    const mount = document.getElementById('githubProposalsMount');
    if (!mount) return;
    const panel = new GitHubProposalsPanel(mount);
    // Exposed so module tests can drive the renderer directly, the same way
    // file-janitor-console.js exposes its panel.
    window.GitHubProposalsPanel = panel;
    panel.init();
  }

  if (document.readyState === 'loading') {
    document.addEventListener('DOMContentLoaded', start);
  } else {
    start();
  }
})();

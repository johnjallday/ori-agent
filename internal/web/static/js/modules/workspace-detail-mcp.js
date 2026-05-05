/**
 * Workspace MCP Manager
 *
 * Owns workspace-scoped MCP binding management: loading the global connector
 * catalog, the binding modal (add/edit/customize/delete), agent-access matrix,
 * email-account integration for email-type connectors, and rendering the
 * binding cards in the workspace detail page. Also serves as the source of
 * truth for "effective" bindings/server-names per agent (used by agent
 * rendering elsewhere on the page).
 *
 * Extracted from workspace-detail.js. Instantiated by WorkspaceDetailPage,
 * which provides workspace data, workspaceId, escapeHtml, normalizeAgentName,
 * getAgentProfile, getAgentInstanceIdsForName, loadWorkspace,
 * renderWorkspaceConfigSummary, and DOM element refs through the host.
 *
 * @module workspace-detail-mcp
 */

export class WorkspaceMCPManager {
  constructor(host) {
    this.host = host;
    this.availableMCPServers = [];
    this.availableMCPServersPromise = null;
    this.availableEmailAccounts = [];
    this.availableEmailAccountsPromise = null;
    this.activeWorkspaceMCPBindingId = '';
    this.activeWorkspaceMCPMode = 'create';
  }

  bindEvents() {
    const elements = this.host.elements;
    elements.mcpList?.addEventListener('click', event => this.handleWorkspaceMCPListClick(event));
    elements.mcpForm?.addEventListener('submit', event => {
      event.preventDefault();
      this.submitWorkspaceMCPModal();
    });
    elements.mcpServerSelect?.addEventListener('change', () => {
      this.handleWorkspaceMCPServerChange();
    });
    [
      elements.mcpEmailActionRead,
      elements.mcpEmailActionSearch,
      elements.mcpEmailActionDraft,
      elements.mcpEmailActionSend
    ].forEach(checkbox => {
      checkbox?.addEventListener('change', () => this.handleWorkspaceMCPEmailActionChange());
    });
    elements.mcpEmailAccountSelect?.addEventListener('change', () =>
      this.updateWorkspaceMCPEmailAccountSummary()
    );
    elements.mcpAgentOptions?.addEventListener('change', () =>
      this.updateWorkspaceMCPAgentAccessSummary()
    );
    elements.mcpModal?.addEventListener('hidden.bs.modal', () => this.resetWorkspaceMCPModal());
  }

  normalizeWorkspaceMCPBinding(binding, source = 'workspace') {
    const emailAccount =
      binding?.email_account && typeof binding.email_account === 'object'
        ? { ...binding.email_account }
        : null;

    return {
      id: String(binding?.id || '').trim(),
      serverName: String(binding?.server_name || binding?.serverName || '').trim(),
      alias: String(binding?.alias || '').trim(),
      enabled: binding?.enabled !== false,
      scope: binding?.scope && typeof binding.scope === 'object' ? { ...binding.scope } : {},
      config: binding?.config && typeof binding.config === 'object' ? { ...binding.config } : {},
      emailAccount,
      emailAccountMissing:
        binding?.email_account_missing === true || binding?.emailAccountMissing === true,
      source
    };
  }

  getExplicitWorkspaceMCPBindings() {
    if (!this.host.workspace || !Array.isArray(this.host.workspace.mcp_bindings)) {
      return [];
    }

    return this.host.workspace.mcp_bindings
      .map(binding => this.normalizeWorkspaceMCPBinding(binding, 'workspace'))
      .filter(binding => binding.id && binding.serverName);
  }

  getWorkspaceMCPBindings(options = {}) {
    if (!this.host.workspace) return [];

    const includeDisabled = options.includeDisabled === true;
    const explicitBindings = this.getExplicitWorkspaceMCPBindings();
    const explicitFilesystemExists = explicitBindings.some(
      binding => binding.serverName.toLowerCase() === 'filesystem'
    );

    const visibleExplicitBindings = includeDisabled
      ? explicitBindings
      : explicitBindings.filter(binding => binding.enabled);

    const directoryRoots = Array.isArray(this.host.workspace.directory_references)
      ? this.host.workspace.directory_references
          .map(reference => String(reference?.path || '').trim())
          .filter(Boolean)
      : [];

    if (explicitFilesystemExists || directoryRoots.length === 0) {
      return visibleExplicitBindings;
    }

    return [
      ...visibleExplicitBindings,
      {
        id: 'workspace-filesystem',
        serverName: 'filesystem',
        alias: 'workspace_filesystem',
        enabled: true,
        scope: { roots: directoryRoots },
        source: 'synthesized'
      }
    ];
  }

  getWorkspaceExplicitMCPBinding(bindingId) {
    const normalizedBindingId = String(bindingId || '')
      .trim()
      .toLowerCase();
    if (!normalizedBindingId) return null;
    return (
      this.getExplicitWorkspaceMCPBindings().find(
        binding =>
          String(binding?.id || '')
            .trim()
            .toLowerCase() === normalizedBindingId
      ) || null
    );
  }

  getWorkspaceMCPBinding(bindingId, options = {}) {
    const normalizedBindingId = String(bindingId || '')
      .trim()
      .toLowerCase();
    if (!normalizedBindingId) return null;
    return (
      this.getWorkspaceMCPBindings(options).find(
        binding =>
          String(binding?.id || '')
            .trim()
            .toLowerCase() === normalizedBindingId
      ) || null
    );
  }

  isWorkspaceRuntimeMCPServerName(serverName) {
    return String(serverName || '')
      .trim()
      .toLowerCase()
      .startsWith('ws:');
  }

  async loadAvailableMCPServers(force = false) {
    if (!force && Array.isArray(this.availableMCPServers) && this.availableMCPServers.length > 0) {
      return this.availableMCPServers;
    }
    if (!force && this.availableMCPServersPromise) {
      return this.availableMCPServersPromise;
    }

    this.availableMCPServersPromise = (async () => {
      const response = await fetch('/api/mcp/servers');
      if (!response.ok) {
        const text = await response.text();
        throw new Error(text || 'Failed to load MCP connectors');
      }

      const data = await response.json();
      const seen = new Set();
      const servers = (Array.isArray(data?.servers) ? data.servers : [])
        .map(server => ({
          name: String(server?.name || '').trim(),
          enabled: server?.enabled !== false
        }))
        .filter(
          server =>
            server.name && server.enabled && !this.isWorkspaceRuntimeMCPServerName(server.name)
        )
        .filter(server => {
          const key = server.name.toLowerCase();
          if (seen.has(key)) return false;
          seen.add(key);
          return true;
        })
        .sort((left, right) => left.name.localeCompare(right.name));

      this.availableMCPServers = servers;
      return servers;
    })();

    try {
      return await this.availableMCPServersPromise;
    } finally {
      this.availableMCPServersPromise = null;
    }
  }

  isEmailWorkspaceMCPServerName(serverName) {
    switch (
      String(serverName || '')
        .trim()
        .toLowerCase()
    ) {
      case 'email':
      case 'gmail':
      case 'microsoft-mail':
      case 'microsoft':
      case 'outlook-mail':
      case 'imap-smtp':
      case 'imap_smtp':
        return true;
      default:
        return false;
    }
  }

  normalizeWorkspaceEmailAccount(account) {
    if (!account || typeof account !== 'object') {
      return null;
    }

    return {
      id: String(account.id || '').trim(),
      vaultId: String(account.vault_id || account.vaultId || '').trim(),
      workspaceId: String(account.workspace_id || account.workspaceId || '').trim(),
      label: String(account.label || '').trim(),
      provider: String(account.provider || '').trim(),
      emailAddress: String(account.email_address || account.emailAddress || '').trim(),
      displayName: String(account.display_name || account.displayName || '').trim(),
      authType: String(account.auth_type || account.authType || '').trim(),
      credentials:
        account.credentials && typeof account.credentials === 'object'
          ? { ...account.credentials }
          : account.credentials_status && typeof account.credentials_status === 'object'
            ? { ...account.credentials_status }
            : {}
    };
  }

  async loadAvailableEmailAccounts(force = false) {
    if (
      !force &&
      Array.isArray(this.availableEmailAccounts) &&
      this.availableEmailAccounts.length > 0
    ) {
      return this.availableEmailAccounts;
    }
    if (!force && this.availableEmailAccountsPromise) {
      return this.availableEmailAccountsPromise;
    }

    this.availableEmailAccountsPromise = (async () => {
      const response = await fetch('/api/vault/email-accounts');
      if (!response.ok) {
        const text = await response.text();
        throw new Error(text || 'Failed to load email accounts');
      }

      const data = await response.json();
      const seen = new Set();
      const accounts = (Array.isArray(data?.accounts) ? data.accounts : [])
        .map(account => this.normalizeWorkspaceEmailAccount(account))
        .filter(account => account && account.id)
        .filter(account => {
          const key = account.id.toLowerCase();
          if (seen.has(key)) return false;
          seen.add(key);
          return true;
        })
        .sort((left, right) => {
          const leftLabel = left.label || left.emailAddress || left.id;
          const rightLabel = right.label || right.emailAddress || right.id;
          return leftLabel.localeCompare(rightLabel);
        });

      this.availableEmailAccounts = accounts;
      return accounts;
    })();

    try {
      return await this.availableEmailAccountsPromise;
    } finally {
      this.availableEmailAccountsPromise = null;
    }
  }

  getVisibleWorkspaceEmailAccounts(existingBinding = null) {
    const workspaceID = String(this.host.workspaceId || '')
      .trim()
      .toLowerCase();
    const existingAccount = this.normalizeWorkspaceEmailAccount(existingBinding?.emailAccount);
    const existingAccountID = String(
      existingBinding?.config?.account_id || existingAccount?.id || ''
    )
      .trim()
      .toLowerCase();
    const accounts = Array.isArray(this.availableEmailAccounts)
      ? [...this.availableEmailAccounts]
      : [];
    const filtered = accounts.filter(account => {
      const accountWorkspaceID = String(account.workspaceId || '')
        .trim()
        .toLowerCase();
      return !accountWorkspaceID || accountWorkspaceID === workspaceID;
    });

    if (
      existingAccount &&
      existingAccount.id &&
      !filtered.some(account => account.id.toLowerCase() === existingAccount.id.toLowerCase())
    ) {
      filtered.unshift(existingAccount);
    } else if (
      existingAccountID &&
      !filtered.some(account => account.id.toLowerCase() === existingAccountID)
    ) {
      filtered.unshift({
        id: String(existingBinding?.config?.account_id || '').trim(),
        label: String(existingBinding?.config?.account_id || 'Unavailable email account'),
        provider: '',
        emailAddress: '',
        authType: '',
        workspaceId: ''
      });
    }

    return filtered;
  }

  setWorkspaceMCPEmailAccountHelp(message, isError = false) {
    if (!this.host.elements.mcpEmailAccountHelp) return;
    this.host.elements.mcpEmailAccountHelp.textContent = message;
    this.host.elements.mcpEmailAccountHelp.classList.toggle('is-error', !!isError);
  }

  populateWorkspaceMCPEmailAccountOptions(selectedAccountID = '', existingBinding = null) {
    if (!this.host.elements.mcpEmailAccountSelect) return;

    const normalizedSelected = String(selectedAccountID || '').trim();
    const accounts = this.getVisibleWorkspaceEmailAccounts(existingBinding);
    const options = ['<option value="">Select an email account</option>'];

    accounts.forEach(account => {
      const id = String(account?.id || '').trim();
      if (!id) return;

      const selected =
        normalizedSelected && id.toLowerCase() === normalizedSelected.toLowerCase()
          ? ' selected'
          : '';
      const accountLabel = account.label || account.emailAddress || id;
      const details = [account.provider, account.emailAddress].filter(Boolean).join(' • ');
      const text = details ? `${accountLabel} (${details})` : accountLabel;
      options.push(
        `<option value="${this.host.escapeHtml(id)}"${selected}>${this.host.escapeHtml(text)}</option>`
      );
    });

    this.host.elements.mcpEmailAccountSelect.innerHTML = options.join('');
    if (normalizedSelected) {
      this.host.elements.mcpEmailAccountSelect.value = normalizedSelected;
    }

    if (accounts.length === 0) {
      this.setWorkspaceMCPEmailAccountHelp(
        'No unlocked email accounts are available. Create one in Vault > Email Accounts.',
        true
      );
      return;
    }

    this.setWorkspaceMCPEmailAccountHelp(
      'Email accounts come from Vault > Email Accounts. Workspace-scoped accounts must match this workspace.',
      false
    );
  }

  getWorkspaceMCPSelectedEmailActions() {
    const mapping = [
      ['read', this.host.elements.mcpEmailActionRead],
      ['search', this.host.elements.mcpEmailActionSearch],
      ['draft', this.host.elements.mcpEmailActionDraft],
      ['send', this.host.elements.mcpEmailActionSend]
    ];

    return mapping.filter(([, element]) => element?.checked).map(([action]) => action);
  }

  setWorkspaceMCPEmailActions(actions = []) {
    const selected = new Set(
      (Array.isArray(actions) ? actions : [])
        .map(action =>
          String(action || '')
            .trim()
            .toLowerCase()
        )
        .filter(Boolean)
    );

    if (this.host.elements.mcpEmailActionRead)
      this.host.elements.mcpEmailActionRead.checked = selected.has('read');
    if (this.host.elements.mcpEmailActionSearch)
      this.host.elements.mcpEmailActionSearch.checked = selected.has('search');
    if (this.host.elements.mcpEmailActionDraft)
      this.host.elements.mcpEmailActionDraft.checked = selected.has('draft');
    if (this.host.elements.mcpEmailActionSend)
      this.host.elements.mcpEmailActionSend.checked = selected.has('send');
    this.handleWorkspaceMCPEmailActionChange();
  }

  handleWorkspaceMCPEmailActionChange() {
    const selected = this.getWorkspaceMCPSelectedEmailActions();
    const canSend = selected.includes('send');

    if (this.host.elements.mcpEmailSendConfirmWrap) {
      this.host.elements.mcpEmailSendConfirmWrap.classList.toggle('d-none', !canSend);
    }
    if (
      this.host.elements.mcpEmailSendConfirmInput &&
      canSend &&
      !this.host.elements.mcpEmailSendConfirmInput.checked
    ) {
      this.host.elements.mcpEmailSendConfirmInput.checked = true;
    }
  }

  updateWorkspaceMCPEmailAccountSummary(existingBinding = null) {
    if (!this.host.elements.mcpEmailAccountSummary) return;

    const selectedAccountID = String(this.host.elements.mcpEmailAccountSelect?.value || '').trim();
    const accounts = this.getVisibleWorkspaceEmailAccounts(existingBinding);
    const account =
      accounts.find(item => String(item?.id || '').trim() === selectedAccountID) ||
      this.normalizeWorkspaceEmailAccount(existingBinding?.emailAccount);

    if (!selectedAccountID && !account) {
      this.host.elements.mcpEmailAccountSummary.textContent =
        'Select an email account to review its provider, address, and stored credential status.';
      return;
    }

    if (!account) {
      this.host.elements.mcpEmailAccountSummary.textContent =
        'The saved email account is currently unavailable. Unlock the correct vault or choose another account.';
      return;
    }

    const credentialState = account.credentials || {};
    const stored = [];
    if (credentialState.has_refresh_token) stored.push('refresh token');
    if (credentialState.has_access_token) stored.push('access token');
    if (credentialState.has_password)
      stored.push(account.authType === 'app_password' ? 'app password' : 'password');
    if (credentialState.has_client_id) stored.push('client id');
    if (credentialState.has_client_secret) stored.push('client secret');

    const summary = [
      account.label || account.emailAddress || account.id,
      account.emailAddress,
      account.provider,
      account.authType
    ]
      .filter(Boolean)
      .join(' • ');

    this.host.elements.mcpEmailAccountSummary.textContent =
      stored.length > 0
        ? `${summary}. Stored in vault: ${stored.join(', ')}.`
        : `${summary}. No credential status is currently available.`;
  }

  async syncWorkspaceMCPEmailFields(existingBinding = null) {
    const serverName = String(this.host.elements.mcpServerSelect?.value || '').trim();
    const isEmailServer = this.isEmailWorkspaceMCPServerName(serverName);

    if (this.host.elements.mcpEmailFields) {
      this.host.elements.mcpEmailFields.classList.toggle('d-none', !isEmailServer);
    }
    if (this.host.elements.mcpConfigDetails) {
      this.host.elements.mcpConfigDetails.open = !isEmailServer;
    }

    if (!isEmailServer) {
      this.updateWorkspaceMCPEmailAccountSummary();
      return;
    }

    try {
      await this.loadAvailableEmailAccounts(true);
      this.populateWorkspaceMCPEmailAccountOptions(
        existingBinding?.config?.account_id || this.host.elements.mcpEmailAccountSelect?.value || '',
        existingBinding
      );
    } catch (error) {
      console.error('Failed to load email accounts for MCP modal:', error);
      this.availableEmailAccounts = [];
      this.populateWorkspaceMCPEmailAccountOptions(
        existingBinding?.config?.account_id || this.host.elements.mcpEmailAccountSelect?.value || '',
        existingBinding
      );
      this.setWorkspaceMCPEmailAccountHelp(error.message || 'Failed to load email accounts', true);
    }

    this.updateWorkspaceMCPEmailAccountSummary(existingBinding);
    this.handleWorkspaceMCPEmailActionChange();
  }

  getWorkspaceAgentAccessEntry(agentInstanceId) {
    const normalizedAgentInstanceId = String(agentInstanceId || '').trim();
    if (
      !normalizedAgentInstanceId ||
      !this.host.workspace ||
      !Array.isArray(this.host.workspace.agent_mcp_access)
    ) {
      return null;
    }

    return (
      this.host.workspace.agent_mcp_access.find(
        entry => String(entry?.agent_instance_id || '').trim() === normalizedAgentInstanceId
      ) || null
    );
  }

  getWorkspaceMCPAgentNamesForBinding(bindingId) {
    const normalizedBindingId = String(bindingId || '')
      .trim()
      .toLowerCase();
    if (!normalizedBindingId || !this.host.workspace || !Array.isArray(this.host.workspace.agent_instances)) {
      return [];
    }

    const accessEntries = Array.isArray(this.host.workspace.agent_mcp_access)
      ? this.host.workspace.agent_mcp_access
      : [];

    const names = [];
    const seen = new Set();
    this.host.workspace.agent_instances.forEach(instance => {
      const instanceId = String(instance?.id || '').trim();
      const agentName = String(instance?.name || '').trim();
      if (!instanceId || !agentName) return;

      const entry = accessEntries.find(
        item => String(item?.agent_instance_id || '').trim() === instanceId
      );
      let allowed = true;
      if (entry) {
        const enabledIDs = Array.isArray(entry.enabled_binding_ids)
          ? entry.enabled_binding_ids
              .map(value =>
                String(value || '')
                  .trim()
                  .toLowerCase()
              )
              .filter(Boolean)
          : [];
        allowed = enabledIDs.includes(normalizedBindingId);
      }
      if (!allowed) return;

      const key = this.host.normalizeAgentName(agentName);
      if (!key || seen.has(key)) return;
      seen.add(key);
      names.push(agentName);
    });
    return names;
  }

  getWorkspaceMCPAgentAccessSelections(bindingId) {
    if (!this.host.workspace || !Array.isArray(this.host.workspace.agent_instances)) {
      return [];
    }

    const normalizedBindingId = String(bindingId || '')
      .trim()
      .toLowerCase();
    return this.host.workspace.agent_instances
      .map(instance => {
        const instanceId = String(instance?.id || '').trim();
        const instanceName = String(instance?.name || '').trim();
        if (!instanceId || !instanceName) return null;

        const entry = this.getWorkspaceAgentAccessEntry(instanceId);
        const enabledBindingIds = Array.isArray(entry?.enabled_binding_ids)
          ? entry.enabled_binding_ids
              .map(value =>
                String(value || '')
                  .trim()
                  .toLowerCase()
              )
              .filter(Boolean)
          : [];

        const instanceNumber = Number(instance?.instance_number || 0);
        const nodeID = String(instance?.node_id || '').trim();
        const label = instanceNumber > 1 ? `${instanceName} #${instanceNumber}` : instanceName;
        const meta = nodeID || 'Workspace agent instance';
        const checked = entry ? enabledBindingIds.includes(normalizedBindingId) : true;

        return {
          id: instanceId,
          label,
          meta,
          checked
        };
      })
      .filter(Boolean);
  }

  summarizeWorkspaceMCPBindingScope(binding) {
    const serverName = String(binding?.serverName || '')
      .trim()
      .toLowerCase();
    const scope = binding?.scope && typeof binding.scope === 'object' ? binding.scope : {};

    if (serverName === 'filesystem') {
      const roots = Array.isArray(scope.roots)
        ? scope.roots.map(value => String(value || '').trim()).filter(Boolean)
        : [];
      if (roots.length === 0) return 'No roots configured';
      if (roots.length === 1) return `1 root: ${roots[0]}`;
      return `${roots.length} roots`;
    }

    const entries = Object.entries(scope).filter(
      ([key, value]) => String(key || '').trim() && value !== null && value !== undefined
    );
    if (entries.length === 0) return '';

    const [firstKey, firstValue] = entries[0];
    if (Array.isArray(firstValue)) {
      return `${firstKey}: ${firstValue.length} item${firstValue.length === 1 ? '' : 's'}`;
    }
    if (typeof firstValue === 'object') {
      return `${firstKey}: configured`;
    }
    return `${firstKey}: ${String(firstValue).trim()}`;
  }

  summarizeWorkspaceMCPBindingConfig(binding) {
    const config = binding?.config && typeof binding.config === 'object' ? binding.config : {};
    const serverName = String(binding?.serverName || '')
      .trim()
      .toLowerCase();

    if (this.isEmailWorkspaceMCPServerName(serverName)) {
      const actions = Array.isArray(config.allowed_actions)
        ? config.allowed_actions.map(action => String(action || '').trim()).filter(Boolean)
        : [];
      const mailboxes = Array.isArray(config.mailboxes)
        ? config.mailboxes.map(value => String(value || '').trim()).filter(Boolean)
        : [];
      const parts = [];
      if (actions.length > 0) {
        parts.push(actions.join(', '));
      }
      if (mailboxes.length > 0) {
        parts.push(mailboxes.length === 1 ? `1 mailbox` : `${mailboxes.length} mailboxes`);
      }
      if (parts.length > 0) {
        return parts.join(' • ');
      }
    }

    const entries = Object.entries(config).filter(
      ([key, value]) => String(key || '').trim() && value !== null && value !== undefined
    );
    if (entries.length === 0) return '';

    const [firstKey, firstValue] = entries[0];
    if (Array.isArray(firstValue)) {
      return `${firstKey}: ${firstValue.length} value${firstValue.length === 1 ? '' : 's'}`;
    }
    if (typeof firstValue === 'object') {
      return `${firstKey}: configured`;
    }
    return `${firstKey}: ${String(firstValue).trim()}`;
  }

  describeWorkspaceMCPBinding(binding) {
    const serverName = String(binding?.serverName || '')
      .trim()
      .toLowerCase();
    if (binding?.enabled === false) {
      return 'Saved on this workspace but currently disabled. Re-enable it to materialize at runtime for agent instances.';
    }
    if (this.isEmailWorkspaceMCPServerName(serverName) && binding?.emailAccountMissing) {
      return 'Workspace-scoped email access is configured here, but the referenced vault account is currently unavailable or still locked.';
    }
    if (this.isEmailWorkspaceMCPServerName(serverName)) {
      return 'Workspace-scoped email access backed by a vault account. Policy on this binding limits which mailbox actions agents may perform.';
    }
    if (serverName === 'filesystem' && binding?.source === 'synthesized') {
      return 'Derived from imported workspace directories so filesystem access follows this workspace automatically.';
    }
    if (serverName === 'filesystem') {
      return 'Workspace-scoped filesystem access. Agents only see the roots allowed by this workspace binding.';
    }
    return 'Explicit workspace MCP binding available to agent instances in this workspace.';
  }

  getWorkspaceMCPModalInstance() {
    if (!this.host.elements.mcpModal || typeof bootstrap === 'undefined' || !bootstrap.Modal) {
      return null;
    }

    return typeof bootstrap.Modal.getOrCreateInstance === 'function'
      ? bootstrap.Modal.getOrCreateInstance(this.host.elements.mcpModal)
      : bootstrap.Modal.getInstance(this.host.elements.mcpModal) ||
          new bootstrap.Modal(this.host.elements.mcpModal);
  }

  generateWorkspaceMCPBindingId() {
    if (window.crypto && typeof window.crypto.randomUUID === 'function') {
      return window.crypto.randomUUID();
    }
    return `mcp-${Date.now().toString(36)}-${Math.random().toString(36).slice(2, 8)}`;
  }

  slugifyWorkspaceMCPAlias(serverName) {
    return String(serverName || '')
      .trim()
      .toLowerCase()
      .replace(/[^a-z0-9]+/g, '_')
      .replace(/^_+|_+$/g, '');
  }

  setWorkspaceMCPServerHelp(message, isError = false) {
    if (!this.host.elements.mcpServerHelp) return;
    this.host.elements.mcpServerHelp.textContent = message;
    this.host.elements.mcpServerHelp.classList.toggle('is-error', !!isError);
  }

  populateWorkspaceMCPServerOptions(selectedServerName = '') {
    if (!this.host.elements.mcpServerSelect) return;

    const normalizedSelected = String(selectedServerName || '').trim();
    const availableServers = Array.isArray(this.availableMCPServers)
      ? [...this.availableMCPServers]
      : [];
    const selectedExists = normalizedSelected
      ? availableServers.some(
          server =>
            String(server?.name || '')
              .trim()
              .toLowerCase() === normalizedSelected.toLowerCase()
        )
      : false;

    if (normalizedSelected && !selectedExists) {
      availableServers.unshift({ name: normalizedSelected, unavailable: true });
    }

    const options = ['<option value="">Select a connector</option>'];
    availableServers.forEach(server => {
      const name = String(server?.name || '').trim();
      if (!name) return;

      const unavailable = server?.unavailable === true;
      const selected =
        normalizedSelected && name.toLowerCase() === normalizedSelected.toLowerCase()
          ? ' selected'
          : '';
      const label = unavailable ? `${name} (currently not globally enabled)` : name;
      options.push(
        `<option value="${this.host.escapeHtml(name)}"${selected}>${this.host.escapeHtml(label)}</option>`
      );
    });

    this.host.elements.mcpServerSelect.innerHTML = options.join('');

    if (normalizedSelected) {
      this.host.elements.mcpServerSelect.value = normalizedSelected;
    }

    if (availableServers.length === 0) {
      this.setWorkspaceMCPServerHelp(
        'No globally enabled connectors are available yet. Enable an MCP globally first.',
        true
      );
      return;
    }

    if (normalizedSelected && !selectedExists) {
      this.setWorkspaceMCPServerHelp(
        `${normalizedSelected} is not globally enabled right now, but you can still update or remove this workspace binding.`,
        true
      );
      return;
    }

    this.setWorkspaceMCPServerHelp('Only globally enabled connectors can be added here.');
  }

  renderWorkspaceMCPAgentOptions(bindingId) {
    if (!this.host.elements.mcpAgentOptions) return;

    const accessOptions = this.getWorkspaceMCPAgentAccessSelections(bindingId);
    if (accessOptions.length === 0) {
      this.host.elements.mcpAgentOptions.innerHTML = `
        <div class="workspace-detail-mcp-agent-empty">
          Add one or more agents to this workspace before assigning MCP access.
        </div>
      `;
      this.updateWorkspaceMCPAgentAccessSummary();
      return;
    }

    this.host.elements.mcpAgentOptions.innerHTML = accessOptions
      .map(
        option => `
      <label class="workspace-detail-mcp-agent-option">
        <input type="checkbox" class="form-check-input workspace-detail-mcp-agent-checkbox" value="${this.host.escapeHtml(option.id)}"${option.checked ? ' checked' : ''}>
        <span class="workspace-detail-mcp-agent-option-copy">
          <span class="workspace-detail-mcp-agent-option-title">${this.host.escapeHtml(option.label)}</span>
          <span class="workspace-detail-mcp-agent-option-meta">${this.host.escapeHtml(option.meta)}</span>
        </span>
      </label>
    `
      )
      .join('');
    this.updateWorkspaceMCPAgentAccessSummary();
  }

  updateWorkspaceMCPAgentAccessSummary() {
    if (!this.host.elements.mcpAgentAccessSummary || !this.host.elements.mcpAgentOptions) return;

    const checkboxes = Array.from(
      this.host.elements.mcpAgentOptions.querySelectorAll('.workspace-detail-mcp-agent-checkbox')
    );
    if (checkboxes.length === 0) {
      this.host.elements.mcpAgentAccessSummary.textContent = 'No agents';
      return;
    }

    const selectedCount = checkboxes.filter(checkbox => checkbox.checked).length;
    this.host.elements.mcpAgentAccessSummary.textContent = `${selectedCount} of ${checkboxes.length} selected`;
  }

  resetWorkspaceMCPModal() {
    this.activeWorkspaceMCPBindingId = '';
    this.activeWorkspaceMCPMode = 'create';

    if (this.host.elements.mcpForm) {
      this.host.elements.mcpForm.reset();
    }
    if (this.host.elements.mcpServerSelect) {
      this.host.elements.mcpServerSelect.innerHTML = '<option value="">Select a connector</option>';
    }
    if (this.host.elements.mcpAgentOptions) {
      this.host.elements.mcpAgentOptions.innerHTML =
        '<div class="workspace-detail-mcp-agent-empty">No agent instances in this workspace yet.</div>';
    }
    if (this.host.elements.mcpModalTitle) {
      this.host.elements.mcpModalTitle.textContent = 'Add Workspace MCP';
    }
    if (this.host.elements.mcpModalSubtitle) {
      this.host.elements.mcpModalSubtitle.textContent =
        'Bind a globally available MCP connector to this workspace, then decide which agent instances can use it here.';
    }
    if (this.host.elements.mcpEnabledInput) {
      this.host.elements.mcpEnabledInput.checked = true;
    }
    if (this.host.elements.mcpScopeInput) {
      this.host.elements.mcpScopeInput.value = '';
    }
    if (this.host.elements.mcpConfigInput) {
      this.host.elements.mcpConfigInput.value = '';
    }
    if (this.host.elements.mcpConfigDetails) {
      this.host.elements.mcpConfigDetails.open = true;
    }
    if (this.host.elements.mcpAliasInput) {
      this.host.elements.mcpAliasInput.value = '';
    }
    if (this.host.elements.mcpEmailFields) {
      this.host.elements.mcpEmailFields.classList.add('d-none');
    }
    if (this.host.elements.mcpEmailAccountSelect) {
      this.host.elements.mcpEmailAccountSelect.innerHTML =
        '<option value="">Select an email account</option>';
      this.host.elements.mcpEmailAccountSelect.value = '';
    }
    if (this.host.elements.mcpEmailMailboxInput) {
      this.host.elements.mcpEmailMailboxInput.value = '';
    }
    this.setWorkspaceMCPEmailActions(['read', 'search']);
    if (this.host.elements.mcpEmailSendConfirmInput) {
      this.host.elements.mcpEmailSendConfirmInput.checked = true;
    }
    this.setWorkspaceMCPEmailAccountHelp('Email accounts come from Vault > Email Accounts.');
    this.updateWorkspaceMCPEmailAccountSummary();
    if (this.host.elements.mcpSubmitBtn) {
      this.host.elements.mcpSubmitBtn.disabled = false;
      this.host.elements.mcpSubmitBtn.textContent = 'Add Binding';
    }
    this.setWorkspaceMCPServerHelp('Only globally enabled connectors can be added here.');
    this.updateWorkspaceMCPAgentAccessSummary();
  }

  async handleWorkspaceMCPServerChange() {
    const serverName = String(this.host.elements.mcpServerSelect?.value || '').trim();
    if (!serverName) {
      await this.syncWorkspaceMCPEmailFields();
      return;
    }
    if (!this.host.elements.mcpAliasInput) return;

    if (!this.host.elements.mcpAliasInput.value.trim()) {
      this.host.elements.mcpAliasInput.value = this.slugifyWorkspaceMCPAlias(serverName);
    }

    await this.syncWorkspaceMCPEmailFields();
  }

  handleWorkspaceMCPListClick(event) {
    const button = event.target.closest('[data-workspace-mcp-action]');
    if (!button) return;
    event.preventDefault();
    event.stopPropagation();

    const action = String(button.dataset.workspaceMcpAction || '').trim();
    const bindingId = String(button.dataset.bindingId || '').trim();
    if (!bindingId) return;

    if (action === 'edit') {
      this.openWorkspaceMCPModal(bindingId);
      return;
    }

    if (action === 'delete') {
      this.deleteWorkspaceMCPBinding(bindingId);
    }
  }

  async openWorkspaceMCPModal(bindingId = '') {
    const explicitBinding = bindingId ? this.getWorkspaceExplicitMCPBinding(bindingId) : null;
    const existingBinding =
      explicitBinding ||
      (bindingId ? this.getWorkspaceMCPBinding(bindingId, { includeDisabled: true }) : null);
    if (bindingId && !existingBinding) {
      if (window.Toast) {
        window.Toast.info('That workspace MCP binding is no longer available.');
      }
      return;
    }

    try {
      await this.loadAvailableMCPServers();
    } catch (error) {
      console.error('Failed to load MCP connectors:', error);
      if (!existingBinding) {
        if (window.Toast) window.Toast.error(error.message || 'Failed to load MCP connectors');
        return;
      }
    }

    const isSynthesized = existingBinding?.source === 'synthesized' && !explicitBinding;
    this.activeWorkspaceMCPMode = explicitBinding ? 'edit' : isSynthesized ? 'customize' : 'create';
    this.activeWorkspaceMCPBindingId = existingBinding?.id || this.generateWorkspaceMCPBindingId();
    this.populateWorkspaceMCPServerOptions(existingBinding?.serverName || '');

    if (this.host.elements.mcpModalTitle) {
      this.host.elements.mcpModalTitle.textContent = explicitBinding
        ? 'Edit Workspace MCP'
        : isSynthesized
          ? 'Customize Workspace MCP'
          : 'Add Workspace MCP';
    }
    if (this.host.elements.mcpModalSubtitle) {
      this.host.elements.mcpModalSubtitle.textContent = explicitBinding
        ? 'Update this workspace binding, refine its scope, or tighten which agent instances can reach it.'
        : isSynthesized
          ? 'This binding is currently derived from imported directories. Saving here will create an explicit workspace binding that you can edit directly.'
          : 'Create a new MCP binding for this workspace and decide which agent instances should be able to use it.';
    }
    if (this.host.elements.mcpAliasInput) {
      this.host.elements.mcpAliasInput.value = existingBinding?.alias || '';
    }
    if (this.host.elements.mcpEnabledInput) {
      this.host.elements.mcpEnabledInput.checked = existingBinding
        ? existingBinding.enabled !== false
        : true;
    }
    if (this.host.elements.mcpScopeInput) {
      const scope =
        existingBinding?.scope && Object.keys(existingBinding.scope).length > 0
          ? JSON.stringify(existingBinding.scope, null, 2)
          : '';
      this.host.elements.mcpScopeInput.value = scope;
    }
    if (this.host.elements.mcpConfigInput) {
      const config =
        existingBinding?.config && Object.keys(existingBinding.config).length > 0
          ? JSON.stringify(existingBinding.config, null, 2)
          : '';
      this.host.elements.mcpConfigInput.value = config;
    }
    const emailConfig =
      existingBinding?.config && typeof existingBinding.config === 'object'
        ? existingBinding.config
        : {};
    if (this.host.elements.mcpEmailMailboxInput) {
      const mailboxes = Array.isArray(emailConfig.mailboxes)
        ? emailConfig.mailboxes.map(item => String(item || '').trim()).filter(Boolean)
        : [];
      this.host.elements.mcpEmailMailboxInput.value = mailboxes.join(', ');
    }
    this.setWorkspaceMCPEmailActions(
      Array.isArray(emailConfig.allowed_actions) && emailConfig.allowed_actions.length > 0
        ? emailConfig.allowed_actions
        : ['read', 'search']
    );
    if (this.host.elements.mcpEmailSendConfirmInput) {
      this.host.elements.mcpEmailSendConfirmInput.checked =
        emailConfig.require_send_confirmation !== false;
    }
    if (this.host.elements.mcpSubmitBtn) {
      this.host.elements.mcpSubmitBtn.textContent = explicitBinding
        ? 'Save Changes'
        : isSynthesized
          ? 'Customize Binding'
          : 'Add Binding';
      this.host.elements.mcpSubmitBtn.disabled = false;
    }

    await this.syncWorkspaceMCPEmailFields(existingBinding);
    this.renderWorkspaceMCPAgentOptions(this.activeWorkspaceMCPBindingId);
    this.getWorkspaceMCPModalInstance()?.show();
  }

  parseWorkspaceMCPScopeValue() {
    const raw = String(this.host.elements.mcpScopeInput?.value || '').trim();
    if (!raw) return {};

    let parsed;
    try {
      parsed = JSON.parse(raw);
    } catch (_error) {
      throw new Error('Scope JSON must be valid JSON');
    }

    if (!parsed || Array.isArray(parsed) || typeof parsed !== 'object') {
      throw new Error('Scope JSON must be an object');
    }

    return parsed;
  }

  parseWorkspaceMCPConfigValue() {
    const raw = String(this.host.elements.mcpConfigInput?.value || '').trim();
    if (!raw) return {};

    let parsed;
    try {
      parsed = JSON.parse(raw);
    } catch (_error) {
      throw new Error('Config JSON must be valid JSON');
    }

    if (!parsed || Array.isArray(parsed) || typeof parsed !== 'object') {
      throw new Error('Config JSON must be an object');
    }

    return parsed;
  }

  parseWorkspaceMCPEmailMailboxes() {
    return String(this.host.elements.mcpEmailMailboxInput?.value || '')
      .split(/[\n,]+/)
      .map(value => String(value || '').trim())
      .filter(Boolean);
  }

  buildWorkspaceMCPEmailConfig(baseConfig = {}) {
    const config = baseConfig && typeof baseConfig === 'object' ? { ...baseConfig } : {};
    const accountID = String(this.host.elements.mcpEmailAccountSelect?.value || '').trim();
    const allowedActions = this.getWorkspaceMCPSelectedEmailActions();

    if (!accountID) {
      throw new Error('Choose an email account');
    }
    if (allowedActions.length === 0) {
      throw new Error('Select at least one email action');
    }

    config.account_id = accountID;
    delete config.account_vault_record_id;
    config.allowed_actions = allowedActions;

    const mailboxes = this.parseWorkspaceMCPEmailMailboxes();
    if (mailboxes.length > 0) {
      config.mailboxes = mailboxes;
    } else {
      delete config.mailboxes;
    }

    if (allowedActions.includes('send')) {
      config.require_send_confirmation = this.host.elements.mcpEmailSendConfirmInput?.checked !== false;
    } else {
      delete config.require_send_confirmation;
    }

    return config;
  }

  getWorkspaceMCPSelectedAgentInstanceIDs() {
    if (!this.host.elements.mcpAgentOptions) return [];
    return Array.from(
      this.host.elements.mcpAgentOptions.querySelectorAll('.workspace-detail-mcp-agent-checkbox:checked')
    )
      .map(checkbox => String(checkbox.value || '').trim())
      .filter(Boolean);
  }

  setWorkspaceMCPModalSubmitting(isSubmitting) {
    if (!this.host.elements.mcpSubmitBtn) return;
    this.host.elements.mcpSubmitBtn.disabled = isSubmitting;
    this.host.elements.mcpSubmitBtn.textContent = isSubmitting
      ? this.activeWorkspaceMCPMode === 'edit'
        ? 'Saving...'
        : this.activeWorkspaceMCPMode === 'customize'
          ? 'Customizing...'
          : 'Adding...'
      : this.activeWorkspaceMCPMode === 'edit'
        ? 'Save Changes'
        : this.activeWorkspaceMCPMode === 'customize'
          ? 'Customize Binding'
          : 'Add Binding';
  }

  async saveWorkspaceMCPBinding(payload) {
    const isEditing = this.activeWorkspaceMCPMode === 'edit';
    const endpoint = isEditing
      ? `/api/workspaces/${encodeURIComponent(this.host.workspaceId)}/mcp-bindings/${encodeURIComponent(this.activeWorkspaceMCPBindingId)}`
      : `/api/workspaces/${encodeURIComponent(this.host.workspaceId)}/mcp-bindings`;

    const response = await fetch(endpoint, {
      method: isEditing ? 'PUT' : 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify(payload)
    });

    if (!response.ok) {
      const text = await response.text();
      throw new Error(text || 'Failed to save workspace MCP binding');
    }

    return response.json();
  }

  async persistWorkspaceMCPAgentAccess(bindingId, selectedAgentInstanceIds) {
    if (
      !this.host.workspace ||
      !Array.isArray(this.host.workspace.agent_instances) ||
      this.host.workspace.agent_instances.length === 0
    ) {
      return;
    }

    const selectedSet = new Set(
      selectedAgentInstanceIds.map(value => String(value || '').trim()).filter(Boolean)
    );
    const effectiveBindingIds = this.getWorkspaceMCPBindings()
      .map(binding => String(binding?.id || '').trim())
      .filter(Boolean);
    const defaultBindingIds = Array.from(new Set(effectiveBindingIds)).sort();
    const normalizeIDs = ids =>
      Array.from(new Set(ids.map(value => String(value || '').trim()).filter(Boolean))).sort();
    const arraysEqual = (left, right) =>
      left.length === right.length && left.every((value, index) => value === right[index]);

    const requests = this.host.workspace.agent_instances.map(async instance => {
      const instanceId = String(instance?.id || '').trim();
      if (!instanceId) return;

      const entry = this.getWorkspaceAgentAccessEntry(instanceId);
      const currentIds = entry
        ? Array.isArray(entry.enabled_binding_ids)
          ? entry.enabled_binding_ids.map(value => String(value || '').trim()).filter(Boolean)
          : []
        : [...defaultBindingIds];
      const allowedSet = new Set(currentIds);

      if (selectedSet.has(instanceId)) {
        allowedSet.add(bindingId);
      } else {
        allowedSet.delete(bindingId);
      }

      const enabledBindingIDs = normalizeIDs(Array.from(allowedSet));
      const currentNormalized = normalizeIDs(currentIds);

      if (!entry && arraysEqual(enabledBindingIDs, defaultBindingIds)) {
        return;
      }

      if (entry && arraysEqual(enabledBindingIDs, defaultBindingIds)) {
        const response = await fetch(
          `/api/workspaces/${encodeURIComponent(this.host.workspaceId)}/agent-mcp-access/${encodeURIComponent(instanceId)}`,
          { method: 'DELETE' }
        );

        if (!response.ok) {
          const text = await response.text();
          throw new Error(text || `Failed to clear MCP access rule for ${instanceId}`);
        }
        return;
      }

      if (arraysEqual(enabledBindingIDs, currentNormalized)) {
        return;
      }

      const response = await fetch(
        `/api/workspaces/${encodeURIComponent(this.host.workspaceId)}/agent-mcp-access/${encodeURIComponent(instanceId)}`,
        {
          method: 'PUT',
          headers: { 'Content-Type': 'application/json' },
          body: JSON.stringify({ enabled_binding_ids: enabledBindingIDs })
        }
      );

      if (!response.ok) {
        const text = await response.text();
        throw new Error(text || `Failed to update MCP access for ${instanceId}`);
      }
    });

    await Promise.all(requests);
  }

  async submitWorkspaceMCPModal() {
    const serverName = String(this.host.elements.mcpServerSelect?.value || '').trim();
    const alias = String(this.host.elements.mcpAliasInput?.value || '').trim();

    if (!serverName) {
      this.setWorkspaceMCPServerHelp(
        'Choose a connector before saving this workspace binding.',
        true
      );
      if (window.Toast) window.Toast.error('Choose a connector');
      return;
    }

    let scope;
    try {
      scope = this.parseWorkspaceMCPScopeValue();
    } catch (error) {
      this.setWorkspaceMCPServerHelp(error.message || 'Scope JSON is invalid', true);
      if (window.Toast) window.Toast.error(error.message || 'Scope JSON is invalid');
      return;
    }

    let config;
    try {
      config = this.parseWorkspaceMCPConfigValue();
    } catch (error) {
      this.setWorkspaceMCPServerHelp(error.message || 'Config JSON is invalid', true);
      if (window.Toast) window.Toast.error(error.message || 'Config JSON is invalid');
      return;
    }

    if (this.isEmailWorkspaceMCPServerName(serverName)) {
      try {
        config = this.buildWorkspaceMCPEmailConfig(config);
        this.setWorkspaceMCPEmailAccountHelp(
          'Email accounts come from Vault > Email Accounts. Workspace-scoped accounts must match this workspace.',
          false
        );
      } catch (error) {
        this.setWorkspaceMCPEmailAccountHelp(
          error.message || 'Email configuration is invalid',
          true
        );
        if (window.Toast) window.Toast.error(error.message || 'Email configuration is invalid');
        return;
      }
    }

    this.setWorkspaceMCPServerHelp('Only globally enabled connectors can be added here.');
    this.setWorkspaceMCPModalSubmitting(true);

    try {
      const enabled = this.host.elements.mcpEnabledInput?.checked !== false;
      const selectedAgentInstanceIds = this.getWorkspaceMCPSelectedAgentInstanceIDs();
      const payload = {
        server_name: serverName,
        alias,
        enabled,
        scope,
        config
      };

      if (this.activeWorkspaceMCPMode !== 'edit') {
        payload.id = this.activeWorkspaceMCPBindingId;
      }

      await this.saveWorkspaceMCPBinding(payload);
      await this.host.loadWorkspace();
      await this.persistWorkspaceMCPAgentAccess(
        this.activeWorkspaceMCPBindingId,
        selectedAgentInstanceIds
      );
      await this.host.loadWorkspace();

      this.getWorkspaceMCPModalInstance()?.hide();
      if (window.Toast) {
        window.Toast.success(
          this.activeWorkspaceMCPMode === 'edit'
            ? 'Workspace MCP updated'
            : this.activeWorkspaceMCPMode === 'customize'
              ? 'Workspace MCP customized'
              : 'Workspace MCP added'
        );
      }
    } catch (error) {
      console.error('Failed to save workspace MCP binding:', error);
      if (window.Toast) window.Toast.error(error.message || 'Failed to save workspace MCP binding');
    } finally {
      this.setWorkspaceMCPModalSubmitting(false);
    }
  }

  async deleteWorkspaceMCPBinding(bindingId) {
    const binding = this.getWorkspaceExplicitMCPBinding(bindingId);
    if (!binding) {
      if (window.Toast) {
        window.Toast.info(
          'Synthesized bindings follow workspace directories and are removed by changing directory scope.'
        );
      }
      return;
    }

    const label = binding.alias || binding.serverName || binding.id;
    if (!window.confirm(`Remove workspace MCP binding "${label}"?`)) {
      return;
    }

    try {
      const response = await fetch(
        `/api/workspaces/${encodeURIComponent(this.host.workspaceId)}/mcp-bindings/${encodeURIComponent(bindingId)}`,
        { method: 'DELETE' }
      );

      if (!response.ok) {
        const text = await response.text();
        throw new Error(text || 'Failed to remove workspace MCP binding');
      }

      await this.host.loadWorkspace();
      if (window.Toast) window.Toast.success('Workspace MCP removed');
    } catch (error) {
      console.error('Failed to delete workspace MCP binding:', error);
      if (window.Toast)
        window.Toast.error(error.message || 'Failed to remove workspace MCP binding');
    }
  }

  renderWorkspaceMCPBindings() {
    if (!this.host.elements.mcpList) return;

    const bindings = this.getWorkspaceMCPBindings({ includeDisabled: true });
    if (bindings.length === 0) {
      this.host.elements.mcpList.innerHTML = `
        <div class="workspace-detail-empty">
          No workspace MCP bindings yet.
          <div class="workspace-detail-mcp-empty-note">Import directories to synthesize <code>filesystem</code>, or add an explicit binding with the <strong>+</strong> button.</div>
        </div>
      `;
      this.host.renderWorkspaceConfigSummary();
      return;
    }

    this.host.elements.mcpList.innerHTML = bindings
      .map(binding => {
        const serverName = String(binding?.serverName || '').trim() || 'unknown';
        const emailServer = this.isEmailWorkspaceMCPServerName(serverName);
        const emailAccount = this.normalizeWorkspaceEmailAccount(binding?.emailAccount);
        const alias = String(binding?.alias || '').trim();
        const source = binding?.source === 'synthesized' ? 'Synthesized' : 'Explicit';
        const isDisabled = binding?.enabled === false;
        const scopeSummary = this.summarizeWorkspaceMCPBindingScope(binding);
        const configSummary = this.summarizeWorkspaceMCPBindingConfig(binding);
        const emailActions = Array.isArray(binding?.config?.allowed_actions)
          ? binding.config.allowed_actions
              .map(action => String(action || '').trim())
              .filter(Boolean)
          : [];
        const agentNames = this.getWorkspaceMCPAgentNamesForBinding(binding.id);
        const accessSummary = isDisabled
          ? 'Disabled for this workspace'
          : agentNames.length > 0
            ? `${agentNames.length} agent${agentNames.length === 1 ? '' : 's'} can use this`
            : Array.isArray(this.host.workspace?.agent_instances) &&
                this.host.workspace.agent_instances.length > 0
              ? 'No agent instances currently have access'
              : 'No agent instances in this workspace';
        const accessLabel = isDisabled
          ? 'Agents: unavailable while disabled'
          : agentNames.length > 0
            ? `Agents: ${agentNames.join(', ')}`
            : 'Agents: none';
        const actions =
          binding?.source === 'workspace'
            ? `
          <div class="workspace-detail-mcp-card-actions">
            <button type="button" class="workspace-detail-mcp-card-btn" data-workspace-mcp-action="edit" data-binding-id="${this.host.escapeHtml(binding.id)}" onclick="event.preventDefault(); event.stopPropagation(); window.workspaceDetail?.openWorkspaceMCPModal('${this.host.escapeHtml(binding.id)}')" title="Edit binding" aria-label="Edit binding ${this.host.escapeHtml(alias || serverName)}">
              <svg width="12" height="12" viewBox="0 0 24 24" fill="currentColor">
                <path d="M20.71,7.04C21.1,6.65 21.1,6 20.71,5.63L18.37,3.29C18,2.9 17.35,2.9 16.96,3.29L15.12,5.12L18.87,8.87M3,17.25V21H6.75L17.81,9.93L14.06,6.18L3,17.25Z"/>
              </svg>
            </button>
            <button type="button" class="workspace-detail-mcp-card-btn is-danger" data-workspace-mcp-action="delete" data-binding-id="${this.host.escapeHtml(binding.id)}" onclick="event.preventDefault(); event.stopPropagation(); window.workspaceDetail?.deleteWorkspaceMCPBinding('${this.host.escapeHtml(binding.id)}')" title="Remove binding" aria-label="Remove binding ${this.host.escapeHtml(alias || serverName)}">
              <svg width="12" height="12" viewBox="0 0 24 24" fill="currentColor">
                <path d="M9,3V4H4V6H5V19A2,2 0 0,0 7,21H17A2,2 0 0,0 19,19V6H20V4H15V3H9M7,6H17V19H7V6M9,8V17H11V8H9M13,8V17H15V8H13Z"/>
              </svg>
            </button>
          </div>
        `
            : binding?.source === 'synthesized'
              ? `
            <div class="workspace-detail-mcp-card-actions">
              <button type="button" class="workspace-detail-mcp-card-btn" data-workspace-mcp-action="edit" data-binding-id="${this.host.escapeHtml(binding.id)}" onclick="event.preventDefault(); event.stopPropagation(); window.workspaceDetail?.openWorkspaceMCPModal('${this.host.escapeHtml(binding.id)}')" title="Customize binding" aria-label="Customize binding ${this.host.escapeHtml(alias || serverName)}">
                <svg width="12" height="12" viewBox="0 0 24 24" fill="currentColor">
                  <path d="M20.71,7.04C21.1,6.65 21.1,6 20.71,5.63L18.37,3.29C18,2.9 17.35,2.9 16.96,3.29L15.12,5.12L18.87,8.87M3,17.25V21H6.75L17.81,9.93L14.06,6.18L3,17.25Z"/>
                </svg>
              </button>
            </div>
          `
              : '';
        const chips = [
          `<span class="workspace-detail-mcp-chip source">${this.host.escapeHtml(source)}</span>`,
          `<span class="workspace-detail-mcp-chip status${isDisabled ? ' is-disabled' : ''}">${isDisabled ? 'Disabled' : 'Enabled'}</span>`,
          alias
            ? `<span class="workspace-detail-mcp-chip alias">Alias: ${this.host.escapeHtml(alias)}</span>`
            : '',
          emailServer && emailAccount?.provider
            ? `<span class="workspace-detail-mcp-chip provider">${this.host.escapeHtml(emailAccount.provider)}</span>`
            : '',
          emailServer && emailAccount?.emailAddress
            ? `<span class="workspace-detail-mcp-chip email">${this.host.escapeHtml(emailAccount.emailAddress)}</span>`
            : '',
          emailServer && emailActions.length > 0
            ? `<span class="workspace-detail-mcp-chip policy">${this.host.escapeHtml(`Actions: ${emailActions.join(', ')}`)}</span>`
            : '',
          emailServer && binding?.config?.require_send_confirmation === true
            ? '<span class="workspace-detail-mcp-chip policy">Send confirm</span>'
            : '',
          emailServer && binding?.emailAccountMissing
            ? '<span class="workspace-detail-mcp-chip warning">Account unavailable</span>'
            : '',
          scopeSummary
            ? `<span class="workspace-detail-mcp-chip scope">${this.host.escapeHtml(scopeSummary)}</span>`
            : '',
          configSummary
            ? `<span class="workspace-detail-mcp-chip scope">Config: ${this.host.escapeHtml(configSummary)}</span>`
            : '',
          `<span class="workspace-detail-mcp-chip access">${this.host.escapeHtml(accessLabel)}</span>`
        ]
          .filter(Boolean)
          .join('');

        return `
        <div class="workspace-detail-mcp-card" data-binding-id="${this.host.escapeHtml(binding.id)}">
          <div class="workspace-detail-mcp-card-top">
            <div class="workspace-detail-mcp-card-top-main">
              <div class="workspace-detail-mcp-server">
                <span>${this.host.escapeHtml(serverName)}</span>
                <code>${this.host.escapeHtml(binding.id)}</code>
              </div>
              <div class="workspace-detail-mcp-meta">${this.host.escapeHtml(accessSummary)}</div>
            </div>
            ${actions}
          </div>
          <div class="workspace-detail-mcp-description">${this.host.escapeHtml(this.describeWorkspaceMCPBinding(binding))}</div>
          <div class="workspace-detail-mcp-chip-row">${chips}</div>
        </div>
      `;
      })
      .join('');
    this.host.renderWorkspaceConfigSummary();
  }
  getEffectiveWorkspaceMCPBindingsForAgent(agentName) {
    const bindings = this.getWorkspaceMCPBindings();
    if (bindings.length === 0) {
      return [];
    }

    const instanceIds = this.host.getAgentInstanceIdsForName(agentName);
    if (instanceIds.length === 0) {
      return bindings;
    }

    const accessEntries = Array.isArray(this.host.workspace?.agent_mcp_access)
      ? this.host.workspace.agent_mcp_access
      : [];

    const allowedByInstance = instanceIds.map(instanceID => {
      const entry = accessEntries.find(
        item => String(item?.agent_instance_id || '').trim() === instanceID
      );
      if (!entry) {
        return bindings;
      }
      if (!Array.isArray(entry.enabled_binding_ids) || entry.enabled_binding_ids.length === 0) {
        return [];
      }

      const allowedIDs = new Set(
        entry.enabled_binding_ids
          .map(value =>
            String(value || '')
              .trim()
              .toLowerCase()
          )
          .filter(Boolean)
      );
      return bindings.filter(binding =>
        allowedIDs.has(
          String(binding.id || '')
            .trim()
            .toLowerCase()
        )
      );
    });

    const merged = [];
    const seen = new Set();
    allowedByInstance.flat().forEach(binding => {
      const key =
        String(binding?.id || '')
          .trim()
          .toLowerCase() ||
        String(binding?.serverName || '')
          .trim()
          .toLowerCase();
      if (!key || seen.has(key)) return;
      seen.add(key);
      merged.push(binding);
    });
    return merged;
  }

  getEffectiveWorkspaceMCPServerNames(agentName) {
    const names = [];
    const seen = new Set();
    const add = value => {
      const name = String(value || '').trim();
      if (!name) return;
      const key = name.toLowerCase();
      if (seen.has(key)) return;
      seen.add(key);
      names.push(name);
    };

    this.getEffectiveWorkspaceMCPBindingsForAgent(agentName).forEach(binding =>
      add(binding.serverName)
    );

    const profile = this.host.getAgentProfile(agentName);
    if (Array.isArray(profile?.mcpServers)) {
      profile.mcpServers.forEach(add);
    }

    return names;
  }
}

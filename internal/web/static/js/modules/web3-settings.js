/**
 * Web3 Settings Manager
 * Handles Web3 wallet connection, network switching, and state management
 */

class Web3SettingsManager {
  constructor() {
    this.provider = null;
    this.signer = null;
    this.address = null;
    this.chainId = null;
    this.ensName = null;
    this.isInitialized = false;
    this.web3Enabled = true;

    // Supported chains configuration
    this.CHAINS = {
      1: { name: 'Ethereum Mainnet', symbol: 'ETH', hexId: '0x1' },
      137: { name: 'Polygon', symbol: 'MATIC', hexId: '0x89' },
      42161: { name: 'Arbitrum', symbol: 'ETH', hexId: '0xa4b1' },
      10: { name: 'Optimism', symbol: 'ETH', hexId: '0xa' },
      8453: { name: 'Base', symbol: 'ETH', hexId: '0x2105' }
    };

    // DOM elements (will be set in init)
    this.elements = {};
  }

  /**
   * Initialize the Web3 settings manager
   */
  async init() {
    // Only run on settings page
    if (!document.getElementById('web3Status')) {
      return;
    }

    this.cacheElements();
    this.web3Enabled = !(window.oriFeatures && window.oriFeatures.web3Enabled === false);
    if (!this.web3Enabled) {
      this.showDisabledState();
      return;
    }
    this.bindEvents();

    // Check if ethereum provider is available
    if (typeof window.ethereum === 'undefined') {
      this.showNoWalletState();
      return;
    }

    // Load saved wallet state from server
    await this.loadSavedState();

    // Set up ethereum event listeners
    this.setupEthereumListeners();

    this.isInitialized = true;
  }

  /**
   * Cache DOM elements for performance
   */
  cacheElements() {
    this.elements = {
      status: document.getElementById('web3Status'),
      statusIndicator: document.getElementById('web3StatusIndicator'),
      statusText: document.getElementById('web3StatusText'),
      statusDetails: document.getElementById('web3StatusDetails'),
      connected: document.getElementById('web3Connected'),
      disconnected: document.getElementById('web3Disconnected'),
      noWallet: document.getElementById('web3NoWallet'),
      address: document.getElementById('web3Address'),
      ensName: document.getElementById('web3ENSName'),
      network: document.getElementById('web3Network'),
      chainId: document.getElementById('web3ChainId'),
      networkSelect: document.getElementById('web3NetworkSelect'),
      connectBtn: document.getElementById('web3ConnectBtn'),
      disconnectBtn: document.getElementById('web3DisconnectBtn'),
      switchNetworkBtn: document.getElementById('web3SwitchNetworkBtn'),
      alerts: document.getElementById('web3Alerts')
    };
  }

  /**
   * Bind event handlers
   */
  bindEvents() {
    if (this.elements.connectBtn) {
      this.elements.connectBtn.addEventListener('click', () => this.connectWallet());
    }
    if (this.elements.disconnectBtn) {
      this.elements.disconnectBtn.addEventListener('click', () => this.disconnectWallet());
    }
    if (this.elements.switchNetworkBtn) {
      this.elements.switchNetworkBtn.addEventListener('click', () => this.switchNetwork());
    }
  }

  /**
   * Set up ethereum provider event listeners
   */
  setupEthereumListeners() {
    if (!window.ethereum) return;

    window.ethereum.on('accountsChanged', accounts => {
      if (accounts.length === 0) {
        // User disconnected wallet
        this.handleDisconnect();
      } else {
        // User switched accounts
        this.handleAccountChange(accounts[0]);
      }
    });

    window.ethereum.on('chainChanged', chainId => {
      // Chain ID is returned as hex string
      const decimalChainId = parseInt(chainId, 16);
      this.handleChainChange(decimalChainId);
    });
  }

  /**
   * Load saved wallet state from server
   */
  async loadSavedState() {
    try {
      const response = await fetch('/api/web3-wallet');
      const data = await response.json();

      if (data.connected) {
        // We have a saved wallet connection
        this.address = data.address;
        this.chainId = data.chain_id;
        this.ensName = data.ens_name || null;

        // Check if wallet is still connected in browser
        const accounts = await window.ethereum.request({
          method: 'eth_accounts'
        });

        if (accounts.length > 0 && accounts[0].toLowerCase() === this.address.toLowerCase()) {
          // Wallet is still connected, show connected state
          this.showConnectedState(data);
        } else {
          // Wallet is not connected in browser anymore
          await this.disconnectWallet(true);
          this.showDisconnectedState();
        }
      } else {
        this.showDisconnectedState();
      }
    } catch (error) {
      console.error('Failed to load Web3 wallet state:', error);
      this.showDisconnectedState();
    }
  }

  /**
   * Connect wallet
   */
  async connectWallet() {
    if (!this.web3Enabled) {
      this.showDisabledState();
      return;
    }

    if (!window.ethereum) {
      this.showAlert('No Web3 wallet detected. Please install MetaMask.', 'warning');
      return;
    }

    try {
      this.showConnectingState();

      // Request account access
      const accounts = await window.ethereum.request({
        method: 'eth_requestAccounts'
      });

      if (accounts.length === 0) {
        throw new Error('No accounts found');
      }

      this.address = accounts[0];

      // Get chain ID
      const chainId = await window.ethereum.request({ method: 'eth_chainId' });
      this.chainId = parseInt(chainId, 16);

      // Try to resolve ENS name (only on Ethereum mainnet)
      if (this.chainId === 1 && typeof ethers !== 'undefined') {
        try {
          const provider = new ethers.BrowserProvider(window.ethereum);
          this.ensName = await provider.lookupAddress(this.address);
        } catch {
          this.ensName = null;
        }
      }

      // Save to server
      await this.saveWalletToServer();

      // Update UI
      this.showConnectedState({
        address: this.address,
        address_masked: this.maskAddress(this.address),
        chain_id: this.chainId,
        chain_name: this.CHAINS[this.chainId]?.name || `Chain ${this.chainId}`,
        ens_name: this.ensName
      });

      this.showAlert('Wallet connected successfully!', 'success');
    } catch (error) {
      console.error('Failed to connect wallet:', error);
      this.showDisconnectedState();

      if (error.code === 4001) {
        this.showAlert('Connection request was rejected.', 'warning');
      } else {
        this.showAlert('Failed to connect wallet. Please try again.', 'danger');
      }
    }
  }

  /**
   * Disconnect wallet
   */
  async disconnectWallet(silent = false) {
    if (!this.web3Enabled) {
      this.showDisabledState();
      return;
    }

    try {
      // Clear server state
      await fetch('/api/web3-wallet', { method: 'DELETE' });

      // Clear local state
      this.address = null;
      this.chainId = null;
      this.ensName = null;

      this.showDisconnectedState();

      if (!silent) {
        this.showAlert('Wallet disconnected.', 'info');
      }
    } catch (error) {
      console.error('Failed to disconnect wallet:', error);
      if (!silent) {
        this.showAlert('Failed to disconnect wallet.', 'danger');
      }
    }
  }

  /**
   * Switch network
   */
  async switchNetwork() {
    if (!this.web3Enabled) {
      this.showDisabledState();
      return;
    }

    if (!window.ethereum || !this.elements.networkSelect) return;

    const targetChainId = parseInt(this.elements.networkSelect.value, 10);
    const chain = this.CHAINS[targetChainId];

    if (!chain) {
      this.showAlert('Unsupported network', 'warning');
      return;
    }

    try {
      await window.ethereum.request({
        method: 'wallet_switchEthereumChain',
        params: [{ chainId: chain.hexId }]
      });
    } catch (error) {
      if (error.code === 4902) {
        this.showAlert(
          'This network is not configured in your wallet. Please add it manually.',
          'warning'
        );
      } else if (error.code === 4001) {
        this.showAlert('Network switch was rejected.', 'warning');
      } else {
        console.error('Failed to switch network:', error);
        this.showAlert('Failed to switch network.', 'danger');
      }
    }
  }

  /**
   * Save wallet connection to server
   */
  async saveWalletToServer() {
    const response = await fetch('/api/web3-wallet', {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({
        address: this.address,
        chain_id: this.chainId,
        ens_name: this.ensName || ''
      })
    });

    if (!response.ok) {
      throw new Error('Failed to save wallet to server');
    }

    return response.json();
  }

  /**
   * Handle account change from wallet
   */
  async handleAccountChange(newAddress) {
    this.address = newAddress;

    // Try to resolve ENS name for new address
    if (this.chainId === 1 && typeof ethers !== 'undefined') {
      try {
        const provider = new ethers.BrowserProvider(window.ethereum);
        this.ensName = await provider.lookupAddress(this.address);
      } catch {
        this.ensName = null;
      }
    } else {
      this.ensName = null;
    }

    // Save updated state
    await this.saveWalletToServer();

    // Update UI
    this.showConnectedState({
      address: this.address,
      address_masked: this.maskAddress(this.address),
      chain_id: this.chainId,
      chain_name: this.CHAINS[this.chainId]?.name || `Chain ${this.chainId}`,
      ens_name: this.ensName
    });

    this.showAlert('Account changed.', 'info');
  }

  /**
   * Handle chain change from wallet
   */
  async handleChainChange(newChainId) {
    this.chainId = newChainId;

    // Clear ENS name if not on mainnet
    if (this.chainId !== 1) {
      this.ensName = null;
    }

    // Save updated state
    if (this.address) {
      await this.saveWalletToServer();

      // Update UI
      this.showConnectedState({
        address: this.address,
        address_masked: this.maskAddress(this.address),
        chain_id: this.chainId,
        chain_name: this.CHAINS[this.chainId]?.name || `Chain ${this.chainId}`,
        ens_name: this.ensName
      });

      this.showAlert(`Switched to ${this.CHAINS[this.chainId]?.name || 'Unknown Network'}`, 'info');
    }
  }

  /**
   * Handle disconnect from wallet
   */
  handleDisconnect() {
    this.disconnectWallet(true);
    this.showAlert('Wallet disconnected from browser.', 'info');
  }

  /**
   * Show connected state UI
   */
  showConnectedState(data) {
    // Update status
    this.elements.statusIndicator.innerHTML =
      '<span class="badge bg-success" style="width: 12px; height: 12px; border-radius: 50%; padding: 0;"></span>';
    this.elements.statusText.textContent = 'Connected';
    this.elements.statusDetails.textContent = data.address_masked;

    // Update address display
    this.elements.address.textContent = data.address_masked;
    if (data.ens_name) {
      this.elements.ensName.textContent = data.ens_name;
      this.elements.ensName.classList.remove('d-none');
    } else {
      this.elements.ensName.textContent = '';
      this.elements.ensName.classList.add('d-none');
    }

    // Update network display
    this.elements.network.textContent = data.chain_name;
    this.elements.chainId.textContent = `Chain ID: ${data.chain_id}`;

    // Update network selector
    if (this.elements.networkSelect) {
      this.elements.networkSelect.value = data.chain_id.toString();
    }

    // Show/hide appropriate sections
    this.elements.connected.classList.remove('d-none');
    this.elements.disconnected.classList.add('d-none');
    this.elements.noWallet.classList.add('d-none');
  }

  /**
   * Show disconnected state UI
   */
  showDisconnectedState() {
    // Update status
    this.elements.statusIndicator.innerHTML =
      '<span class="badge bg-secondary" style="width: 12px; height: 12px; border-radius: 50%; padding: 0;"></span>';
    this.elements.statusText.textContent = 'Not Connected';
    this.elements.statusDetails.textContent = 'Connect your wallet to get started';

    // Show/hide appropriate sections
    this.elements.connected.classList.add('d-none');
    this.elements.disconnected.classList.remove('d-none');
    this.elements.noWallet.classList.add('d-none');
  }

  /**
   * Show connecting state UI
   */
  showConnectingState() {
    this.elements.statusIndicator.innerHTML =
      '<span class="spinner-border spinner-border-sm" role="status"></span>';
    this.elements.statusText.textContent = 'Connecting...';
    this.elements.statusDetails.textContent = 'Please approve the connection in your wallet';
  }

  /**
   * Show no wallet available state UI
   */
  showNoWalletState() {
    // Update status
    this.elements.statusIndicator.innerHTML =
      '<span class="badge bg-warning" style="width: 12px; height: 12px; border-radius: 50%; padding: 0;"></span>';
    this.elements.statusText.textContent = 'No Wallet Detected';
    this.elements.statusDetails.textContent = 'Install a Web3 wallet to continue';

    // Show/hide appropriate sections
    this.elements.connected.classList.add('d-none');
    this.elements.disconnected.classList.add('d-none');
    this.elements.noWallet.classList.remove('d-none');
  }

  /**
   * Show disabled state UI
   */
  showDisabledState() {
    if (!this.elements.statusIndicator) {
      return;
    }

    this.elements.statusIndicator.innerHTML =
      '<span class="badge bg-secondary" style="width: 12px; height: 12px; border-radius: 50%; padding: 0;"></span>';
    this.elements.statusText.textContent = 'Web3 Disabled';
    this.elements.statusDetails.textContent = 'Web3 features are disabled on this server.';

    this.elements.connected.classList.add('d-none');
    this.elements.disconnected.classList.add('d-none');
    this.elements.noWallet.classList.add('d-none');
    this.showAlert('Web3 features are disabled on this server.', 'info');
  }

  /**
   * Mask an ethereum address for display
   */
  maskAddress(address) {
    if (!address || address.length < 10) return address;
    return `${address.slice(0, 6)}...${address.slice(-4)}`;
  }

  /**
   * Show an alert message
   */
  showAlert(message, type = 'info') {
    if (!this.elements.alerts) return;

    const alertId = `web3Alert${Date.now()}`;
    const alertHtml = `
      <div id="${alertId}" class="alert alert-${type} alert-dismissible fade show" role="alert">
        ${message}
        <button type="button" class="btn-close" data-bs-dismiss="alert" aria-label="Close"></button>
      </div>
    `;

    this.elements.alerts.insertAdjacentHTML('beforeend', alertHtml);

    // Auto-dismiss after 5 seconds
    setTimeout(() => {
      const alertEl = document.getElementById(alertId);
      if (alertEl) {
        alertEl.remove();
      }
    }, 5000);
  }
}

// Initialize on page load
const web3SettingsManager = new Web3SettingsManager();
document.addEventListener('DOMContentLoaded', () => web3SettingsManager.init());

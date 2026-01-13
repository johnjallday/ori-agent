package settingshttp

import (
	"net/http"
	"time"

	"github.com/johnjallday/ori-agent/internal/config"
	orihttp "github.com/johnjallday/ori-agent/internal/http"
)

// Web3WalletHandler handles Web3 wallet connection operations
func (h *Handler) Web3WalletHandler(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		h.getWeb3Wallet(w, r)
	case http.MethodPost:
		h.saveWeb3Wallet(w, r)
	case http.MethodDelete:
		h.disconnectWeb3Wallet(w, r)
	default:
		w.WriteHeader(http.StatusMethodNotAllowed)
	}
}

// getWeb3Wallet returns the current Web3 wallet connection info
func (h *Handler) getWeb3Wallet(w http.ResponseWriter, r *http.Request) {
	wallet := h.configManager.GetWeb3Wallet()

	if wallet == nil {
		// No wallet connected
		response := struct {
			Connected bool `json:"connected"`
		}{
			Connected: false,
		}
		orihttp.WriteJSON(w, response)
		return
	}

	// Return wallet info with masked address for display
	response := struct {
		Connected     bool   `json:"connected"`
		Address       string `json:"address"`
		AddressMasked string `json:"address_masked"`
		ChainID       int    `json:"chain_id"`
		ChainName     string `json:"chain_name"`
		ENSName       string `json:"ens_name,omitempty"`
		ConnectedAt   string `json:"connected_at,omitempty"`
	}{
		Connected:     true,
		Address:       wallet.Address,
		AddressMasked: config.MaskWeb3Address(wallet.Address),
		ChainID:       wallet.ChainID,
		ChainName:     config.SupportedChains()[wallet.ChainID],
		ENSName:       wallet.ENSName,
		ConnectedAt:   wallet.ConnectedAt,
	}
	orihttp.WriteJSON(w, response)
}

// saveWeb3Wallet saves a Web3 wallet connection
func (h *Handler) saveWeb3Wallet(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Address string `json:"address"`
		ChainID int    `json:"chain_id"`
		ENSName string `json:"ens_name,omitempty"`
	}

	if !orihttp.ParseJSONBody(w, r, &req) {
		return
	}

	// Create wallet object
	wallet := &config.Web3Wallet{
		Address:     req.Address,
		ChainID:     req.ChainID,
		ENSName:     req.ENSName,
		ConnectedAt: time.Now().UTC().Format(time.RFC3339),
	}

	// Validate and save
	if err := h.configManager.SetWeb3Wallet(wallet); err != nil {
		orihttp.BadRequest(w, err.Error())
		return
	}

	// Persist to disk
	if err := h.configManager.Save(); err != nil {
		orihttp.InternalError(w, "Failed to save wallet configuration")
		return
	}

	// Return success with wallet info
	response := struct {
		Success       bool   `json:"success"`
		Address       string `json:"address"`
		AddressMasked string `json:"address_masked"`
		ChainID       int    `json:"chain_id"`
		ChainName     string `json:"chain_name"`
		ENSName       string `json:"ens_name,omitempty"`
		ConnectedAt   string `json:"connected_at"`
	}{
		Success:       true,
		Address:       wallet.Address,
		AddressMasked: config.MaskWeb3Address(wallet.Address),
		ChainID:       wallet.ChainID,
		ChainName:     config.SupportedChains()[wallet.ChainID],
		ENSName:       wallet.ENSName,
		ConnectedAt:   wallet.ConnectedAt,
	}
	orihttp.WriteJSON(w, response)
}

// disconnectWeb3Wallet removes the Web3 wallet connection
func (h *Handler) disconnectWeb3Wallet(w http.ResponseWriter, r *http.Request) {
	h.configManager.ClearWeb3Wallet()

	// Persist to disk
	if err := h.configManager.Save(); err != nil {
		orihttp.InternalError(w, "Failed to save configuration")
		return
	}

	response := struct {
		Success   bool   `json:"success"`
		Message   string `json:"message"`
		Connected bool   `json:"connected"`
	}{
		Success:   true,
		Message:   "Wallet disconnected successfully",
		Connected: false,
	}
	orihttp.WriteJSON(w, response)
}

// Web3ChainsHandler returns the list of supported chains
func (h *Handler) Web3ChainsHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}

	chains := config.SupportedChains()

	type chainInfo struct {
		ID   int    `json:"id"`
		Name string `json:"name"`
	}

	var chainList []chainInfo
	for id, name := range chains {
		chainList = append(chainList, chainInfo{ID: id, Name: name})
	}

	response := struct {
		Chains []chainInfo `json:"chains"`
	}{
		Chains: chainList,
	}
	orihttp.WriteJSON(w, response)
}

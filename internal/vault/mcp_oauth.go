package vault

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"
)

// mcpOAuthPayload is the encrypted-at-rest payload for a RecordTypeMCPOAuth
// record. AuthRef is also stored as the record's Label so a lookup never
// needs to decrypt every candidate record; ServerName is a separate,
// purely-descriptive field carried for display/debugging.
type mcpOAuthPayload struct {
	AuthRef       string    `json:"auth_ref"`
	ServerName    string    `json:"server_name"`
	ClientID      string    `json:"client_id,omitempty"`
	ClientSecret  string    `json:"client_secret,omitempty"`
	AccessToken   string    `json:"access_token,omitempty"`
	RefreshToken  string    `json:"refresh_token,omitempty"`
	TokenType     string    `json:"token_type,omitempty"`
	TokenEndpoint string    `json:"token_endpoint,omitempty"`
	Expiry        time.Time `json:"expiry,omitempty"`
	Scopes        []string  `json:"scopes,omitempty"`
}

// MCPOAuthCredential is the decrypted OAuth credential material for a remote
// MCP server. Like EmailOAuthCredentials, it is returned only to internal
// server wiring and must never be serialized to an HTTP response, log, or
// LLM prompt.
type MCPOAuthCredential struct {
	ID            string
	AuthRef       string
	ServerName    string
	ClientID      string
	ClientSecret  string
	AccessToken   string
	RefreshToken  string
	TokenType     string
	TokenEndpoint string
	Expiry        time.Time
	Scopes        []string
}

// MCPOAuthStatus is the safe, secret-free public view of a remote MCP
// server's OAuth state.
type MCPOAuthStatus struct {
	Configured      bool      `json:"configured"` // client id/secret submitted
	Connected       bool      `json:"connected"`  // has an access or refresh token
	HasClientSecret bool      `json:"has_client_secret"`
	Expiry          time.Time `json:"expiry,omitempty"`
}

// FindMCPOAuthRecordID looks up the vault record id for a remote server's
// OAuth credential by AuthRef (stored as the record Label), without
// decrypting every mcp_oauth record's payload.
func (s *Store) FindMCPOAuthRecordID(ctx context.Context, vaultID, authRef string) (string, bool, error) {
	authRef = strings.TrimSpace(authRef)
	if authRef == "" {
		return "", false, fmt.Errorf("%w: auth ref is required", ErrRecordNotFound)
	}

	items, err := s.ListRecords(ctx, RecordFilter{
		VaultID: vaultID,
		Type:    RecordTypeMCPOAuth,
	}, AccessContext{})
	if err != nil {
		return "", false, err
	}

	for _, item := range items {
		if item.Label == authRef {
			return item.ID, true, nil
		}
	}
	return "", false, nil
}

// GetMCPOAuthStatus returns the safe, secret-free status for a remote
// server's OAuth credential, or a zero-value "not configured" status if none
// exists yet.
func (s *Store) GetMCPOAuthStatus(ctx context.Context, vaultID, authRef string) (MCPOAuthStatus, error) {
	id, ok, err := s.FindMCPOAuthRecordID(ctx, vaultID, authRef)
	if err != nil || !ok {
		return MCPOAuthStatus{}, err
	}

	record, err := s.GetRecord(ctx, id, AccessContext{})
	if err != nil {
		return MCPOAuthStatus{}, err
	}
	payload, err := decodeMCPOAuthPayload(record.Payload)
	if err != nil {
		return MCPOAuthStatus{}, err
	}

	return MCPOAuthStatus{
		Configured:      payload.ClientID != "",
		Connected:       payload.AccessToken != "" || payload.RefreshToken != "",
		HasClientSecret: payload.ClientSecret != "",
		Expiry:          payload.Expiry,
	}, nil
}

// UpsertMCPOAuthCredential creates or updates the OAuth credential for a
// remote server, keyed by (vaultID, cred.AuthRef). Returns the resulting
// record id.
func (s *Store) UpsertMCPOAuthCredential(ctx context.Context, vaultID string, cred MCPOAuthCredential) (string, error) {
	authRef := strings.TrimSpace(cred.AuthRef)
	if authRef == "" {
		return "", fmt.Errorf("%w: auth ref is required", ErrRecordNotFound)
	}

	payload := mcpOAuthPayload{
		AuthRef:       authRef,
		ServerName:    strings.TrimSpace(cred.ServerName),
		ClientID:      strings.TrimSpace(cred.ClientID),
		ClientSecret:  strings.TrimSpace(cred.ClientSecret),
		AccessToken:   strings.TrimSpace(cred.AccessToken),
		RefreshToken:  strings.TrimSpace(cred.RefreshToken),
		TokenType:     strings.TrimSpace(cred.TokenType),
		TokenEndpoint: strings.TrimSpace(cred.TokenEndpoint),
		Expiry:        cred.Expiry,
		Scopes:        append([]string{}, cred.Scopes...),
	}

	// #nosec G117 -- this JSON becomes Record.Payload, which the vault store
	// encrypts before it ever touches disk (see store_records.go); it never
	// serializes into mcp_registry.json, an HTTP response, or a log.
	data, err := json.Marshal(payload)
	if err != nil {
		return "", fmt.Errorf("marshal mcp oauth payload: %w", err)
	}
	rawPayload := json.RawMessage(data)

	id, exists, err := s.FindMCPOAuthRecordID(ctx, vaultID, authRef)
	if err != nil {
		return "", err
	}
	if exists {
		if _, err := s.UpdateRecord(ctx, id, RecordUpdate{Payload: &rawPayload}, AccessContext{}); err != nil {
			return "", err
		}
		return id, nil
	}

	record := &Record{
		VaultID: strings.TrimSpace(vaultID),
		Type:    RecordTypeMCPOAuth,
		Label:   authRef,
		Payload: rawPayload,
	}
	if err := s.CreateRecord(ctx, record, AccessContext{}); err != nil {
		return "", err
	}
	return record.ID, nil
}

// RevealMCPOAuthCredential decrypts and returns the OAuth credential
// material for a remote server. This is the single, explicit reveal path so
// token exposure is auditable and confined to server-side wiring.
func (s *Store) RevealMCPOAuthCredential(ctx context.Context, vaultID, authRef string) (*MCPOAuthCredential, bool, error) {
	id, ok, err := s.FindMCPOAuthRecordID(ctx, vaultID, authRef)
	if err != nil || !ok {
		return nil, ok, err
	}

	record, err := s.GetRecord(ctx, id, AccessContext{})
	if err != nil {
		return nil, false, err
	}
	payload, err := decodeMCPOAuthPayload(record.Payload)
	if err != nil {
		return nil, false, err
	}

	return &MCPOAuthCredential{
		ID:            record.ID,
		AuthRef:       payload.AuthRef,
		ServerName:    payload.ServerName,
		ClientID:      payload.ClientID,
		ClientSecret:  payload.ClientSecret,
		AccessToken:   payload.AccessToken,
		RefreshToken:  payload.RefreshToken,
		TokenType:     payload.TokenType,
		TokenEndpoint: payload.TokenEndpoint,
		Expiry:        payload.Expiry,
		Scopes:        append([]string{}, payload.Scopes...),
	}, true, nil
}

// DeleteMCPOAuthCredential removes a remote server's OAuth credential
// record, if any. Deleting a nonexistent record is not an error, matching
// the vault-record-delete revocation semantics used elsewhere.
func (s *Store) DeleteMCPOAuthCredential(ctx context.Context, vaultID, authRef string) error {
	id, ok, err := s.FindMCPOAuthRecordID(ctx, vaultID, authRef)
	if err != nil {
		return err
	}
	if !ok {
		return nil
	}
	return s.DeleteRecord(ctx, id, AccessContext{})
}

func decodeMCPOAuthPayload(data json.RawMessage) (mcpOAuthPayload, error) {
	var payload mcpOAuthPayload
	if len(data) == 0 {
		return payload, fmt.Errorf("%w: payload is required", ErrMalformedRecord)
	}
	if err := json.Unmarshal(data, &payload); err != nil {
		return payload, fmt.Errorf("%w: %v", ErrMalformedRecord, err)
	}
	return payload, nil
}

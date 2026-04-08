package vault

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
)

const (
	defaultIMAPSSLPort = 993
	defaultSMTPSSLPort = 465
)

type emailAccountPayload struct {
	Provider      EmailProvider `json:"provider"`
	EmailAddress  string        `json:"email_address"`
	DisplayName   string        `json:"display_name,omitempty"`
	Username      string        `json:"username,omitempty"`
	AuthType      EmailAuthType `json:"auth_type"`
	IMAPHost      string        `json:"imap_host,omitempty"`
	IMAPPort      int           `json:"imap_port,omitempty"`
	SMTPHost      string        `json:"smtp_host,omitempty"`
	SMTPPort      int           `json:"smtp_port,omitempty"`
	AccessToken   string        `json:"access_token,omitempty"`
	RefreshToken  string        `json:"refresh_token,omitempty"`
	Password      string        `json:"password,omitempty"`
	ClientID      string        `json:"client_id,omitempty"`
	ClientSecret  string        `json:"client_secret,omitempty"`
	TokenEndpoint string        `json:"token_endpoint,omitempty"`
}

func (s *Store) ListEmailAccounts(ctx context.Context, vaultID string, workspaceID string) ([]EmailAccount, error) {
	records, err := s.ListRecords(ctx, RecordFilter{
		VaultID:     vaultID,
		WorkspaceID: workspaceID,
		Type:        RecordTypeEmailAccount,
	}, AccessContext{})
	if err != nil {
		return nil, err
	}

	accounts := make([]EmailAccount, 0, len(records))
	for _, item := range records {
		account, err := s.GetEmailAccount(ctx, item.ID)
		if err != nil {
			return nil, err
		}
		if account != nil {
			accounts = append(accounts, *account)
		}
	}
	return accounts, nil
}

func (s *Store) GetEmailAccount(ctx context.Context, id string) (*EmailAccount, error) {
	record, err := s.GetRecord(ctx, id, AccessContext{})
	if err != nil {
		return nil, err
	}
	return emailAccountFromRecord(record)
}

func (s *Store) CreateEmailAccount(ctx context.Context, input EmailAccountInput) (*EmailAccount, error) {
	record, err := buildEmailAccountRecord(input)
	if err != nil {
		return nil, err
	}
	if err := s.CreateRecord(ctx, record, AccessContext{}); err != nil {
		return nil, err
	}
	return emailAccountFromRecord(record)
}

func (s *Store) UpdateEmailAccount(ctx context.Context, id string, update EmailAccountUpdate) (*EmailAccount, error) {
	record, err := s.GetRecord(ctx, id, AccessContext{})
	if err != nil {
		return nil, err
	}
	if normalizeRecordType(record.Type) != RecordTypeEmailAccount {
		return nil, ErrRecordNotFound
	}

	payload, err := decodeEmailAccountPayload(record.Payload)
	if err != nil {
		return nil, err
	}

	if update.WorkspaceID != nil {
		record.WorkspaceID = strings.TrimSpace(*update.WorkspaceID)
	}
	if update.Label != nil {
		record.Label = strings.TrimSpace(*update.Label)
	}
	if update.Tags != nil {
		record.Tags = append([]string{}, (*update.Tags)...)
	}
	if update.Source != nil {
		record.Source = strings.TrimSpace(*update.Source)
	}
	if update.RetentionPolicy != nil {
		record.RetentionPolicy = strings.TrimSpace(*update.RetentionPolicy)
	}
	if update.Provider != nil {
		payload.Provider = *update.Provider
	}
	if update.EmailAddress != nil {
		payload.EmailAddress = strings.TrimSpace(*update.EmailAddress)
	}
	if update.DisplayName != nil {
		payload.DisplayName = strings.TrimSpace(*update.DisplayName)
	}
	if update.Username != nil {
		payload.Username = strings.TrimSpace(*update.Username)
	}
	if update.AuthType != nil {
		payload.AuthType = *update.AuthType
	}
	if update.IMAPHost != nil {
		payload.IMAPHost = strings.TrimSpace(*update.IMAPHost)
	}
	if update.IMAPPort != nil {
		payload.IMAPPort = *update.IMAPPort
	}
	if update.SMTPHost != nil {
		payload.SMTPHost = strings.TrimSpace(*update.SMTPHost)
	}
	if update.SMTPPort != nil {
		payload.SMTPPort = *update.SMTPPort
	}
	if update.AccessToken != nil {
		payload.AccessToken = strings.TrimSpace(*update.AccessToken)
	}
	if update.RefreshToken != nil {
		payload.RefreshToken = strings.TrimSpace(*update.RefreshToken)
	}
	if update.Password != nil {
		payload.Password = strings.TrimSpace(*update.Password)
	}
	if update.ClientID != nil {
		payload.ClientID = strings.TrimSpace(*update.ClientID)
	}
	if update.ClientSecret != nil {
		payload.ClientSecret = strings.TrimSpace(*update.ClientSecret)
	}
	if update.TokenEndpoint != nil {
		payload.TokenEndpoint = strings.TrimSpace(*update.TokenEndpoint)
	}

	normalizedRecord, normalizedPayload, err := normalizeEmailAccountRecord(*record, payload)
	if err != nil {
		return nil, err
	}
	data, err := json.Marshal(normalizedPayload)
	if err != nil {
		return nil, fmt.Errorf("marshal email account payload: %w", err)
	}
	rawPayload := json.RawMessage(data)

	label := normalizedRecord.Label
	workspaceID := normalizedRecord.WorkspaceID
	tags := append([]string{}, normalizedRecord.Tags...)
	source := normalizedRecord.Source
	retentionPolicy := normalizedRecord.RetentionPolicy
	recordType := normalizedRecord.Type

	updated, err := s.UpdateRecord(ctx, id, RecordUpdate{
		Type:            &recordType,
		WorkspaceID:     &workspaceID,
		Label:           &label,
		Tags:            &tags,
		Source:          &source,
		RetentionPolicy: &retentionPolicy,
		Payload:         &rawPayload,
	}, AccessContext{})
	if err != nil {
		return nil, err
	}
	return emailAccountFromRecord(updated)
}

func (s *Store) DeleteEmailAccount(ctx context.Context, id string) error {
	if _, err := s.GetEmailAccount(ctx, id); err != nil {
		return err
	}
	return s.DeleteRecord(ctx, id, AccessContext{})
}

func buildEmailAccountRecord(input EmailAccountInput) (*Record, error) {
	record := Record{
		VaultID:         strings.TrimSpace(input.VaultID),
		WorkspaceID:     strings.TrimSpace(input.WorkspaceID),
		Type:            RecordTypeEmailAccount,
		Label:           strings.TrimSpace(input.Label),
		Tags:            append([]string{}, input.Tags...),
		Source:          strings.TrimSpace(input.Source),
		RetentionPolicy: strings.TrimSpace(input.RetentionPolicy),
	}
	payload := emailAccountPayload{
		Provider:      input.Provider,
		EmailAddress:  strings.TrimSpace(input.EmailAddress),
		DisplayName:   strings.TrimSpace(input.DisplayName),
		Username:      strings.TrimSpace(input.Username),
		AuthType:      input.AuthType,
		IMAPHost:      strings.TrimSpace(input.IMAPHost),
		IMAPPort:      input.IMAPPort,
		SMTPHost:      strings.TrimSpace(input.SMTPHost),
		SMTPPort:      input.SMTPPort,
		AccessToken:   strings.TrimSpace(input.Credentials.AccessToken),
		RefreshToken:  strings.TrimSpace(input.Credentials.RefreshToken),
		Password:      strings.TrimSpace(input.Credentials.Password),
		ClientID:      strings.TrimSpace(input.Credentials.ClientID),
		ClientSecret:  strings.TrimSpace(input.Credentials.ClientSecret),
		TokenEndpoint: strings.TrimSpace(input.Credentials.TokenEndpoint),
	}

	normalizedRecord, normalizedPayload, err := normalizeEmailAccountRecord(record, payload)
	if err != nil {
		return nil, err
	}

	data, err := json.Marshal(normalizedPayload)
	if err != nil {
		return nil, fmt.Errorf("marshal email account payload: %w", err)
	}
	normalizedRecord.Payload = data
	return &normalizedRecord, nil
}

func normalizeEmailAccountRecord(record Record, payload emailAccountPayload) (Record, emailAccountPayload, error) {
	payload.Provider = normalizeEmailProvider(payload.Provider)
	payload.AuthType = normalizeEmailAuthType(payload.AuthType)
	payload.EmailAddress = strings.TrimSpace(payload.EmailAddress)
	payload.DisplayName = strings.TrimSpace(payload.DisplayName)
	payload.Username = strings.TrimSpace(payload.Username)
	payload.IMAPHost = strings.TrimSpace(payload.IMAPHost)
	payload.SMTPHost = strings.TrimSpace(payload.SMTPHost)
	payload.AccessToken = strings.TrimSpace(payload.AccessToken)
	payload.RefreshToken = strings.TrimSpace(payload.RefreshToken)
	payload.Password = strings.TrimSpace(payload.Password)
	payload.ClientID = strings.TrimSpace(payload.ClientID)
	payload.ClientSecret = strings.TrimSpace(payload.ClientSecret)
	payload.TokenEndpoint = strings.TrimSpace(payload.TokenEndpoint)

	if payload.Provider == "" {
		return record, payload, fmt.Errorf("%w: provider is required", ErrInvalidEmailAccount)
	}
	if payload.AuthType == "" {
		return record, payload, fmt.Errorf("%w: auth_type is required", ErrInvalidEmailAccount)
	}
	if payload.EmailAddress == "" || !strings.Contains(payload.EmailAddress, "@") {
		return record, payload, fmt.Errorf("%w: email_address must be a valid email", ErrInvalidEmailAccount)
	}
	if payload.Username == "" {
		payload.Username = payload.EmailAddress
	}

	switch payload.Provider {
	case EmailProviderGmail:
		if payload.IMAPHost == "" {
			payload.IMAPHost = "imap.gmail.com"
		}
		if payload.SMTPHost == "" {
			payload.SMTPHost = "smtp.gmail.com"
		}
	case EmailProviderMicrosoft:
		if payload.IMAPHost == "" {
			payload.IMAPHost = "outlook.office365.com"
		}
		if payload.SMTPHost == "" {
			payload.SMTPHost = "smtp.office365.com"
		}
	case EmailProviderIMAPSMTP:
		if payload.IMAPHost == "" || payload.SMTPHost == "" {
			return record, payload, fmt.Errorf("%w: imap_host and smtp_host are required for imap_smtp accounts", ErrInvalidEmailAccount)
		}
	default:
		return record, payload, fmt.Errorf("%w: unsupported provider %q", ErrInvalidEmailAccount, payload.Provider)
	}

	if payload.IMAPPort == 0 {
		payload.IMAPPort = defaultIMAPSSLPort
	}
	if payload.SMTPPort == 0 {
		payload.SMTPPort = defaultSMTPSSLPort
	}

	switch payload.AuthType {
	case EmailAuthTypeOAuth2:
		if payload.AccessToken == "" && payload.RefreshToken == "" {
			return record, payload, fmt.Errorf("%w: oauth2 accounts require access_token or refresh_token", ErrInvalidEmailAccount)
		}
	case EmailAuthTypePassword, EmailAuthTypeAppPassword:
		if payload.Password == "" {
			return record, payload, fmt.Errorf("%w: password is required for %s auth", ErrInvalidEmailAccount, payload.AuthType)
		}
	default:
		return record, payload, fmt.Errorf("%w: unsupported auth_type %q", ErrInvalidEmailAccount, payload.AuthType)
	}

	record.Type = RecordTypeEmailAccount
	record.WorkspaceID = strings.TrimSpace(record.WorkspaceID)
	record.Label = strings.TrimSpace(record.Label)
	if record.Label == "" {
		record.Label = payload.EmailAddress
	}
	record.Tags = normalizeTags(record.Tags)
	record.Source = strings.TrimSpace(record.Source)
	record.RetentionPolicy = strings.TrimSpace(record.RetentionPolicy)
	return record, payload, nil
}

func emailAccountFromRecord(record *Record) (*EmailAccount, error) {
	if record == nil {
		return nil, ErrRecordNotFound
	}
	if normalizeRecordType(record.Type) != RecordTypeEmailAccount {
		return nil, ErrRecordNotFound
	}

	payload, err := decodeEmailAccountPayload(record.Payload)
	if err != nil {
		return nil, err
	}

	state := EmailAccountSecretState{
		HasAccessToken:  payload.AccessToken != "",
		HasRefreshToken: payload.RefreshToken != "",
		HasPassword:     payload.Password != "",
		HasClientID:     payload.ClientID != "",
		HasClientSecret: payload.ClientSecret != "",
	}

	return &EmailAccount{
		ID:                record.ID,
		VaultID:           record.VaultID,
		WorkspaceID:       record.WorkspaceID,
		Label:             record.Label,
		Tags:              append([]string{}, record.Tags...),
		Source:            record.Source,
		RetentionPolicy:   record.RetentionPolicy,
		Provider:          payload.Provider,
		EmailAddress:      payload.EmailAddress,
		DisplayName:       payload.DisplayName,
		Username:          payload.Username,
		AuthType:          payload.AuthType,
		IMAPHost:          payload.IMAPHost,
		IMAPPort:          payload.IMAPPort,
		SMTPHost:          payload.SMTPHost,
		SMTPPort:          payload.SMTPPort,
		HasAccessToken:    state.HasAccessToken,
		HasRefreshToken:   state.HasRefreshToken,
		HasPassword:       state.HasPassword,
		CreatedAt:         record.CreatedAt,
		UpdatedAt:         record.UpdatedAt,
		CredentialsStatus: state,
	}, nil
}

func decodeEmailAccountPayload(data json.RawMessage) (emailAccountPayload, error) {
	var payload emailAccountPayload
	if len(data) == 0 {
		return payload, fmt.Errorf("%w: payload is required", ErrInvalidEmailAccount)
	}
	if err := json.Unmarshal(data, &payload); err != nil {
		return payload, fmt.Errorf("%w: %v", ErrInvalidEmailAccount, err)
	}
	return payload, nil
}

func normalizeEmailProvider(provider EmailProvider) EmailProvider {
	switch strings.ToLower(strings.TrimSpace(string(provider))) {
	case "gmail":
		return EmailProviderGmail
	case "microsoft", "microsoft_mail", "microsoft-mail", "outlook", "outlook_mail", "outlook-mail":
		return EmailProviderMicrosoft
	case "imap_smtp", "imap-smtp", "imap", "smtp":
		return EmailProviderIMAPSMTP
	default:
		return EmailProvider(strings.ToLower(strings.TrimSpace(string(provider))))
	}
}

func normalizeEmailAuthType(authType EmailAuthType) EmailAuthType {
	switch strings.ToLower(strings.TrimSpace(string(authType))) {
	case "oauth2":
		return EmailAuthTypeOAuth2
	case "password":
		return EmailAuthTypePassword
	case "app_password", "app-password":
		return EmailAuthTypeAppPassword
	default:
		return EmailAuthType(strings.ToLower(strings.TrimSpace(string(authType))))
	}
}

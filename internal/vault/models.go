package vault

import (
	"encoding/json"
	"errors"
	"strings"
	"time"
)

var (
	ErrVaultRequired            = errors.New("vault: vault selection is required")
	ErrVaultNotFound            = errors.New("vault: vault not found")
	ErrVaultAlreadyExists       = errors.New("vault: vault already exists")
	ErrVaultNameRequired        = errors.New("vault: vault name is required")
	ErrRecordNotFound           = errors.New("vault: record not found")
	ErrGrantNotFound            = errors.New("vault: grant not found")
	ErrPermissionDenied         = errors.New("vault: permission denied")
	ErrInvalidEmailAccount      = errors.New("vault: invalid email account")
	ErrVaultLocked              = errors.New("vault: vault locked")
	ErrVaultKeyUnavailable      = errors.New("vault: data encryption key unavailable")
	ErrMalformedRecord          = errors.New("vault: malformed encrypted record")
	ErrVaultPasswordRequired    = errors.New("vault: vault password is required")
	ErrVaultPasswordInvalid     = errors.New("vault: incorrect vault password")
	ErrExportPasswordEmpty      = errors.New("vault: export password is required")
	ErrImportPasswordRequired   = errors.New("vault: import password is required")
	ErrImportPasswordInvalid    = errors.New("vault: incorrect import password or corrupted bundle")
	ErrImportBundleRequired     = errors.New("vault: import bundle is required")
	ErrImportBundleInvalid      = errors.New("vault: import bundle is invalid")
	ErrImportTargetRequired     = errors.New("vault: import target is required")
	ErrFolderPathInvalid        = errors.New("vault: folder path is invalid")
	ErrFolderNotEmpty           = errors.New("vault: folder is not empty")
	ErrRecordAttachmentNotFound = errors.New("vault: record attachment not found")
	ErrRecordAttachmentRequired = errors.New("vault: attachment content is required")
	ErrRecordAttachmentTooLarge = errors.New("vault: attachment exceeds maximum size")
)

const DefaultVaultID = "default"
const RecordTypeEmailAccount = "email_account"

type ActorType string

const (
	ActorTypeUser   ActorType = "user"
	ActorTypeAgent  ActorType = "agent"
	ActorTypePlugin ActorType = "plugin"
)

type Capability string

const (
	CapabilitySecretsRead   Capability = "vault.secrets.read"
	CapabilitySecretsWrite  Capability = "vault.secrets.write"
	CapabilityPersonalRead  Capability = "vault.personal.read"
	CapabilityPersonalWrite Capability = "vault.personal.write"
	CapabilityEmailRead     Capability = "vault.email.read_saved"
	CapabilityEmailWrite    Capability = "vault.email.write_saved"
)

type AccessContext struct {
	WorkspaceID string    `json:"workspace_id,omitempty"`
	ActorType   ActorType `json:"actor_type,omitempty"`
	ActorID     string    `json:"actor_id,omitempty"`
}

type Vault struct {
	ID                string    `json:"id"`
	Name              string    `json:"name"`
	Description       string    `json:"description,omitempty"`
	IsDefault         bool      `json:"is_default"`
	PasswordProtected bool      `json:"password_protected"`
	RecordCount       int       `json:"record_count"`
	CreatedAt         time.Time `json:"created_at"`
	UpdatedAt         time.Time `json:"updated_at"`
}

type Record struct {
	ID              string          `json:"id"`
	VaultID         string          `json:"vault_id,omitempty"`
	Type            string          `json:"type"`
	WorkspaceID     string          `json:"workspace_id,omitempty"`
	FolderPath      string          `json:"folder_path,omitempty"`
	Label           string          `json:"label"`
	Tags            []string        `json:"tags,omitempty"`
	Source          string          `json:"source,omitempty"`
	RetentionPolicy string          `json:"retention_policy,omitempty"`
	Payload         json.RawMessage `json:"payload"`
	CreatedAt       time.Time       `json:"created_at"`
	UpdatedAt       time.Time       `json:"updated_at"`
}

type RecordUpdate struct {
	Type            *string          `json:"type,omitempty"`
	WorkspaceID     *string          `json:"workspace_id,omitempty"`
	FolderPath      *string          `json:"folder_path,omitempty"`
	Label           *string          `json:"label,omitempty"`
	Tags            *[]string        `json:"tags,omitempty"`
	Source          *string          `json:"source,omitempty"`
	RetentionPolicy *string          `json:"retention_policy,omitempty"`
	Payload         *json.RawMessage `json:"payload,omitempty"`
}

type RecordAttachment struct {
	ID            string    `json:"id"`
	RecordID      string    `json:"record_id,omitempty"`
	VaultID       string    `json:"vault_id,omitempty"`
	Name          string    `json:"name"`
	MimeType      string    `json:"mime_type"`
	SizeBytes     int64     `json:"size_bytes"`
	Kind          string    `json:"kind,omitempty"`
	DownloadURL   string    `json:"download_url,omitempty"`
	ContentBase64 string    `json:"content_base64,omitempty"`
	CreatedAt     time.Time `json:"created_at,omitempty"`
	UpdatedAt     time.Time `json:"updated_at,omitempty"`
}

type RecordListItem struct {
	ID              string    `json:"id"`
	VaultID         string    `json:"vault_id,omitempty"`
	Type            string    `json:"type"`
	WorkspaceID     string    `json:"workspace_id,omitempty"`
	FolderPath      string    `json:"folder_path,omitempty"`
	Label           string    `json:"label"`
	Tags            []string  `json:"tags,omitempty"`
	Source          string    `json:"source,omitempty"`
	RetentionPolicy string    `json:"retention_policy,omitempty"`
	CreatedAt       time.Time `json:"created_at"`
	UpdatedAt       time.Time `json:"updated_at"`
}

type Folder struct {
	ID        string    `json:"id"`
	VaultID   string    `json:"vault_id,omitempty"`
	Path      string    `json:"path"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

type EmailProvider string

const (
	EmailProviderGmail     EmailProvider = "gmail"
	EmailProviderMicrosoft EmailProvider = "microsoft"
	EmailProviderIMAPSMTP  EmailProvider = "imap_smtp"
)

type EmailAuthType string

const (
	EmailAuthTypeOAuth2      EmailAuthType = "oauth2"
	EmailAuthTypePassword    EmailAuthType = "password"
	EmailAuthTypeAppPassword EmailAuthType = "app_password"
)

type EmailAccountCredentials struct {
	AccessToken   string `json:"access_token,omitempty"`
	RefreshToken  string `json:"refresh_token,omitempty"`
	Password      string `json:"password,omitempty"`
	ClientID      string `json:"client_id,omitempty"`
	ClientSecret  string `json:"client_secret,omitempty"`
	TokenEndpoint string `json:"token_endpoint,omitempty"`
}

type EmailAccount struct {
	ID                string                  `json:"id"`
	VaultID           string                  `json:"vault_id,omitempty"`
	WorkspaceID       string                  `json:"workspace_id,omitempty"`
	Label             string                  `json:"label"`
	Tags              []string                `json:"tags,omitempty"`
	Source            string                  `json:"source,omitempty"`
	RetentionPolicy   string                  `json:"retention_policy,omitempty"`
	Provider          EmailProvider           `json:"provider"`
	EmailAddress      string                  `json:"email_address"`
	DisplayName       string                  `json:"display_name,omitempty"`
	Username          string                  `json:"username,omitempty"`
	AuthType          EmailAuthType           `json:"auth_type"`
	IMAPHost          string                  `json:"imap_host,omitempty"`
	IMAPPort          int                     `json:"imap_port,omitempty"`
	SMTPHost          string                  `json:"smtp_host,omitempty"`
	SMTPPort          int                     `json:"smtp_port,omitempty"`
	HasAccessToken    bool                    `json:"has_access_token,omitempty"`
	HasRefreshToken   bool                    `json:"has_refresh_token,omitempty"`
	HasPassword       bool                    `json:"has_password,omitempty"`
	CreatedAt         time.Time               `json:"created_at"`
	UpdatedAt         time.Time               `json:"updated_at"`
	CredentialsStatus EmailAccountSecretState `json:"credentials_status"`
}

type EmailAccountSecretState struct {
	HasAccessToken  bool `json:"has_access_token,omitempty"`
	HasRefreshToken bool `json:"has_refresh_token,omitempty"`
	HasPassword     bool `json:"has_password,omitempty"`
	HasClientID     bool `json:"has_client_id,omitempty"`
	HasClientSecret bool `json:"has_client_secret,omitempty"`
}

type EmailAccountInput struct {
	VaultID         string                  `json:"vault_id,omitempty"`
	WorkspaceID     string                  `json:"workspace_id,omitempty"`
	Label           string                  `json:"label,omitempty"`
	Tags            []string                `json:"tags,omitempty"`
	Source          string                  `json:"source,omitempty"`
	RetentionPolicy string                  `json:"retention_policy,omitempty"`
	Provider        EmailProvider           `json:"provider"`
	EmailAddress    string                  `json:"email_address"`
	DisplayName     string                  `json:"display_name,omitempty"`
	Username        string                  `json:"username,omitempty"`
	AuthType        EmailAuthType           `json:"auth_type"`
	IMAPHost        string                  `json:"imap_host,omitempty"`
	IMAPPort        int                     `json:"imap_port,omitempty"`
	SMTPHost        string                  `json:"smtp_host,omitempty"`
	SMTPPort        int                     `json:"smtp_port,omitempty"`
	Credentials     EmailAccountCredentials `json:"credentials"`
}

type EmailAccountUpdate struct {
	WorkspaceID     *string        `json:"workspace_id,omitempty"`
	Label           *string        `json:"label,omitempty"`
	Tags            *[]string      `json:"tags,omitempty"`
	Source          *string        `json:"source,omitempty"`
	RetentionPolicy *string        `json:"retention_policy,omitempty"`
	Provider        *EmailProvider `json:"provider,omitempty"`
	EmailAddress    *string        `json:"email_address,omitempty"`
	DisplayName     *string        `json:"display_name,omitempty"`
	Username        *string        `json:"username,omitempty"`
	AuthType        *EmailAuthType `json:"auth_type,omitempty"`
	IMAPHost        *string        `json:"imap_host,omitempty"`
	IMAPPort        *int           `json:"imap_port,omitempty"`
	SMTPHost        *string        `json:"smtp_host,omitempty"`
	SMTPPort        *int           `json:"smtp_port,omitempty"`
	AccessToken     *string        `json:"access_token,omitempty"`
	RefreshToken    *string        `json:"refresh_token,omitempty"`
	Password        *string        `json:"password,omitempty"`
	ClientID        *string        `json:"client_id,omitempty"`
	ClientSecret    *string        `json:"client_secret,omitempty"`
	TokenEndpoint   *string        `json:"token_endpoint,omitempty"`
}

type RecordFilter struct {
	VaultID     string
	WorkspaceID string
	Type        string
}

type Grant struct {
	ID          string     `json:"id"`
	VaultID     string     `json:"vault_id,omitempty"`
	WorkspaceID string     `json:"workspace_id"`
	ActorType   ActorType  `json:"actor_type"`
	ActorID     string     `json:"actor_id"`
	Capability  Capability `json:"capability"`
	RecordType  string     `json:"record_type,omitempty"`
	CreatedAt   time.Time  `json:"created_at"`
	UpdatedAt   time.Time  `json:"updated_at"`
}

type AuditEvent struct {
	ID          string    `json:"id"`
	VaultID     string    `json:"vault_id,omitempty"`
	WorkspaceID string    `json:"workspace_id,omitempty"`
	ActorType   ActorType `json:"actor_type,omitempty"`
	ActorID     string    `json:"actor_id,omitempty"`
	Action      string    `json:"action"`
	RecordID    string    `json:"record_id,omitempty"`
	RecordType  string    `json:"record_type,omitempty"`
	Outcome     string    `json:"outcome"`
	Details     string    `json:"details,omitempty"`
	CreatedAt   time.Time `json:"created_at"`
}

type VaultStatus struct {
	VaultID            string      `json:"vault_id,omitempty"`
	VaultName          string      `json:"vault_name,omitempty"`
	Available          bool        `json:"available"`
	Locked             bool        `json:"locked"`
	Writable           bool        `json:"writable"`
	PasswordProtected  bool        `json:"password_protected"`
	RequiresPassphrase bool        `json:"requires_passphrase"`
	Message            string      `json:"message,omitempty"`
	RecordCount        int         `json:"record_count"`
	SecretStore        StoreStatus `json:"secret_store"`
}

type ExportRequest struct {
	VaultID     string
	WorkspaceID string
	Password    string
}

type ExportBundle struct {
	Version     int       `json:"version"`
	VaultID     string    `json:"vault_id,omitempty"`
	VaultName   string    `json:"vault_name,omitempty"`
	WorkspaceID string    `json:"workspace_id,omitempty"`
	Salt        string    `json:"salt"`
	Nonce       string    `json:"nonce"`
	Ciphertext  string    `json:"ciphertext"`
	ExportedAt  time.Time `json:"exported_at"`
	RecordCount int       `json:"record_count"`
	GrantCount  int       `json:"grant_count"`
}

type ImportRequest struct {
	TargetVaultID       string
	Password            string
	Bundle              ExportBundle
	NewVaultName        string
	NewVaultDescription string
	NewVaultPassword    string
	RestoreGrants       bool
}

type ImportResult struct {
	Vault           Vault  `json:"vault"`
	CreatedVault    bool   `json:"created_vault"`
	SourceVaultName string `json:"source_vault_name,omitempty"`
	RecordCount     int    `json:"record_count"`
	GrantCount      int    `json:"grant_count"`
}

func (a AccessContext) normalized() AccessContext {
	a.WorkspaceID = strings.TrimSpace(a.WorkspaceID)
	a.ActorID = strings.TrimSpace(a.ActorID)
	a.ActorType = normalizeActorType(a.ActorType)
	return a
}

func (a AccessContext) requiresGrant() bool {
	a = a.normalized()
	if a.ActorID != "" {
		return true
	}
	return a.ActorType != "" && a.ActorType != ActorTypeUser
}

func normalizeActorType(actorType ActorType) ActorType {
	switch ActorType(strings.ToLower(strings.TrimSpace(string(actorType)))) {
	case ActorTypeAgent:
		return ActorTypeAgent
	case ActorTypePlugin:
		return ActorTypePlugin
	case ActorTypeUser:
		return ActorTypeUser
	default:
		return ActorType(strings.ToLower(strings.TrimSpace(string(actorType))))
	}
}

func normalizeVaultID(vaultID string) string {
	return strings.TrimSpace(vaultID)
}

func normalizeCapability(cap Capability) Capability {
	return Capability(strings.ToLower(strings.TrimSpace(string(cap))))
}

func normalizeRecordType(recordType string) string {
	recordType = strings.ToLower(strings.TrimSpace(recordType))
	if recordType == "" {
		return "*"
	}
	return recordType
}

func normalizeTags(tags []string) []string {
	if len(tags) == 0 {
		return nil
	}

	seen := make(map[string]struct{}, len(tags))
	normalized := make([]string, 0, len(tags))
	for _, tag := range tags {
		tag = strings.TrimSpace(tag)
		if tag == "" {
			continue
		}
		tag = strings.ToLower(tag)
		if _, ok := seen[tag]; ok {
			continue
		}
		seen[tag] = struct{}{}
		normalized = append(normalized, tag)
	}
	if len(normalized) == 0 {
		return nil
	}
	return normalized
}

func capabilitiesForRecordType(recordType string) (Capability, Capability) {
	switch normalizeRecordType(recordType) {
	case "secret", "credential", "credentials", "token", "api_key", "oauth_token", RecordTypeEmailAccount:
		return CapabilitySecretsRead, CapabilitySecretsWrite
	case "email", "email_snippet", "email_address":
		return CapabilityEmailRead, CapabilityEmailWrite
	default:
		return CapabilityPersonalRead, CapabilityPersonalWrite
	}
}

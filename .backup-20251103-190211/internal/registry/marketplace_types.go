package registry

import "time"

// MarketplacePlugin extends PluginInfo with decentralized marketplace fields
type MarketplacePlugin struct {
	// === Core Plugin Info (existing fields) ===
	Name        string   `json:"name"`
	Version     string   `json:"version"`
	Description string   `json:"description"`
	Author      string   `json:"author"`
	Path        string   `json:"path"`
	Platform    string   `json:"platform"`
	Tags        []string `json:"tags,omitempty"`

	// === Identity & Ownership ===
	PluginID          string  `json:"plugin_id"`                     // UUID or content-based hash
	DeveloperAddress  string  `json:"developer_address"`             // Wallet address of creator
	PublisherAddress  string  `json:"publisher_address,omitempty"`   // May differ from developer
	SmartContractAddr string  `json:"smart_contract_addr,omitempty"` // Contract managing this plugin
	NFTTokenID        *uint64 `json:"nft_token_id,omitempty"`        // If using NFT ownership model

	// === Pricing & Economics ===
	PricingModel string        `json:"pricing_model"` // "free", "one-time", "subscription", "usage-based"
	Price        *Price        `json:"price,omitempty"`
	Currency     string        `json:"currency,omitempty"`       // "ETH", "SOL", "USDC", "MATIC"
	RevenueShare []RevenueShare `json:"revenue_share,omitempty"` // Payment splits

	// === Distribution & Storage ===
	IPFSHash       string `json:"ipfs_hash,omitempty"`         // Content-addressed binary
	ArweaveID      string `json:"arweave_id,omitempty"`        // Permanent storage
	ChecksumSHA256 string `json:"checksum_sha256"`             // Binary verification
	SourceCodeRepo string `json:"source_code_repo,omitempty"`  // Optional open source link
	BinarySize     int64  `json:"binary_size,omitempty"`       // Bytes

	// === Licensing & Access Control ===
	LicenseType     string  `json:"license_type"`                   // "proprietary", "MIT", "GPL-3.0", "commercial"
	AccessControl   string  `json:"access_control"`                 // "public", "token-gated", "nft-holder", "paid"
	MinTokenBalance *uint64 `json:"min_token_balance,omitempty"`    // For token-gated access

	// === Verification & Trust ===
	CodeAudited        bool      `json:"code_audited"`
	AuditReport        string    `json:"audit_report,omitempty"`        // IPFS hash or URL
	AuditedAt          *time.Time `json:"audited_at,omitempty"`
	SecurityScore      int       `json:"security_score,omitempty"`      // 0-100 automated scan
	DeveloperVerified  bool      `json:"developer_verified"`            // KYC/identity verified
	SignaturePublicKey string    `json:"signature_public_key,omitempty"` // For code signing
	Signature          string    `json:"signature,omitempty"`           // Signed checksum

	// === Discovery & Marketing ===
	Category       []string `json:"category,omitempty"`        // ["music", "devops", "ai", "security"]
	Screenshots    []string `json:"screenshots,omitempty"`     // IPFS hashes
	DemoVideo      string   `json:"demo_video,omitempty"`      // IPFS hash or URL
	Website        string   `json:"website,omitempty"`
	Documentation  string   `json:"documentation,omitempty"`   // IPFS hash or URL
	SupportContact string   `json:"support_contact,omitempty"` // Email or support URL

	// === Social Proof & Metrics ===
	Downloads    uint64  `json:"downloads"`
	ActiveUsers  uint64  `json:"active_users,omitempty"`
	Rating       float32 `json:"rating,omitempty"`       // 0.0-5.0 stars
	ReviewCount  uint32  `json:"review_count,omitempty"`
	CreatedAt    time.Time `json:"created_at"`
	LastUpdated  time.Time `json:"last_updated"`

	// === Versioning & Updates ===
	VersionHistory  []PluginVersion `json:"version_history,omitempty"`
	UpdatePolicy    string          `json:"update_policy,omitempty"`    // "free-forever", "subscription-required", "paid-upgrade"
	DeprecationDate *time.Time      `json:"deprecation_date,omitempty"`
	ChangelogURI    string          `json:"changelog_uri,omitempty"`    // IPFS hash or URL

	// === Dependencies & Compatibility ===
	Dependencies    []PluginDependency `json:"dependencies,omitempty"`
	MinAgentVersion string             `json:"min_agent_version,omitempty"`
	MaxAgentVersion string             `json:"max_agent_version,omitempty"`
	Platforms       []string           `json:"platforms,omitempty"` // ["darwin-amd64", "linux-amd64", "windows-amd64"]

	// === Blockchain Metadata ===
	ChainID       uint64    `json:"chain_id,omitempty"`        // 1=Ethereum, 137=Polygon, etc.
	MintedAt      *time.Time `json:"minted_at,omitempty"`
	MintTxHash    string    `json:"mint_tx_hash,omitempty"`    // Transaction hash of initial mint
	LastUpdateTx  string    `json:"last_update_tx,omitempty"`  // Last on-chain update
	TotalSales    uint64    `json:"total_sales,omitempty"`
	TotalRevenue  string    `json:"total_revenue,omitempty"`   // In smallest unit (wei, lamports, etc.)

	// === Featured & Editorial ===
	Featured      bool   `json:"featured,omitempty"`
	EditorChoice  bool   `json:"editor_choice,omitempty"`
	FeaturedUntil *time.Time `json:"featured_until,omitempty"`
}

// Price represents plugin pricing information
type Price struct {
	Amount   string `json:"amount"`   // Use string to avoid float precision (e.g., "0.05")
	Currency string `json:"currency"` // "ETH", "USDC", "SOL", "MATIC"
	USD      string `json:"usd,omitempty"` // USD equivalent at time of pricing
}

// RevenueShare defines payment distribution
type RevenueShare struct {
	Address    string `json:"address"`              // Wallet address
	Percentage uint8  `json:"percentage"`           // 0-100
	Reason     string `json:"reason,omitempty"`     // "developer", "dependency", "platform-fee"
}

// PluginVersion represents a version in the history
type PluginVersion struct {
	Version      string    `json:"version"`
	IPFSHash     string    `json:"ipfs_hash"`
	ChecksumSHA256 string  `json:"checksum_sha256"`
	ReleasedAt   time.Time `json:"released_at"`
	ChangelogURI string    `json:"changelog_uri,omitempty"`
	ReleaseNotes string    `json:"release_notes,omitempty"`
	Deprecated   bool      `json:"deprecated,omitempty"`
}

// PluginDependency represents a dependency on another plugin
type PluginDependency struct {
	PluginID     string `json:"plugin_id"`                 // UUID of dependency
	Name         string `json:"name"`                      // Human-readable name
	MinVersion   string `json:"min_version"`               // Semantic version
	MaxVersion   string `json:"max_version,omitempty"`     // Optional max version
	Required     bool   `json:"required"`                  // True if plugin won't work without it
	RevenueShare uint8  `json:"revenue_share,omitempty"`   // % of revenue to send to dependency (0-100)
}

// PluginReview represents a user review
type PluginReview struct {
	ReviewID     string    `json:"review_id"`
	PluginID     string    `json:"plugin_id"`
	UserAddress  string    `json:"user_address"`              // Wallet address (anonymized if needed)
	Username     string    `json:"username,omitempty"`        // Optional display name
	Rating       uint8     `json:"rating"`                    // 1-5 stars
	Title        string    `json:"title,omitempty"`
	Comment      string    `json:"comment,omitempty"`
	CreatedAt    time.Time `json:"created_at"`
	Verified     bool      `json:"verified"`                  // True if user actually purchased/used
	HelpfulCount uint32    `json:"helpful_count,omitempty"`   // Upvotes
	Response     string    `json:"response,omitempty"`        // Developer response
}

// DeveloperProfile represents a plugin developer's public profile
type DeveloperProfile struct {
	Address           string    `json:"address"`                     // Wallet address
	Username          string    `json:"username,omitempty"`
	Bio               string    `json:"bio,omitempty"`
	Avatar            string    `json:"avatar,omitempty"`            // IPFS hash
	Website           string    `json:"website,omitempty"`
	Email             string    `json:"email,omitempty"`             // Optional, encrypted
	Verified          bool      `json:"verified"`                    // KYC verified
	JoinedAt          time.Time `json:"joined_at"`
	TotalPlugins      uint32    `json:"total_plugins"`
	TotalDownloads    uint64    `json:"total_downloads"`
	AverageRating     float32   `json:"average_rating"`
	SecurityIncidents uint32    `json:"security_incidents,omitempty"`
	BadgeLevel        string    `json:"badge_level,omitempty"`       // "bronze", "silver", "gold", "diamond"
	SocialLinks       map[string]string `json:"social_links,omitempty"` // twitter, github, discord, etc.
}

// Purchase represents a plugin purchase record (can be stored on-chain or off-chain)
type Purchase struct {
	PurchaseID   string    `json:"purchase_id"`
	PluginID     string    `json:"plugin_id"`
	BuyerAddress string    `json:"buyer_address"`
	Price        Price     `json:"price"`
	PurchasedAt  time.Time `json:"purchased_at"`
	TxHash       string    `json:"tx_hash,omitempty"`      // Blockchain transaction
	LicenseKey   string    `json:"license_key,omitempty"`  // If using traditional licensing
	ExpiresAt    *time.Time `json:"expires_at,omitempty"`  // For subscriptions
}

// MarketplaceStats represents overall marketplace statistics
type MarketplaceStats struct {
	TotalPlugins      uint32    `json:"total_plugins"`
	TotalDevelopers   uint32    `json:"total_developers"`
	TotalDownloads    uint64    `json:"total_downloads"`
	TotalTransactions uint64    `json:"total_transactions"`
	TotalVolume       string    `json:"total_volume"`         // In platform currency
	TopPlugins        []string  `json:"top_plugins"`          // Plugin IDs
	TrendingPlugins   []string  `json:"trending_plugins"`
	UpdatedAt         time.Time `json:"updated_at"`
}

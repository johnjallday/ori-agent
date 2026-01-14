package privateservices

import (
	"context"
	"errors"
	"time"
)

// ErrPrivateServicesDisabled is returned when private services are unavailable.
var ErrPrivateServicesDisabled = errors.New("private services disabled")

// EligibilityStatus represents the eligibility state for private service features.
type EligibilityStatus string

const (
	EligibilityUnknown    EligibilityStatus = "unknown"
	EligibilityEligible   EligibilityStatus = "eligible"
	EligibilityIneligible EligibilityStatus = "ineligible"
)

// EligibilityResult represents the eligibility decision for a wallet.
type EligibilityResult struct {
	Status     EligibilityStatus
	Reason     string
	NFTTokenID string
}

// CreditsBalance represents a user's current credit balance.
type CreditsBalance struct {
	Balance   int64
	UpdatedAt time.Time
}

// CreditsGrant represents a daily credit grant result.
type CreditsGrant struct {
	Amount    int64
	GrantedAt time.Time
}

// CashoutReceipt represents a cashout request result.
type CashoutReceipt struct {
	RequestID   string
	Amount      int64
	Status      string
	RequestedAt time.Time
}

// Client defines the integration contract for private Ori services.
type Client interface {
	Provider
	CheckEligibility(ctx context.Context, walletAddress string) (EligibilityResult, error)
	GrantDailyCredits(ctx context.Context, walletAddress string) (CreditsGrant, error)
	GetCreditsBalance(ctx context.Context, walletAddress string) (CreditsBalance, error)
	RequestCashout(ctx context.Context, walletAddress string, amount int64) (CashoutReceipt, error)
}

// NewNoopClient returns a client that disables private-service operations.
func NewNoopClient() Client {
	return NoopClient{}
}

// NoopClient implements Client with disabled operations.
type NoopClient struct{}

// Capabilities returns the disabled capability set.
func (NoopClient) Capabilities() Capabilities {
	return NoopProvider{}.Capabilities()
}

// CheckEligibility returns a disabled response.
func (NoopClient) CheckEligibility(ctx context.Context, walletAddress string) (EligibilityResult, error) {
	return EligibilityResult{Status: EligibilityUnknown}, ErrPrivateServicesDisabled
}

// GrantDailyCredits returns a disabled response.
func (NoopClient) GrantDailyCredits(ctx context.Context, walletAddress string) (CreditsGrant, error) {
	return CreditsGrant{}, ErrPrivateServicesDisabled
}

// GetCreditsBalance returns a disabled response.
func (NoopClient) GetCreditsBalance(ctx context.Context, walletAddress string) (CreditsBalance, error) {
	return CreditsBalance{}, ErrPrivateServicesDisabled
}

// RequestCashout returns a disabled response.
func (NoopClient) RequestCashout(ctx context.Context, walletAddress string, amount int64) (CashoutReceipt, error) {
	return CashoutReceipt{}, ErrPrivateServicesDisabled
}

// NewEnvClient returns a client that exposes env-driven capabilities with disabled operations.
func NewEnvClient() Client {
	return EnvClient{}
}

// EnvClient exposes capability flags from environment variables.
type EnvClient struct{}

// Capabilities returns env-driven capability flags.
func (EnvClient) Capabilities() Capabilities {
	return EnvProvider{}.Capabilities()
}

// CheckEligibility returns a disabled response.
func (EnvClient) CheckEligibility(ctx context.Context, walletAddress string) (EligibilityResult, error) {
	return EligibilityResult{Status: EligibilityUnknown}, ErrPrivateServicesDisabled
}

// GrantDailyCredits returns a disabled response.
func (EnvClient) GrantDailyCredits(ctx context.Context, walletAddress string) (CreditsGrant, error) {
	return CreditsGrant{}, ErrPrivateServicesDisabled
}

// GetCreditsBalance returns a disabled response.
func (EnvClient) GetCreditsBalance(ctx context.Context, walletAddress string) (CreditsBalance, error) {
	return CreditsBalance{}, ErrPrivateServicesDisabled
}

// RequestCashout returns a disabled response.
func (EnvClient) RequestCashout(ctx context.Context, walletAddress string, amount int64) (CashoutReceipt, error) {
	return CashoutReceipt{}, ErrPrivateServicesDisabled
}

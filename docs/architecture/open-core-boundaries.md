# Open-Core Boundaries for Ori Agent

## Purpose
Define the split between the open-source core and the private web3/marketplace services so the core can be used commercially while Ori Token services remain proprietary.

## Current Web3 + Marketplace Touchpoints (Audit)

### Backend
- `internal/config/config.go`: web3 wallet fields, validation, and persistence.
- `internal/settingshttp/web3_handler.go`: `/api/web3-wallet` and `/api/web3-chains` endpoints.
- `internal/server/routes.go`: web3 wallet routes, marketplace routes, and `/marketplace` page routing.
- `internal/server/initialization.go`: marketplace store + registry initialization.
- `internal/server/builder_core.go`: wiring for marketplace handler.
- `internal/marketplace`: marketplace config store and persistence.
- `internal/marketplacehttp/handlers.go`: marketplace CRUD endpoints.
- `internal/registry/*`: marketplace registry fetching/merging logic.
- `internal/mcphttp/handlers.go`: `/api/mcp/marketplace` endpoints for MCP server marketplace.

### Frontend
- `internal/web/templates/pages/settings.tmpl`: web3 wallet section in settings.
- `internal/web/templates/components/modals.tmpl`: onboarding step for web3 wallet.
- `internal/web/static/js/modules/web3-settings.js`: web3 wallet connection for settings page.
- `internal/web/static/js/modules/onboarding.js`: web3 onboarding step logic.
- `internal/web/static/js/modules/marketplace-settings.js`: marketplace configuration UI.
- `internal/web/static/js/mcp.js`: MCP marketplace search and install UI.

### Config + Docs
- `marketplace_config.json`: local marketplace config file.
- `docs/premium_features.md`: tokenomics and premium feature notes.

## Open-Source Core Scope (Commercial Use Allowed)
- Agent runtime, orchestration, plugin SDK, and local plugin execution.
- Plugin registry browsing and non-monetized marketplace management.
- Web3 wallet connection UI and local storage of wallet metadata, without on-chain operations.
- All non-web3 UI, API routes, and settings needed to run the core agent.

## Private Services Scope (Not in Public Repo)
- Ori Token issuance, smart contracts, and token operations.
- Marketplace payment processing and Ori-specific monetization.
- Daily credits ledger, eligibility enforcement, and cashout to Ori Token.
- NFT ownership verification and anti-cheat logic.
- Payout accounting, fraud monitoring, and operational tooling.

## Private Repo Location
- Private services live in a separate repository: `ori-platform-services`.
- This repo is intentionally kept out of the open-source tree and release artifacts.

## Integration Boundaries
- Open-source core should define interfaces/adapters for optional private services.
- Default OSS behavior should be no-op or mock implementations.
- Any Ori marketplace/web3 functionality should only activate when private services are configured.

## Capability Flags (OSS)
- `ORI_WEB3_WALLET_ENABLED` (default: true) controls web3 wallet UI and routes.
- `ORI_MARKETPLACE_PAYMENTS_ENABLED` (default: false) is reserved for private payment flows.
- `ORI_TOKEN_PAYOUTS_ENABLED` (default: false) is reserved for credits/cashout flows.

## Licensing and Terms Alignment
- License must allow commercial use of the core agent.
- License must prohibit use of Ori Token services, Ori marketplace APIs, or Ori branding without agreement.
- Public docs should not provide guidance for third-party web3 implementations.

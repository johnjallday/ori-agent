package connectionshttp

import (
	"net/url"

	"github.com/johnjallday/ori-agent/internal/connections"
)

// Result-page copy for the OAuth callback. The old page said "We couldn't
// complete the Google sign-in" for every failure — including the common case
// where the sign-in worked perfectly and only the local vault write failed.
// Each category now gets its own headline, explanation, and repair action
// (FR 13-16), and none of them can contain a code, token, or vault secret.

// settingsAnchor is where every result page returns to: the Google Account card.
const settingsAnchor = "/settings#google-account"

// callbackCopy is one category's user-facing presentation.
type callbackCopy struct {
	// Title is the headline. It states plainly whether Google sign-in succeeded.
	Title string
	// Body explains what happened and what will fix it.
	Body string
	// ActionLabel is the link text back into Ori.
	ActionLabel string
	// Action, when set, is a repair hint the Google Account card acts on as soon
	// as the user lands back on Settings (e.g. re-open the unlock prompt).
	Action string
}

// signedInPrefix marks failures where the user DID authenticate with Google, so
// the page never implies they need to redo the Google half.
const signedInPrefix = "You signed in with Google successfully. "

var callbackCopyByCategory = map[connections.CallbackCategory]callbackCopy{
	connections.CategoryDenied: {
		Title:       "Sign-in canceled",
		Body:        "Nothing changed. Gmail is still disabled — you can start again whenever you're ready.",
		ActionLabel: "Return to Google Account",
	},
	connections.CategoryExpiredState: {
		Title:       "This sign-in link expired",
		Body:        "The link expired or was already used, so Ori stopped rather than trusting it. Start the connection again from Settings.",
		ActionLabel: "Return to Google Account",
	},
	connections.CategoryAccountMismatch: {
		Title:       "That's a different Google account",
		Body:        signedInPrefix + "It isn't the account currently connected to Ori, so nothing was changed. Disconnect the current account first, or sign in again with the connected one.",
		ActionLabel: "Return to Google Account",
	},
	connections.CategoryTokenExchangeFailed: {
		Title:       "Google didn't complete the sign-in",
		Body:        "Google returned an error while finishing the sign-in. Nothing was stored. Please try again.",
		ActionLabel: "Return to Google Account",
	},
	connections.CategoryIdentityUnverified: {
		Title:       "We couldn't verify the Google sign-in",
		Body:        "The identity Google returned didn't pass verification, so Ori stopped and stored nothing. Please try again.",
		ActionLabel: "Return to Google Account",
	},
	connections.CategoryVaultLocked: {
		Title:       "Signed in — your vault is locked",
		Body:        signedInPrefix + "Ori couldn't store the credential because the vault it belongs in is locked. Unlock the vault and enable Gmail again; you won't need to sign in with Google a second time.",
		ActionLabel: "Unlock the vault and try again",
		Action:      "unlock",
	},
	connections.CategoryVaultSelectionRequired: {
		Title:       "Signed in — choose a vault",
		Body:        signedInPrefix + "Ori doesn't have a usable vault to store the credential in. Choose or create one, then enable Gmail again.",
		ActionLabel: "Choose a vault and try again",
		Action:      "choose",
	},
	connections.CategoryVaultUnavailable: {
		Title:       "Signed in — vault unavailable",
		Body:        signedInPrefix + "Ori couldn't reach its credential vault, so nothing was stored. Check the vault in Settings, then try again.",
		ActionLabel: "Return to Google Account",
		Action:      "repair",
	},
	connections.CategoryCredentialPersistFailed: {
		Title:       "Signed in — saving failed on this machine",
		Body:        signedInPrefix + "Ori couldn't write the credential locally, so Gmail is still disabled. Try enabling Gmail again.",
		ActionLabel: "Return to Google Account",
	},
	connections.CategoryConnectionPersistFailed: {
		Title:       "Signed in — saving failed on this machine",
		Body:        signedInPrefix + "Ori couldn't record the result locally. Reload Settings to see the current state and try again.",
		ActionLabel: "Return to Google Account",
	},
	connections.CategoryNotConfigured: {
		Title:       "Google sign-in isn't configured",
		Body:        "This build has no Google OAuth client configured, so the sign-in can't be completed.",
		ActionLabel: "Return to Google Account",
	},
	connections.CategoryNoIdentity: {
		Title:       "No Google account is connected",
		Body:        "Connect your Google account first, then enable Gmail.",
		ActionLabel: "Return to Google Account",
	},
}

var unknownCallbackCopy = callbackCopy{
	Title:       "We couldn't finish connecting",
	Body:        "Something went wrong on this machine after the sign-in. Nothing sensitive was stored. Please try again from Settings.",
	ActionLabel: "Return to Google Account",
}

// copyFor returns the presentation for a classified failure.
func copyFor(failure *connections.CallbackError) callbackCopy {
	if failure == nil {
		return unknownCallbackCopy
	}
	c, ok := callbackCopyByCategory[failure.Category]
	if !ok {
		return unknownCallbackCopy
	}
	return c
}

// returnURL builds the link back into Settings, carrying the repair hint (and
// the vault it applies to) so the Google Account card can offer the exact next
// step instead of making the user rediscover it. Only ids and short action
// keywords travel in the URL.
func returnURL(failure *connections.CallbackError, action string) string {
	if action == "" {
		return settingsAnchor
	}
	q := url.Values{}
	q.Set("gc_action", action)
	if failure != nil && failure.VaultID != "" {
		q.Set("gc_vault", failure.VaultID)
	}
	return "/settings?" + q.Encode() + "#google-account"
}

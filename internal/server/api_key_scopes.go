package server

// APIKeyScope is one entry in the workspace API key scope catalog.
// The catalog lives in code (not the DB) because adding/changing scopes
// requires handler-side enforcement changes anyway — keeping them in
// lockstep with code avoids migration drift.
//
// Naming convention: "<resource>:<verb>". Match this 1:1 with the swag
// operationId of the handler that enforces it.
type APIKeyScope struct {
	Name        string
	Description string
	Available   bool // false = listed in catalog but not yet enforced anywhere; mint rejects it
}

// apiKeyScopeCatalog is the v1 scope catalog. Adding a scope is a code
// change here + an enforcement check on the handler that requires it.
//
// Available=false slots are placeholders so the SPA can show them as
// "coming soon" without surprising users with a sudden new scope.
var apiKeyScopeCatalog = []APIKeyScope{
	{Name: "turns:submit", Description: "Submit codex turns via POST /api/turns (LLM cost incurred)", Available: true},
	{Name: "turns:read", Description: "List past turn history", Available: false},
	{Name: "threads:create", Description: "Start a fresh thread", Available: false},
	{Name: "threads:cancel", Description: "Cancel an in-flight turn", Available: false},
	{Name: "threads:read", Description: "Read thread history", Available: false},
	{Name: "mailbox:read", Description: "Read inbound mailbox messages", Available: false},
	{Name: "mailbox:send", Description: "Send to a mailbox", Available: false},
}

// ScopeTurnsSubmit is the constant used by /api/turns to assert presence.
// Each enforcement site declares its own constant to make grep easy.
const ScopeTurnsSubmit = "turns:submit"

// validateScopes returns an error if any scope is unknown or not Available.
// Empty input is also an error (mint must request at least one scope).
func validateScopes(requested []string) error {
	if len(requested) == 0 {
		return errInvalidScope("at least one scope required")
	}
	allowed := map[string]bool{}
	for _, s := range apiKeyScopeCatalog {
		if s.Available {
			allowed[s.Name] = true
		}
	}
	for _, r := range requested {
		if !allowed[r] {
			return errInvalidScope("scope not available: " + r)
		}
	}
	return nil
}

type errInvalidScope string

func (e errInvalidScope) Error() string { return string(e) }

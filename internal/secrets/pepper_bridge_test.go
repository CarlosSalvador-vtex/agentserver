// pepper_bridge_test.go — bridges the internal ResetPepperForTest helper
// into the external test package (package secrets_test). This file must be
// in package secrets_test so it can call the exported test-only var defined
// in pepper_reset_test.go (package secrets).
package secrets_test

import "github.com/agentserver/agentserver/internal/secrets"

// resetPepperForTest is the function referenced by secrets_test.go's
// t.Cleanup calls. It delegates to the internal test helper.
func resetPepperForTest() {
	secrets.ResetPepperForTest()
}

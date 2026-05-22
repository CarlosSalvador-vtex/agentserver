// pepper_reset_test.go — test-only helpers for pepper state management.
// This file is in package secrets (internal test package) so it can access
// the unexported pepper variable. It re-exports resetPepper as the package-
// level var ResetPepperForTest so that the external _test package can call
// it via a thin wrapper.
package secrets

// ResetPepperForTest zeros the package-level pepper so tests that call
// SetPepper don't bleed into subsequent tests. ONLY for use in tests.
var ResetPepperForTest = func() {
	pepperMu.Lock()
	defer pepperMu.Unlock()
	pepper = nil
}

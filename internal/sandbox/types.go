package sandbox

// Sandbox type identifiers for supported agent runtimes.
const (
	TypeOpenclaw = "openclaw"
	TypeHermes   = "hermes"
)

// Valid reports whether t is a supported sandbox type.
func Valid(t string) bool {
	switch t {
	case TypeOpenclaw, TypeHermes:
		return true
	default:
		return false
	}
}

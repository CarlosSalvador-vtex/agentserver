package imbridgesvc

const telegramBindingSandboxTypeMsg = "telegram binding requires an openclaw or nanoclaw sandbox"

// telegramBindAllowedType reports whether sandboxType may use legacy sandbox-level Telegram binding.
func telegramBindAllowedType(sandboxType string) bool {
	switch sandboxType {
	case "openclaw", "nanoclaw":
		return true
	default:
		return false
	}
}

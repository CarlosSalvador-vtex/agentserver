package sandbox

// SandboxType identifies the agent runtime image for a sandbox pod.
type SandboxType string

const (
	SandboxTypeOpencode   SandboxType = "opencode"
	SandboxTypeOpenclaw   SandboxType = "openclaw"
	SandboxTypeNanoclaw   SandboxType = "nanoclaw"
	SandboxTypeClaudeCode SandboxType = "claudecode"
	SandboxTypeJupyter    SandboxType = "jupyter"
	SandboxTypeHermes     SandboxType = "hermes"
)

// String returns the wire/API value for the sandbox type.
func (s SandboxType) String() string { return string(s) }

// Valid reports whether s is a known sandbox runtime type.
func (s SandboxType) Valid() bool {
	switch s {
	case SandboxTypeOpencode, SandboxTypeOpenclaw, SandboxTypeNanoclaw,
		SandboxTypeClaudeCode, SandboxTypeJupyter, SandboxTypeHermes:
		return true
	}
	return false
}

// RefKind is the prefix of a composition reference (git: or draft:).
type RefKind string

const (
	RefKindGit   RefKind = "git"
	RefKindDraft RefKind = "draft"
)

// String returns the ref kind wire value.
func (k RefKind) String() string { return string(k) }

// ProviderKind identifies an IM bridge provider.
type ProviderKind string

const (
	ProviderWeixin   ProviderKind = "weixin"
	ProviderTelegram ProviderKind = "telegram"
	ProviderMatrix   ProviderKind = "matrix"
	ProviderWhatsApp ProviderKind = "whatsapp"
)

// String returns the provider wire value.
func (p ProviderKind) String() string { return string(p) }

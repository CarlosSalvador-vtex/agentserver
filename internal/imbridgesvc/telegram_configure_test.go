package imbridgesvc

import "testing"

func TestTelegramBindAllowedType(t *testing.T) {
	tests := []struct {
		name        string
		sandboxType string
		want        bool
	}{
		{name: "openclaw", sandboxType: "openclaw", want: true},
		{name: "nanoclaw", sandboxType: "nanoclaw", want: true},
		{name: "hermes", sandboxType: "hermes", want: false},
		{name: "empty", sandboxType: "", want: false},
		{name: "codex", sandboxType: "codex", want: false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := telegramBindAllowedType(tt.sandboxType); got != tt.want {
				t.Fatalf("telegramBindAllowedType(%q) = %v, want %v", tt.sandboxType, got, tt.want)
			}
		})
	}
}

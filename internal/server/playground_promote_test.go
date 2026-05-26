package server

import (
	"strings"
	"testing"
)

func TestScanPII(t *testing.T) {
	tests := []struct {
		name    string
		content string
		wantHit bool
	}{
		{"clean text", "Greeting message no numbers here.", false},
		{"raw cpf", `{"cpf": "111.222.333-44"}`, true},
		{"raw email", `Contact john@example.com`, true},
		{"raw phone", `Call +55 27 99607-3736 today`, true},
		{"cpf with TEST allow", `Maria (TEST), cpf 111.222.333-44, fixture data only`, false},
		{"cpf last_3 only", `{"cpf_last_3": "111"}`, false},
		{"email with TEST does not allow", `john@example.com (TEST account)`, true},
		{"empty", "", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := scanPII(tt.content)
			hit := got != ""
			if hit != tt.wantHit {
				t.Errorf("scanPII(%q) = %q, wantHit=%v", tt.content, got, tt.wantHit)
			}
		})
	}
}

func TestFrontmatterToYAML(t *testing.T) {
	fm := map[string]interface{}{
		"id":      "julia-cobranca",
		"version": "1.2.0",
		"voice": map[string]interface{}{
			"language":  "pt-BR",
			"formality": "high",
		},
		"constraints": map[string]interface{}{
			"max_turns":       float64(20),
			"refuse_patterns": []interface{}{"legal-threat", "pii-disclosure"},
		},
	}
	got, err := frontmatterToYAML(fm)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	for _, want := range []string{
		"id: julia-cobranca",
		"version: 1.2.0",
		"voice:",
		"language: pt-BR",
		"formality: high",
		"constraints:",
		"max_turns: 20",
		"refuse_patterns:",
		"- legal-threat",
		"- pii-disclosure",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("missing %q in:\n%s", want, got)
		}
	}
	// Stable key order — id before version before voice.
	idIdx := strings.Index(got, "id:")
	verIdx := strings.Index(got, "version:")
	voiceIdx := strings.Index(got, "voice:")
	if !(idIdx < verIdx && verIdx < voiceIdx) {
		t.Errorf("expected stable order id < version < voice; got\n%s", got)
	}
}

func TestYamlString(t *testing.T) {
	cases := map[string]string{
		"plain":        "plain",
		"with-dash":    "with-dash",
		"":             `""`,
		"has: colon":   `"has: colon"`,
		"has\"quote":   `"has\"quote"`,
		"trailing ":    `"trailing "`,
		" leading":     `" leading"`,
		"has{}braces":  `"has{}braces"`,
	}
	for in, want := range cases {
		if got := yamlString(in); got != want {
			t.Errorf("yamlString(%q) = %q, want %q", in, got, want)
		}
	}
}

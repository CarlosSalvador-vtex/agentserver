package sandbox

import (
	"strings"
	"testing"
)

func TestParseCompositionRef(t *testing.T) {
	tests := []struct {
		in      string
		wantNil bool
		wantErr bool
		kind    string
		name    string
		sha     string
		uuid    string
	}{
		{in: "", wantNil: true},
		{in: "   ", wantNil: true},
		{in: "git:cobranca@a3f2c", kind: "git", name: "cobranca", sha: "a3f2c"},
		{in: "git:my-skill@v1.2.0", kind: "git", name: "my-skill", sha: "v1.2.0"},
		{in: "git:cobranca@main", kind: "git", name: "cobranca", sha: "main"},
		{in: "draft:abc-123", kind: "draft", uuid: "abc-123"},
		{in: "git:cobranca", wantErr: true},
		{in: "git:@sha", wantErr: true},
		{in: "git:name@", wantErr: true},
		{in: "draft:", wantErr: true},
		{in: "unknown:foo", wantErr: true},
	}
	for _, tt := range tests {
		t.Run(tt.in, func(t *testing.T) {
			got, err := ParseCompositionRef(tt.in)
			if tt.wantErr {
				if err == nil {
					t.Fatalf("want error, got nil")
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if tt.wantNil {
				if got != nil {
					t.Fatalf("want nil, got %+v", got)
				}
				return
			}
			if got.Kind != tt.kind || got.Name != tt.name || got.Sha != tt.sha || got.UUID != tt.uuid {
				t.Fatalf("mismatch: got %+v, want kind=%q name=%q sha=%q uuid=%q",
					got, tt.kind, tt.name, tt.sha, tt.uuid)
			}
		})
	}
}

func TestPrependOpenclawSoulHint(t *testing.T) {
	// prependOpenclawSoulHint is intentionally a no-op since S4-PR1:
	// openclaw auto-loads ~/.openclaw/workspace/SOUL.md natively as a
	// bootstrap file, so injecting a hint into prompt.md is unnecessary.
	// This test guards against accidental re-introduction of mutation.
	in := map[string]string{"prompt.md": "# Skill\nDo things."}
	out := prependOpenclawSoulHint(in)
	if out["prompt.md"] != in["prompt.md"] {
		t.Fatalf("prependOpenclawSoulHint must be a passthrough; got %q, want %q",
			out["prompt.md"], in["prompt.md"])
	}
}

func TestExtractSoulConstraints(t *testing.T) {
	fm := map[string]interface{}{
		"id": "julia",
		"constraints": map[string]interface{}{
			"max_turns":       float64(20),
			"refuse_patterns": []interface{}{"legal-threat"},
			"ignored_field":   "should not appear",
		},
		"voice": map[string]interface{}{"language": "pt-BR"},
	}
	out := extractSoulConstraints(fm)
	if _, ok := out["max_turns"]; !ok {
		t.Errorf("max_turns missing")
	}
	if _, ok := out["refuse_patterns"]; !ok {
		t.Errorf("refuse_patterns missing")
	}
	if _, ok := out["ignored_field"]; ok {
		t.Errorf("unknown field should not be copied")
	}

	if got := extractSoulConstraints(nil); got != nil {
		t.Errorf("nil frontmatter should return nil, got %+v", got)
	}
}

func TestSanitizeLabelValue(t *testing.T) {
	cases := map[string]string{
		"Negociação de Dívida-fork": "negocia--o-de-d-vida-fork",
		"cobranca":                  "cobranca",
		"My Skill v1.2":             "my-skill-v1.2",
		"  ção  ":                   "o", // accents → '-', trimmed to alphanumeric
		"":                          "",
		// Edge case: pure-CJK names have no ASCII alphanumerics, so every rune
		// becomes '-' and the trailing Trim collapses the whole thing to "".
		// K8s accepts an empty label value, so the pod still starts — which is
		// exactly why this is dangerous: two distinct CJK-named templates both
		// sanitize to "" and collide silently on the same ConfigMap label.
		// Fixtures P4 (サポート担当) and A6 (天気エージェント) exercise this in a sandbox;
		// these assert the same path at the unit level so a regression is loud.
		"天気エージェント":   "",        // pure CJK → empty
		"サポート担当":     "",        // different CJK, ALSO empty
		"天気-weather": "weather", // mixed: CJK stripped, ASCII tail survives
	}
	for in, want := range cases {
		if got := sanitizeLabelValue(in); got != want {
			t.Errorf("sanitizeLabelValue(%q) = %q, want %q", in, got, want)
		}
	}
	// Make the collision explicit: two different CJK names currently both map
	// to "" — a non-distinguishing label. This documents the #126 gap. If
	// sanitizeLabelValue ever gains a fallback (e.g. a hash suffix) to avoid
	// empty/colliding labels, flip this to require the two to differ.
	if a, b := sanitizeLabelValue("天気エージェント"), sanitizeLabelValue("サポート担当"); a != "" || b != "" {
		t.Errorf("expected both CJK names to sanitize to empty (documenting the collision); got %q and %q", a, b)
	}
	// Must never exceed 63 chars and must be a valid label (start/end alnum).
	long := sanitizeLabelValue(strings.Repeat("a ", 100))
	if len(long) > 63 {
		t.Errorf("sanitizeLabelValue too long: %d", len(long))
	}
}

package db

import "testing"

func TestValidateSlug(t *testing.T) {
	cases := []struct {
		name    string
		in      string
		wantErr bool
	}{
		{"valid kebab", "empresa-a", false},
		{"valid lowercase", "acme", false},
		{"valid with digits", "foo-1", false},
		{"single char", "a", true},
		{"too long", string(make([]byte, 64)), true},
		{"uppercase", "Empresa-A", true},
		{"underscore", "empresa_a", true},
		{"leading hyphen", "-empresa", true},
		{"trailing hyphen", "empresa-", true},
		{"double hyphen", "empresa--a", true},
		{"reserved www", "www", true},
		{"reserved api", "api", true},
		{"reserved admin", "admin", true},
		{"reserved app", "app", true},
		{"reserved claw-prefix", "claw-foo", true},
		{"reserved hermes-prefix", "hermes-bar", true},
		{"empty", "", true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := ValidateSlug(tc.in)
			if (err != nil) != tc.wantErr {
				t.Fatalf("ValidateSlug(%q) err=%v, wantErr=%v", tc.in, err, tc.wantErr)
			}
		})
	}
}

func TestValidateSlugReservedExpanded(t *testing.T) {
	// One representative slug per B10 category (operational, support, status, docs, infra, commercial, marketing, compliance, resources, special hosts).
	samples := []string{
		"mail", "support", "status", "docs", "cdn",
		"billing", "signup", "legal", "media", "localhost",
	}
	for _, slug := range samples {
		if err := ValidateSlug(slug); err == nil {
			t.Fatalf("ValidateSlug(%q) expected reserved error, got nil", slug)
		}
	}
}

func TestSlugify(t *testing.T) {
	cases := map[string]string{
		"Empresa A":       "empresa-a",
		"  Acme   Corp  ": "acme-corp",
		"Foo_Bar.Baz":     "foo-bar-baz",
		"Mañana":          "ma-ana",
		"":                "workspace",
		"---":             "workspace",
	}
	for in, want := range cases {
		if got := Slugify(in); got != want {
			t.Fatalf("Slugify(%q)=%q, want %q", in, got, want)
		}
	}
}

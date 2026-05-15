package server

import (
	"strings"
	"testing"
)

func TestGenerateCodexToken_ShapeAndUniqueness(t *testing.T) {
	t1, id1, secret1, err := generateCodexToken()
	if err != nil {
		t.Fatalf("gen: %v", err)
	}
	if !strings.HasPrefix(t1, "ast_") {
		t.Errorf("missing prefix: %s", t1)
	}
	if id1 == "" || len(id1) != 8 {
		t.Errorf("id len: %q", id1)
	}
	if len(secret1) < 40 {
		t.Errorf("secret too short: %d", len(secret1))
	}
	if t1 != "ast_"+id1+"_"+secret1 {
		t.Errorf("token recombination mismatch: %s", t1)
	}
	t2, _, _, _ := generateCodexToken()
	if t1 == t2 {
		t.Error("two generated tokens collide")
	}
}

func TestParseCodexToken(t *testing.T) {
	cases := []struct {
		in        string
		wantID    string
		wantSec   string
		wantErr   bool
	}{
		{"ast_a3k9f7zq_n2p4xj8m", "a3k9f7zq", "n2p4xj8m", false},
		{"", "", "", true},
		{"ast_only_two_segments", "", "", true},
		{"foo_a3k9f7zq_secret", "", "", true},
		{"ast__secret", "", "", true},
		{"ast_id_", "", "", true},
	}
	for _, c := range cases {
		id, sec, err := parseCodexToken(c.in)
		if c.wantErr {
			if err == nil {
				t.Errorf("%q: want err, got id=%q sec=%q", c.in, id, sec)
			}
			continue
		}
		if err != nil {
			t.Errorf("%q: unexpected err %v", c.in, err)
		}
		if id != c.wantID || sec != c.wantSec {
			t.Errorf("%q: got id=%q sec=%q", c.in, id, sec)
		}
	}
}

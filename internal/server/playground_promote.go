package server

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"regexp"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/agentserver/agentserver/internal/auth"
	"github.com/agentserver/agentserver/internal/db"
)

// Promote — turns a DB draft into a git PR on the agentserver repo.
// Uses the GitHub Contents API so the agentserver pod doesn't need a
// git binary or a working tree. Config via env:
//
//	GITHUB_PROMOTE_TOKEN  PAT with "repo" scope (write access)
//	GITHUB_PROMOTE_REPO   "<owner>/<repo>" — defaults to upstream
//	GITHUB_PROMOTE_BASE   base branch to PR against (default "main")
//
// When GITHUB_PROMOTE_TOKEN is empty, the promote endpoint returns 503
// — useful for dev clusters where authoring matters but git pushes
// shouldn't fire.
type promoteConfig struct {
	Token string
	Repo  string
	Base  string
}

func loadPromoteConfig() (*promoteConfig, error) {
	token := strings.TrimSpace(os.Getenv("GITHUB_PROMOTE_TOKEN"))
	if token == "" {
		return nil, fmt.Errorf("promote disabled: GITHUB_PROMOTE_TOKEN not set")
	}
	repo := strings.TrimSpace(os.Getenv("GITHUB_PROMOTE_REPO"))
	if repo == "" {
		repo = "CarlosSalvador-vtex/agentserver"
	}
	if !strings.Contains(repo, "/") {
		return nil, fmt.Errorf("GITHUB_PROMOTE_REPO must be owner/repo, got %q", repo)
	}
	base := strings.TrimSpace(os.Getenv("GITHUB_PROMOTE_BASE"))
	if base == "" {
		base = "main"
	}
	return &promoteConfig{Token: token, Repo: repo, Base: base}, nil
}

// --- promote skill -----------------------------------------------------

func (s *Server) handlePromoteSkillDraft(w http.ResponseWriter, r *http.Request) {
	userID := auth.UserIDFromContext(r.Context())
	id := chi.URLParam(r, "id")

	if !s.userIsMaintainerOrOwner(userID) {
		http.Error(w, "promote requires maintainer or owner role", http.StatusForbidden)
		return
	}

	cfg, err := loadPromoteConfig()
	if err != nil {
		http.Error(w, err.Error(), http.StatusServiceUnavailable)
		return
	}

	draft, err := s.DB.GetSkillDraft(id)
	if err != nil || draft == nil {
		http.Error(w, "not found", http.StatusNotFound)
		return
	}
	if !draft.AuthorUserID.Valid || draft.AuthorUserID.String != userID {
		// Allow maintainers to promote others' drafts if they were
		// previously shared. For MVP keep author-only.
		http.Error(w, "not your draft", http.StatusForbidden)
		return
	}
	if err := validateSkillForPromote(draft); err != nil {
		RecordPromoteResult("skill", "failed_validation")
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	// Atomic status flip: only one promote can be in flight per draft.
	ok, err := s.DB.TryPromoteSkillDraft(id)
	if err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	if !ok {
		http.Error(w, "promote already in flight or draft not editable", http.StatusConflict)
		return
	}

	// Convert files to {repoPath: content}.
	files := make(map[string]string, len(draft.Files))
	for path, content := range draft.Files {
		files["deploy/helm/agentserver/skills/"+draft.Name+"/"+path] = content
	}

	branch := fmt.Sprintf("playground/skill-%s-%s", draft.Name, draft.ID[:8])
	title := fmt.Sprintf("feat(skill): promote %s from playground", draft.Name)
	body := fmt.Sprintf("Promoted from playground draft `%s` by user `%s`.\n\n"+
		"## Draft metadata\n- ID: %s\n- Files: %d\n- Description: %s\n",
		draft.ID, userID, draft.ID, len(draft.Files), draft.Description)

	result, err := promoteToGitHub(cfg, branch, title, body, files)
	if err != nil {
		RecordPromoteResult("skill", "failed_github")
		_ = s.DB.RevertPromoteSkillDraft(id)
		http.Error(w, fmt.Sprintf("github push failed: %v", err), http.StatusBadGateway)
		return
	}

	if err := s.DB.CompletePromoteSkillDraft(id, result.PRURL, result.HeadSha); err != nil {
		// Status is now lost: PR opened, DB not updated. Logging would
		// be enough — the next list call surfaces the divergence.
		http.Error(w, fmt.Sprintf("PR opened (%s) but DB update failed: %v", result.PRURL, err), http.StatusInternalServerError)
		return
	}

	RecordPromoteResult("skill", "ok")
	RecordDraftAction("skill", "promoted")
	writeJSON(w, http.StatusOK, map[string]string{
		"pr_url":    result.PRURL,
		"branch":    branch,
		"head_sha":  result.HeadSha,
		"draft_id":  id,
		"draft_kind": "skill",
	})
}

// --- promote soul ------------------------------------------------------

func (s *Server) handlePromoteSoulDraft(w http.ResponseWriter, r *http.Request) {
	userID := auth.UserIDFromContext(r.Context())
	id := chi.URLParam(r, "id")

	if !s.userIsMaintainerOrOwner(userID) {
		http.Error(w, "promote requires maintainer or owner role", http.StatusForbidden)
		return
	}

	cfg, err := loadPromoteConfig()
	if err != nil {
		http.Error(w, err.Error(), http.StatusServiceUnavailable)
		return
	}

	draft, err := s.DB.GetSoulDraft(id)
	if err != nil || draft == nil {
		http.Error(w, "not found", http.StatusNotFound)
		return
	}
	if !draft.AuthorUserID.Valid || draft.AuthorUserID.String != userID {
		http.Error(w, "not your draft", http.StatusForbidden)
		return
	}
	if err := validateSoulForPromote(draft); err != nil {
		RecordPromoteResult("soul", "failed_validation")
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	ok, err := s.DB.TryPromoteSoulDraft(id)
	if err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	if !ok {
		http.Error(w, "promote already in flight or draft not editable", http.StatusConflict)
		return
	}

	// Soul ships as a single soul.md file: frontmatter as YAML, then
	// "---" separator, then body. (GitHub Markdown renders this layout
	// natively when previewing the file.)
	fmYAML, err := frontmatterToYAML(draft.Frontmatter)
	if err != nil {
		_ = s.DB.RevertPromoteSoulDraft(id)
		http.Error(w, fmt.Sprintf("encode frontmatter: %v", err), http.StatusBadRequest)
		return
	}
	soulMD := "---\n" + fmYAML + "---\n\n" + draft.Body
	files := map[string]string{
		"deploy/helm/agentserver/souls/" + draft.Name + "/soul.md": soulMD,
	}

	branch := fmt.Sprintf("playground/soul-%s-%s", draft.Name, draft.ID[:8])
	title := fmt.Sprintf("feat(soul): promote %s from playground", draft.Name)
	body := fmt.Sprintf("Promoted from playground draft `%s` by user `%s`.\n\n"+
		"## Draft metadata\n- ID: %s\n- Schema: %s\n- Description: %s\n",
		draft.ID, userID, draft.ID, draft.SchemaVersion, draft.Description)

	result, err := promoteToGitHub(cfg, branch, title, body, files)
	if err != nil {
		RecordPromoteResult("soul", "failed_github")
		_ = s.DB.RevertPromoteSoulDraft(id)
		http.Error(w, fmt.Sprintf("github push failed: %v", err), http.StatusBadGateway)
		return
	}

	if err := s.DB.CompletePromoteSoulDraft(id, result.PRURL, result.HeadSha); err != nil {
		http.Error(w, fmt.Sprintf("PR opened (%s) but DB update failed: %v", result.PRURL, err), http.StatusInternalServerError)
		return
	}

	RecordPromoteResult("soul", "ok")
	RecordDraftAction("soul", "promoted")
	writeJSON(w, http.StatusOK, map[string]string{
		"pr_url":     result.PRURL,
		"branch":     branch,
		"head_sha":   result.HeadSha,
		"draft_id":   id,
		"draft_kind": "soul",
	})
}

// --- role gate ---------------------------------------------------------

// userIsMaintainerOrOwner returns true if the user holds maintainer or
// owner role in *any* workspace. Playground promote is system-wide
// (templates are global per design §3 row 4), so we use "is at least
// maintainer somewhere" as the gate. Workspaces with only developer
// roles see promote blocked.
func (s *Server) userIsMaintainerOrOwner(userID string) bool {
	// Cheap check via a single query.
	var n int
	err := s.DB.QueryRow(
		`SELECT COUNT(*) FROM workspace_members
		WHERE user_id = $1 AND role IN ('owner', 'maintainer')`,
		userID,
	).Scan(&n)
	if err != nil {
		return false
	}
	return n > 0
}

// --- PII heuristics + schema validators --------------------------------

// CPF pattern: 11 digits, optionally formatted. We accept hits that are
// clearly test placeholders ("...111", "(TEST)" tag nearby) to avoid
// flagging the cobranca fixtures.
var (
	cpfRE   = regexp.MustCompile(`\b\d{3}\.?\d{3}\.?\d{3}-?\d{2}\b`)
	emailRE = regexp.MustCompile(`[A-Za-z0-9._%+\-]+@[A-Za-z0-9.\-]+\.[A-Za-z]{2,}`)
	phoneRE = regexp.MustCompile(`\+?\d{2,4}[\s\-]?\(?\d{2,4}\)?[\s\-]?\d{3,5}[\s\-]?\d{3,5}`)
)

// validateSkillForPromote runs structural + PII checks before any DB
// or GitHub side-effect.
func validateSkillForPromote(d *db.SkillDraft) error {
	if len(d.Files) == 0 {
		return fmt.Errorf("draft is empty — add at least one file before promoting")
	}
	// Manifest must be present + valid shape.
	manifest, ok := d.Files["openclaw.plugin.json"]
	if !ok {
		return fmt.Errorf("openclaw.plugin.json is required (see docs/openclaw-skill-slash-research.md)")
	}
	var parsed map[string]interface{}
	if err := json.Unmarshal([]byte(manifest), &parsed); err != nil {
		return fmt.Errorf("openclaw.plugin.json: invalid JSON: %v", err)
	}
	if _, ok := parsed["id"].(string); !ok {
		return fmt.Errorf("openclaw.plugin.json: top-level \"id\" string is required")
	}
	if _, ok := parsed["configSchema"]; !ok {
		return fmt.Errorf("openclaw.plugin.json: top-level \"configSchema\" object is required (see telegram.openclaw.plugin.json for shape)")
	}
	// PII scan, scoped to references/*.json (the typical fixture path).
	for path, content := range d.Files {
		if !strings.HasPrefix(path, "references/") {
			continue
		}
		if !strings.HasSuffix(path, ".json") {
			continue
		}
		if hit := scanPII(content); hit != "" {
			return fmt.Errorf("PII heuristic in %s: %s (use synthetic data + \"(TEST)\" suffix)", path, hit)
		}
	}
	return nil
}

func validateSoulForPromote(d *db.SoulDraft) error {
	if len(d.Frontmatter) == 0 {
		return fmt.Errorf("soul frontmatter is empty")
	}
	if d.Body == "" {
		return fmt.Errorf("soul body is empty")
	}
	if hit := scanPII(d.Body); hit != "" {
		return fmt.Errorf("PII heuristic in soul body: %s", hit)
	}
	return nil
}

// scanPII returns a non-empty hit description for the first match
// found that doesn't look like a clearly-marked test fixture. Empty
// return means clean.
func scanPII(content string) string {
	// Quick allow-pass: explicit TEST marker.
	testMarker := regexp.MustCompile(`\bTEST\b|\bMOCK\b|\bSAMPLE\b`)
	allow := testMarker.MatchString(content)

	if loc := cpfRE.FindStringIndex(content); loc != nil {
		snippet := content[loc[0]:loc[1]]
		if allow {
			// Snippet near a TEST marker is plausibly fine.
			// Heuristic: if the line containing the hit has TEST somewhere, allow.
			line := lineAround(content, loc[0])
			if testMarker.MatchString(line) {
				return ""
			}
		}
		return "CPF-like number " + snippet
	}
	if loc := emailRE.FindStringIndex(content); loc != nil {
		// Real email — don't allow even with TEST marker, since it's likely a leak.
		return "email-like address " + content[loc[0]:loc[1]]
	}
	if loc := phoneRE.FindStringIndex(content); loc != nil {
		snippet := content[loc[0]:loc[1]]
		if allow {
			line := lineAround(content, loc[0])
			if testMarker.MatchString(line) {
				return ""
			}
		}
		return "phone-like number " + snippet
	}
	return ""
}

func lineAround(s string, pos int) string {
	start := strings.LastIndex(s[:pos], "\n")
	if start < 0 {
		start = 0
	} else {
		start++
	}
	end := strings.Index(s[pos:], "\n")
	if end < 0 {
		end = len(s)
	} else {
		end += pos
	}
	return s[start:end]
}

// --- frontmatter → YAML (minimal: only the keys soul.md uses) ----------

// frontmatterToYAML emits the soul frontmatter as a stable YAML block.
// We don't pull in a YAML lib — only need the (id, version, voice.*,
// constraints.*, compatible_skills) subset documented in the design.
func frontmatterToYAML(fm map[string]interface{}) (string, error) {
	if fm == nil {
		return "", nil
	}
	// Stable key order so PR diffs stay readable.
	order := []string{"schema", "id", "version", "description", "voice", "constraints", "compatible_skills"}
	seen := map[string]bool{}
	var b bytes.Buffer
	for _, k := range order {
		v, ok := fm[k]
		if !ok {
			continue
		}
		seen[k] = true
		if err := encodeYAMLKey(&b, k, v, 0); err != nil {
			return "", err
		}
	}
	// Append any unknown top-level keys in sorted order — forward-compat.
	for k, v := range fm {
		if seen[k] {
			continue
		}
		if err := encodeYAMLKey(&b, k, v, 0); err != nil {
			return "", err
		}
	}
	return b.String(), nil
}

func encodeYAMLKey(b *bytes.Buffer, key string, v interface{}, indent int) error {
	pad := strings.Repeat("  ", indent)
	switch val := v.(type) {
	case string:
		fmt.Fprintf(b, "%s%s: %s\n", pad, key, yamlString(val))
	case bool:
		fmt.Fprintf(b, "%s%s: %v\n", pad, key, val)
	case float64:
		// JSON unmarshals all numbers as float64.
		if val == float64(int(val)) {
			fmt.Fprintf(b, "%s%s: %d\n", pad, key, int(val))
		} else {
			fmt.Fprintf(b, "%s%s: %v\n", pad, key, val)
		}
	case []interface{}:
		fmt.Fprintf(b, "%s%s:\n", pad, key)
		for _, item := range val {
			fmt.Fprintf(b, "%s  - %s\n", pad, yamlString(fmt.Sprintf("%v", item)))
		}
	case map[string]interface{}:
		fmt.Fprintf(b, "%s%s:\n", pad, key)
		for k, vv := range val {
			if err := encodeYAMLKey(b, k, vv, indent+1); err != nil {
				return err
			}
		}
	case nil:
		fmt.Fprintf(b, "%s%s: null\n", pad, key)
	default:
		return fmt.Errorf("unsupported YAML value type %T", v)
	}
	return nil
}

// yamlString quotes a string if it contains characters that would be
// ambiguous as a bare scalar.
func yamlString(s string) string {
	if s == "" {
		return `""`
	}
	if strings.ContainsAny(s, ":#\n\"'[]{}") || strings.HasPrefix(s, " ") || strings.HasSuffix(s, " ") {
		// Double-quoted style with minimal escaping.
		escaped := strings.ReplaceAll(s, `"`, `\"`)
		return `"` + escaped + `"`
	}
	return s
}

// --- GitHub Contents API client ----------------------------------------

type promoteResult struct {
	PRURL   string
	HeadSha string
}

func promoteToGitHub(cfg *promoteConfig, branch, title, body string, files map[string]string) (*promoteResult, error) {
	ghc := newGitHubClient(cfg.Token, cfg.Repo)

	// 1. Resolve base branch HEAD sha.
	baseSha, err := ghc.getRefSha("heads/" + cfg.Base)
	if err != nil {
		return nil, fmt.Errorf("resolve base %s: %w", cfg.Base, err)
	}

	// 2. Create the playground branch from baseSha. If it already
	//    exists (re-promote of same draft after a revert), reuse it.
	if err := ghc.createBranch(branch, baseSha); err != nil {
		if !strings.Contains(err.Error(), "Reference already exists") {
			return nil, fmt.Errorf("create branch %s: %w", branch, err)
		}
	}

	// 3. Write each file on that branch via the Contents API.
	headSha := baseSha
	for path, content := range files {
		commit, err := ghc.putFile(path, branch, content, fmt.Sprintf("%s: %s", title, path))
		if err != nil {
			return nil, fmt.Errorf("put %s: %w", path, err)
		}
		headSha = commit
	}

	// 4. Open the PR.
	prURL, err := ghc.openPR(branch, cfg.Base, title, body)
	if err != nil {
		return nil, fmt.Errorf("open PR: %w", err)
	}

	return &promoteResult{PRURL: prURL, HeadSha: headSha}, nil
}

type githubClient struct {
	token string
	repo  string
	http  *http.Client
}

func newGitHubClient(token, repo string) *githubClient {
	return &githubClient{
		token: token,
		repo:  repo,
		http:  &http.Client{Timeout: 20 * time.Second},
	}
}

func (g *githubClient) do(method, path string, payload, out interface{}) error {
	url := "https://api.github.com/repos/" + g.repo + path
	var body io.Reader
	if payload != nil {
		buf, err := json.Marshal(payload)
		if err != nil {
			return fmt.Errorf("marshal: %w", err)
		}
		body = bytes.NewReader(buf)
	}
	req, err := http.NewRequest(method, url, body)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+g.token)
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("X-GitHub-Api-Version", "2022-11-28")
	req.Header.Set("Content-Type", "application/json")

	resp, err := g.http.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	respBody, _ := io.ReadAll(resp.Body)
	if resp.StatusCode >= 400 {
		return fmt.Errorf("%s %s: %d %s", method, path, resp.StatusCode, string(respBody))
	}
	if out != nil && len(respBody) > 0 {
		if err := json.Unmarshal(respBody, out); err != nil {
			return fmt.Errorf("decode response: %w", err)
		}
	}
	return nil
}

func (g *githubClient) getRefSha(ref string) (string, error) {
	var out struct {
		Object struct{ Sha string } `json:"object"`
	}
	if err := g.do("GET", "/git/ref/"+ref, nil, &out); err != nil {
		return "", err
	}
	return out.Object.Sha, nil
}

func (g *githubClient) createBranch(branch, fromSha string) error {
	return g.do("POST", "/git/refs", map[string]string{
		"ref": "refs/heads/" + branch,
		"sha": fromSha,
	}, nil)
}

func (g *githubClient) putFile(path, branch, content, message string) (string, error) {
	// PUT /contents/{path}: create or update file. We always create
	// (no sha parameter), so re-promoting a draft that wrote to the
	// same path will 422 — handled at the branch level (each draft
	// targets its own branch).
	var out struct {
		Commit struct{ Sha string } `json:"commit"`
	}
	payload := map[string]string{
		"message": message,
		"content": base64.StdEncoding.EncodeToString([]byte(content)),
		"branch":  branch,
	}
	if err := g.do("PUT", "/contents/"+path, payload, &out); err != nil {
		return "", err
	}
	return out.Commit.Sha, nil
}

func (g *githubClient) openPR(head, base, title, body string) (string, error) {
	var out struct {
		HTMLURL string `json:"html_url"`
	}
	payload := map[string]string{
		"title": title,
		"head":  head,
		"base":  base,
		"body":  body,
	}
	if err := g.do("POST", "/pulls", payload, &out); err != nil {
		return "", err
	}
	return out.HTMLURL, nil
}

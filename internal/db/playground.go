package db

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"time"

	"github.com/lib/pq"
)

// SkillDraft is one row in skill_drafts. Files is a flat path→content
// map; nested paths (e.g. "references/leads.json") use "/" verbatim.
type SkillDraft struct {
	ID              string
	Name            string
	Description     string
	AuthorUserID    sql.NullString
	WorkspaceID     sql.NullString // tenant scope; NULL = system template (migration 035)
	Files           map[string]string
	Status          string
	Visibility      string // 'private' | 'shared' (migration 036)
	PromotedPRURL   sql.NullString
	PromotedCommit  sql.NullString
	PromotedPRState sql.NullString // 'open' | 'merged' | 'closed' | NULL (migration 033)
	CreatedAt       time.Time
	UpdatedAt       time.Time
}

// SoulDraft is one row in soul_drafts. Frontmatter is held as raw map
// so the API layer can validate against the schema before persisting.
type SoulDraft struct {
	ID              string
	Name            string
	Description     string
	AuthorUserID    sql.NullString
	WorkspaceID     sql.NullString // tenant scope; NULL = system template (migration 035)
	Frontmatter     map[string]interface{}
	Body            string
	SchemaVersion   string
	Status          string
	Visibility      string // 'private' | 'shared' (migration 036)
	PromotedPRURL   sql.NullString
	PromotedCommit  sql.NullString
	PromotedPRState sql.NullString // 'open' | 'merged' | 'closed' | NULL (migration 033)
	CreatedAt       time.Time
	UpdatedAt       time.Time
}

// SandboxComposition records which soul + skills a sandbox boots with.
// Refs follow the grammar in docs/playground-design.md §4.3.
type SandboxComposition struct {
	SandboxID      string
	SoulRef        sql.NullString
	SkillRefs      []string
	SkillConfig    map[string]map[string]interface{}
	TrackUpstream  bool
	CreatedAt      time.Time
}

// --- Skill drafts ----------------------------------------------------------

// CreateSkillDraft inserts a new draft. workspaceID is the tenant scope
// (migration 035); empty = system template visible to all. Backward-compat:
// existing callers that pass "" effectively create a system-wide draft —
// new code paths set it to the caller's chosen workspace.
func (db *DB) CreateSkillDraft(name, description, authorUserID, workspaceID string) (*SkillDraft, error) {
	d := &SkillDraft{}
	err := db.QueryRow(
		`INSERT INTO skill_drafts (name, description, author_user_id, workspace_id)
		VALUES ($1, $2, $3, $4)
		RETURNING id, name, description, author_user_id, workspace_id, files, status, visibility, promoted_pr_url, promoted_commit, promoted_pr_state, created_at, updated_at`,
		name, description, nullIfEmpty(authorUserID), nullIfEmpty(workspaceID),
	).Scan(&d.ID, &d.Name, &d.Description, &d.AuthorUserID, &d.WorkspaceID, jsonScanner(&d.Files), &d.Status, &d.Visibility, &d.PromotedPRURL, &d.PromotedCommit, &d.PromotedPRState, &d.CreatedAt, &d.UpdatedAt)
	if err != nil {
		return nil, fmt.Errorf("create skill draft: %w", err)
	}
	return d, nil
}

func (db *DB) GetSkillDraft(id string) (*SkillDraft, error) {
	d := &SkillDraft{}
	err := db.QueryRow(
		`SELECT id, name, description, author_user_id, workspace_id, files, status, visibility, promoted_pr_url, promoted_commit, promoted_pr_state, created_at, updated_at
		FROM skill_drafts WHERE id = $1`,
		id,
	).Scan(&d.ID, &d.Name, &d.Description, &d.AuthorUserID, &d.WorkspaceID, jsonScanner(&d.Files), &d.Status, &d.Visibility, &d.PromotedPRURL, &d.PromotedCommit, &d.PromotedPRState, &d.CreatedAt, &d.UpdatedAt)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("get skill draft: %w", err)
	}
	return d, nil
}

func (db *DB) ListSkillDraftsByAuthor(authorUserID string) ([]*SkillDraft, error) {
	rows, err := db.Query(
		`SELECT id, name, description, author_user_id, workspace_id, files, status, visibility, promoted_pr_url, promoted_commit, promoted_pr_state, created_at, updated_at
		FROM skill_drafts WHERE author_user_id = $1 AND status != 'archived' ORDER BY updated_at DESC`,
		authorUserID,
	)
	if err != nil {
		return nil, fmt.Errorf("list skill drafts: %w", err)
	}
	defer rows.Close()

	var drafts []*SkillDraft
	for rows.Next() {
		d := &SkillDraft{}
		if err := rows.Scan(&d.ID, &d.Name, &d.Description, &d.AuthorUserID, &d.WorkspaceID, jsonScanner(&d.Files), &d.Status, &d.Visibility, &d.PromotedPRURL, &d.PromotedCommit, &d.PromotedPRState, &d.CreatedAt, &d.UpdatedAt); err != nil {
			return nil, fmt.Errorf("scan skill draft: %w", err)
		}
		drafts = append(drafts, d)
	}
	return drafts, rows.Err()
}

// ListSkillDraftsForScope returns drafts visible to a user under the
// tenant-scoped catalog rules (improvements.md #17):
//   - Drafts the user authored, OR
//   - Drafts whose workspace_id matches one the user belongs to, OR
//   - System templates (workspace_id IS NULL)
// Filtered to non-archived; ordered by updated_at DESC.
func (db *DB) ListSkillDraftsForScope(authorUserID string, workspaceIDs []string) ([]*SkillDraft, error) {
	rows, err := db.Query(
		`SELECT id, name, description, author_user_id, workspace_id, files, status, visibility, promoted_pr_url, promoted_commit, promoted_pr_state, created_at, updated_at
		 FROM skill_drafts
		 WHERE status != 'archived'
		   AND (
		     author_user_id = $1
		     OR workspace_id IS NULL
		     OR workspace_id = ANY($2)
		   )
		 ORDER BY updated_at DESC`,
		authorUserID, pq.Array(workspaceIDs),
	)
	if err != nil {
		return nil, fmt.Errorf("list skill drafts (scoped): %w", err)
	}
	defer rows.Close()
	var drafts []*SkillDraft
	for rows.Next() {
		d := &SkillDraft{}
		if err := rows.Scan(&d.ID, &d.Name, &d.Description, &d.AuthorUserID, &d.WorkspaceID, jsonScanner(&d.Files), &d.Status, &d.Visibility, &d.PromotedPRURL, &d.PromotedCommit, &d.PromotedPRState, &d.CreatedAt, &d.UpdatedAt); err != nil {
			return nil, fmt.Errorf("scan skill draft: %w", err)
		}
		drafts = append(drafts, d)
	}
	return drafts, rows.Err()
}

// ListSoulDraftsForScope mirrors ListSkillDraftsForScope for soul drafts.
func (db *DB) ListSoulDraftsForScope(authorUserID string, workspaceIDs []string) ([]*SoulDraft, error) {
	rows, err := db.Query(
		`SELECT id, name, description, author_user_id, workspace_id, frontmatter, body, schema_version, status, visibility, promoted_pr_url, promoted_commit, promoted_pr_state, created_at, updated_at
		 FROM soul_drafts
		 WHERE status != 'archived'
		   AND (
		     author_user_id = $1
		     OR workspace_id IS NULL
		     OR workspace_id = ANY($2)
		   )
		 ORDER BY updated_at DESC`,
		authorUserID, pq.Array(workspaceIDs),
	)
	if err != nil {
		return nil, fmt.Errorf("list soul drafts (scoped): %w", err)
	}
	defer rows.Close()
	var drafts []*SoulDraft
	for rows.Next() {
		d := &SoulDraft{}
		if err := rows.Scan(&d.ID, &d.Name, &d.Description, &d.AuthorUserID, &d.WorkspaceID, jsonScanner(&d.Frontmatter), &d.Body, &d.SchemaVersion, &d.Status, &d.Visibility, &d.PromotedPRURL, &d.PromotedCommit, &d.PromotedPRState, &d.CreatedAt, &d.UpdatedAt); err != nil {
			return nil, fmt.Errorf("scan soul draft: %w", err)
		}
		drafts = append(drafts, d)
	}
	return drafts, rows.Err()
}

// CountSkillDraftsByAuthor counts non-archived drafts; used for quota.
func (db *DB) CountSkillDraftsByAuthor(authorUserID string) (int, error) {
	var n int
	err := db.QueryRow(
		`SELECT COUNT(*) FROM skill_drafts WHERE author_user_id = $1 AND status != 'archived'`,
		authorUserID,
	).Scan(&n)
	return n, err
}

// UpdateSkillDraftFiles replaces the files map atomically + bumps updated_at.
func (db *DB) UpdateSkillDraftFiles(id string, files map[string]string) error {
	payload, err := json.Marshal(files)
	if err != nil {
		return fmt.Errorf("marshal files: %w", err)
	}
	res, err := db.Exec(
		`UPDATE skill_drafts SET files = $2, updated_at = NOW() WHERE id = $1 AND status = 'draft'`,
		id, payload,
	)
	if err != nil {
		return fmt.Errorf("update skill draft files: %w", err)
	}
	rows, _ := res.RowsAffected()
	if rows == 0 {
		return fmt.Errorf("skill draft %s: not found or not editable (status != 'draft')", id)
	}
	return nil
}

func (db *DB) ArchiveSkillDraft(id string) error {
	_, err := db.Exec(`UPDATE skill_drafts SET status = 'archived', updated_at = NOW() WHERE id = $1`, id)
	return err
}

// TryPromoteSkillDraft atomically flips status from 'draft' to
// 'promoting' if and only if it's currently 'draft'. Returns (true,
// nil) on success, (false, nil) when another promote is already in
// flight or the draft is in a non-promotable state.
func (db *DB) TryPromoteSkillDraft(id string) (bool, error) {
	res, err := db.Exec(
		`UPDATE skill_drafts SET status = 'promoting', updated_at = NOW()
		WHERE id = $1 AND status = 'draft'`,
		id,
	)
	if err != nil {
		return false, fmt.Errorf("try promote skill draft: %w", err)
	}
	rows, _ := res.RowsAffected()
	return rows == 1, nil
}

func (db *DB) CompletePromoteSkillDraft(id, prURL, commitSha string) error {
	_, err := db.Exec(
		`UPDATE skill_drafts SET status = 'promoted', promoted_pr_url = $2, promoted_commit = $3, updated_at = NOW()
		WHERE id = $1`,
		id, prURL, commitSha,
	)
	return err
}

func (db *DB) RevertPromoteSkillDraft(id string) error {
	_, err := db.Exec(
		`UPDATE skill_drafts SET status = 'draft', updated_at = NOW()
		WHERE id = $1 AND status = 'promoting'`,
		id,
	)
	return err
}

// --- Soul drafts -----------------------------------------------------------

// CreateSoulDraft inserts a new soul draft. workspaceID is the tenant scope
// (migration 035); empty = system template visible to all.
func (db *DB) CreateSoulDraft(name, description, authorUserID, workspaceID string) (*SoulDraft, error) {
	d := &SoulDraft{}
	err := db.QueryRow(
		`INSERT INTO soul_drafts (name, description, author_user_id, workspace_id)
		VALUES ($1, $2, $3, $4)
		RETURNING id, name, description, author_user_id, workspace_id, frontmatter, body, schema_version, status, visibility, promoted_pr_url, promoted_commit, promoted_pr_state, created_at, updated_at`,
		name, description, nullIfEmpty(authorUserID), nullIfEmpty(workspaceID),
	).Scan(&d.ID, &d.Name, &d.Description, &d.AuthorUserID, &d.WorkspaceID, jsonScanner(&d.Frontmatter), &d.Body, &d.SchemaVersion, &d.Status, &d.Visibility, &d.PromotedPRURL, &d.PromotedCommit, &d.PromotedPRState, &d.CreatedAt, &d.UpdatedAt)
	if err != nil {
		return nil, fmt.Errorf("create soul draft: %w", err)
	}
	return d, nil
}

func (db *DB) GetSoulDraft(id string) (*SoulDraft, error) {
	d := &SoulDraft{}
	err := db.QueryRow(
		`SELECT id, name, description, author_user_id, workspace_id, frontmatter, body, schema_version, status, visibility, promoted_pr_url, promoted_commit, promoted_pr_state, created_at, updated_at
		FROM soul_drafts WHERE id = $1`,
		id,
	).Scan(&d.ID, &d.Name, &d.Description, &d.AuthorUserID, &d.WorkspaceID, jsonScanner(&d.Frontmatter), &d.Body, &d.SchemaVersion, &d.Status, &d.Visibility, &d.PromotedPRURL, &d.PromotedCommit, &d.PromotedPRState, &d.CreatedAt, &d.UpdatedAt)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("get soul draft: %w", err)
	}
	return d, nil
}

func (db *DB) ListSoulDraftsByAuthor(authorUserID string) ([]*SoulDraft, error) {
	rows, err := db.Query(
		`SELECT id, name, description, author_user_id, workspace_id, frontmatter, body, schema_version, status, visibility, promoted_pr_url, promoted_commit, promoted_pr_state, created_at, updated_at
		FROM soul_drafts WHERE author_user_id = $1 AND status != 'archived' ORDER BY updated_at DESC`,
		authorUserID,
	)
	if err != nil {
		return nil, fmt.Errorf("list soul drafts: %w", err)
	}
	defer rows.Close()

	var drafts []*SoulDraft
	for rows.Next() {
		d := &SoulDraft{}
		if err := rows.Scan(&d.ID, &d.Name, &d.Description, &d.AuthorUserID, &d.WorkspaceID, jsonScanner(&d.Frontmatter), &d.Body, &d.SchemaVersion, &d.Status, &d.Visibility, &d.PromotedPRURL, &d.PromotedCommit, &d.PromotedPRState, &d.CreatedAt, &d.UpdatedAt); err != nil {
			return nil, fmt.Errorf("scan soul draft: %w", err)
		}
		drafts = append(drafts, d)
	}
	return drafts, rows.Err()
}

func (db *DB) CountSoulDraftsByAuthor(authorUserID string) (int, error) {
	var n int
	err := db.QueryRow(
		`SELECT COUNT(*) FROM soul_drafts WHERE author_user_id = $1 AND status != 'archived'`,
		authorUserID,
	).Scan(&n)
	return n, err
}

// UpdateSoulDraft replaces frontmatter + body atomically.
func (db *DB) UpdateSoulDraft(id string, frontmatter map[string]interface{}, body string) error {
	payload, err := json.Marshal(frontmatter)
	if err != nil {
		return fmt.Errorf("marshal frontmatter: %w", err)
	}
	res, err := db.Exec(
		`UPDATE soul_drafts SET frontmatter = $2, body = $3, updated_at = NOW() WHERE id = $1 AND status = 'draft'`,
		id, payload, body,
	)
	if err != nil {
		return fmt.Errorf("update soul draft: %w", err)
	}
	rows, _ := res.RowsAffected()
	if rows == 0 {
		return fmt.Errorf("soul draft %s: not found or not editable (status != 'draft')", id)
	}
	return nil
}

func (db *DB) ArchiveSoulDraft(id string) error {
	_, err := db.Exec(`UPDATE soul_drafts SET status = 'archived', updated_at = NOW() WHERE id = $1`, id)
	return err
}

func (db *DB) TryPromoteSoulDraft(id string) (bool, error) {
	res, err := db.Exec(
		`UPDATE soul_drafts SET status = 'promoting', updated_at = NOW()
		WHERE id = $1 AND status = 'draft'`,
		id,
	)
	if err != nil {
		return false, fmt.Errorf("try promote soul draft: %w", err)
	}
	rows, _ := res.RowsAffected()
	return rows == 1, nil
}

func (db *DB) CompletePromoteSoulDraft(id, prURL, commitSha string) error {
	_, err := db.Exec(
		`UPDATE soul_drafts SET status = 'promoted', promoted_pr_url = $2, promoted_commit = $3, updated_at = NOW()
		WHERE id = $1`,
		id, prURL, commitSha,
	)
	return err
}

func (db *DB) RevertPromoteSoulDraft(id string) error {
	_, err := db.Exec(
		`UPDATE soul_drafts SET status = 'draft', updated_at = NOW()
		WHERE id = $1 AND status = 'promoting'`,
		id,
	)
	return err
}

// --- Sandbox compositions --------------------------------------------------

func (db *DB) CreateSandboxComposition(sandboxID, soulRef string, skillRefs []string, skillConfig map[string]map[string]interface{}, trackUpstream bool) error {
	payload, err := json.Marshal(skillConfig)
	if err != nil {
		return fmt.Errorf("marshal skill config: %w", err)
	}
	_, err = db.Exec(
		`INSERT INTO sandbox_compositions (sandbox_id, soul_ref, skill_refs, skill_config, track_upstream)
		VALUES ($1, $2, $3, $4, $5)`,
		sandboxID, nullIfEmpty(soulRef), pq.Array(skillRefs), payload, trackUpstream,
	)
	if err != nil {
		return fmt.Errorf("create sandbox composition: %w", err)
	}
	return nil
}

func (db *DB) GetSandboxComposition(sandboxID string) (*SandboxComposition, error) {
	c := &SandboxComposition{}
	err := db.QueryRow(
		`SELECT sandbox_id, soul_ref, skill_refs, skill_config, track_upstream, created_at
		FROM sandbox_compositions WHERE sandbox_id = $1`,
		sandboxID,
	).Scan(&c.SandboxID, &c.SoulRef, pq.Array(&c.SkillRefs), jsonScanner(&c.SkillConfig), &c.TrackUpstream, &c.CreatedAt)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("get sandbox composition: %w", err)
	}
	return c, nil
}

// --- Playground test sandboxes (quota + reaper) ----------------------------

func (db *DB) CreatePlaygroundTestSandbox(sandboxID, authorUserID string, ttl time.Duration) error {
	_, err := db.Exec(
		`INSERT INTO playground_test_sandboxes (sandbox_id, author_user_id, expires_at)
		VALUES ($1, $2, NOW() + $3::interval)`,
		sandboxID, authorUserID, fmt.Sprintf("%d seconds", int(ttl.Seconds())),
	)
	return err
}

func (db *DB) CountActivePlaygroundTestSandboxes(authorUserID string) (int, error) {
	var n int
	err := db.QueryRow(
		`SELECT COUNT(*) FROM playground_test_sandboxes WHERE author_user_id = $1 AND expires_at > NOW()`,
		authorUserID,
	).Scan(&n)
	return n, err
}

// ListExpiredPlaygroundTestSandboxes returns sandbox IDs whose TTL elapsed.
// Used by the reaper goroutine.
func (db *DB) ListExpiredPlaygroundTestSandboxes() ([]string, error) {
	rows, err := db.Query(
		`SELECT sandbox_id FROM playground_test_sandboxes WHERE expires_at <= NOW()`,
	)
	if err != nil {
		return nil, fmt.Errorf("list expired test sandboxes: %w", err)
	}
	defer rows.Close()

	var ids []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		ids = append(ids, id)
	}
	return ids, rows.Err()
}

// --- promote PR state (migration 033) --------------------------------------

// PromoteStateRow is a thin projection used by the PR-state poller. Kind is
// "skill" or "soul"; PRNumber is parsed from the stored URL when scheduling
// the GitHub API call.
type PromoteStateRow struct {
	Kind            string // "skill" | "soul"
	DraftID         string
	PromotedPRURL   string
	PromotedPRState sql.NullString
}

// ListPromotedDraftsForPolling returns every promoted draft (skill + soul)
// whose recorded GitHub state is NULL or "open" — those are the only states
// the poller can change. Closed / merged are terminal.
func (db *DB) ListPromotedDraftsForPolling() ([]PromoteStateRow, error) {
	var out []PromoteStateRow
	q := func(table, kind string) error {
		rows, err := db.Query(
			`SELECT id, promoted_pr_url, promoted_pr_state
			 FROM ` + table + `
			 WHERE status = 'promoted' AND promoted_pr_url IS NOT NULL
			   AND (promoted_pr_state IS NULL OR promoted_pr_state = 'open')`,
		)
		if err != nil {
			return fmt.Errorf("list %s for polling: %w", table, err)
		}
		defer rows.Close()
		for rows.Next() {
			var r PromoteStateRow
			r.Kind = kind
			if err := rows.Scan(&r.DraftID, &r.PromotedPRURL, &r.PromotedPRState); err != nil {
				return fmt.Errorf("scan %s row: %w", table, err)
			}
			out = append(out, r)
		}
		return rows.Err()
	}
	if err := q("skill_drafts", "skill"); err != nil {
		return nil, err
	}
	if err := q("soul_drafts", "soul"); err != nil {
		return nil, err
	}
	return out, nil
}

// UpdatePromotedPRState writes the observed GitHub PR state for a promoted
// draft. Validates kind + state at the app boundary.
func (db *DB) UpdatePromotedPRState(kind, draftID, state string) error {
	if state != "open" && state != "merged" && state != "closed" {
		return fmt.Errorf("invalid promoted_pr_state %q", state)
	}
	var table string
	switch kind {
	case "skill":
		table = "skill_drafts"
	case "soul":
		table = "soul_drafts"
	default:
		return fmt.Errorf("invalid kind %q", kind)
	}
	_, err := db.Exec(
		`UPDATE `+table+` SET promoted_pr_state = $2, updated_at = NOW() WHERE id = $1`,
		draftID, state,
	)
	return err
}

// --- Marketplace (improvements.md #18) ------------------------------------

// ListSharedSkillDrafts returns all skill drafts with visibility='shared',
// ordered by updated_at DESC. Used by GET /api/marketplace/skills.
func (db *DB) ListSharedSkillDrafts() ([]*SkillDraft, error) {
	rows, err := db.Query(
		`SELECT id, name, description, author_user_id, workspace_id, files, status, visibility, promoted_pr_url, promoted_commit, promoted_pr_state, created_at, updated_at
		 FROM skill_drafts WHERE visibility = 'shared' AND status != 'archived'
		 ORDER BY updated_at DESC`,
	)
	if err != nil {
		return nil, fmt.Errorf("list shared skill drafts: %w", err)
	}
	defer rows.Close()
	var drafts []*SkillDraft
	for rows.Next() {
		d := &SkillDraft{}
		if err := rows.Scan(&d.ID, &d.Name, &d.Description, &d.AuthorUserID, &d.WorkspaceID, jsonScanner(&d.Files), &d.Status, &d.Visibility, &d.PromotedPRURL, &d.PromotedCommit, &d.PromotedPRState, &d.CreatedAt, &d.UpdatedAt); err != nil {
			return nil, fmt.Errorf("scan shared skill draft: %w", err)
		}
		drafts = append(drafts, d)
	}
	return drafts, rows.Err()
}

// ListSharedSoulDrafts mirrors ListSharedSkillDrafts for souls.
func (db *DB) ListSharedSoulDrafts() ([]*SoulDraft, error) {
	rows, err := db.Query(
		`SELECT id, name, description, author_user_id, workspace_id, frontmatter, body, schema_version, status, visibility, promoted_pr_url, promoted_commit, promoted_pr_state, created_at, updated_at
		 FROM soul_drafts WHERE visibility = 'shared' AND status != 'archived'
		 ORDER BY updated_at DESC`,
	)
	if err != nil {
		return nil, fmt.Errorf("list shared soul drafts: %w", err)
	}
	defer rows.Close()
	var drafts []*SoulDraft
	for rows.Next() {
		d := &SoulDraft{}
		if err := rows.Scan(&d.ID, &d.Name, &d.Description, &d.AuthorUserID, &d.WorkspaceID, jsonScanner(&d.Frontmatter), &d.Body, &d.SchemaVersion, &d.Status, &d.Visibility, &d.PromotedPRURL, &d.PromotedCommit, &d.PromotedPRState, &d.CreatedAt, &d.UpdatedAt); err != nil {
			return nil, fmt.Errorf("scan shared soul draft: %w", err)
		}
		drafts = append(drafts, d)
	}
	return drafts, rows.Err()
}

// ListSystemSkillDrafts returns all skill drafts with workspace_id IS NULL
// (system-wide templates), excluding archived. Used by admin management UI.
func (db *DB) ListSystemSkillDrafts() ([]*SkillDraft, error) {
	rows, err := db.Query(
		`SELECT id, name, description, author_user_id, workspace_id, files, status, visibility, promoted_pr_url, promoted_commit, promoted_pr_state, created_at, updated_at
		 FROM skill_drafts WHERE workspace_id IS NULL AND status != 'archived'
		 ORDER BY updated_at DESC`,
	)
	if err != nil {
		return nil, fmt.Errorf("list system skill drafts: %w", err)
	}
	defer rows.Close()
	var drafts []*SkillDraft
	for rows.Next() {
		d := &SkillDraft{}
		if err := rows.Scan(&d.ID, &d.Name, &d.Description, &d.AuthorUserID, &d.WorkspaceID, jsonScanner(&d.Files), &d.Status, &d.Visibility, &d.PromotedPRURL, &d.PromotedCommit, &d.PromotedPRState, &d.CreatedAt, &d.UpdatedAt); err != nil {
			return nil, fmt.Errorf("scan system skill draft: %w", err)
		}
		drafts = append(drafts, d)
	}
	return drafts, rows.Err()
}

// ListSystemSoulDrafts mirrors ListSystemSkillDrafts for souls.
func (db *DB) ListSystemSoulDrafts() ([]*SoulDraft, error) {
	rows, err := db.Query(
		`SELECT id, name, description, author_user_id, workspace_id, frontmatter, body, schema_version, status, visibility, promoted_pr_url, promoted_commit, promoted_pr_state, created_at, updated_at
		 FROM soul_drafts WHERE workspace_id IS NULL AND status != 'archived'
		 ORDER BY updated_at DESC`,
	)
	if err != nil {
		return nil, fmt.Errorf("list system soul drafts: %w", err)
	}
	defer rows.Close()
	var drafts []*SoulDraft
	for rows.Next() {
		d := &SoulDraft{}
		if err := rows.Scan(&d.ID, &d.Name, &d.Description, &d.AuthorUserID, &d.WorkspaceID, jsonScanner(&d.Frontmatter), &d.Body, &d.SchemaVersion, &d.Status, &d.Visibility, &d.PromotedPRURL, &d.PromotedCommit, &d.PromotedPRState, &d.CreatedAt, &d.UpdatedAt); err != nil {
			return nil, fmt.Errorf("scan system soul draft: %w", err)
		}
		drafts = append(drafts, d)
	}
	return drafts, rows.Err()
}

// SetSkillDraftVisibility sets visibility='shared'|'private'. Admin-only at API layer.
func (db *DB) SetSkillDraftVisibility(id, visibility string) error {
	if visibility != "private" && visibility != "shared" {
		return fmt.Errorf("invalid visibility %q", visibility)
	}
	res, err := db.Exec(
		`UPDATE skill_drafts SET visibility = $2, updated_at = NOW() WHERE id = $1`,
		id, visibility,
	)
	if err != nil {
		return fmt.Errorf("set skill visibility: %w", err)
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return fmt.Errorf("skill draft %s: not found", id)
	}
	return nil
}

// SetSoulDraftVisibility mirrors SetSkillDraftVisibility for souls.
func (db *DB) SetSoulDraftVisibility(id, visibility string) error {
	if visibility != "private" && visibility != "shared" {
		return fmt.Errorf("invalid visibility %q", visibility)
	}
	res, err := db.Exec(
		`UPDATE soul_drafts SET visibility = $2, updated_at = NOW() WHERE id = $1`,
		id, visibility,
	)
	if err != nil {
		return fmt.Errorf("set soul visibility: %w", err)
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return fmt.Errorf("soul draft %s: not found", id)
	}
	return nil
}

// ForkSkillDraft copies a shared skill draft into the caller's workspace as a
// new private draft. Name gets "-fork" suffix to avoid conflicts.
func (db *DB) ForkSkillDraft(sourceID, callerUserID, workspaceID string) (*SkillDraft, error) {
	src, err := db.GetSkillDraft(sourceID)
	if err != nil || src == nil {
		return nil, fmt.Errorf("source skill draft %s: not found", sourceID)
	}
	if src.Visibility != "shared" {
		return nil, fmt.Errorf("skill draft %s: not shared", sourceID)
	}
	filesJSON, err := json.Marshal(src.Files)
	if err != nil {
		return nil, fmt.Errorf("marshal files: %w", err)
	}
	d := &SkillDraft{}
	err = db.QueryRow(
		`INSERT INTO skill_drafts (name, description, author_user_id, workspace_id, files)
		 VALUES ($1, $2, $3, $4, $5)
		 RETURNING id, name, description, author_user_id, workspace_id, files, status, visibility, promoted_pr_url, promoted_commit, promoted_pr_state, created_at, updated_at`,
		src.Name+"-fork", src.Description, nullIfEmpty(callerUserID), nullIfEmpty(workspaceID), filesJSON,
	).Scan(&d.ID, &d.Name, &d.Description, &d.AuthorUserID, &d.WorkspaceID, jsonScanner(&d.Files), &d.Status, &d.Visibility, &d.PromotedPRURL, &d.PromotedCommit, &d.PromotedPRState, &d.CreatedAt, &d.UpdatedAt)
	if err != nil {
		return nil, fmt.Errorf("fork skill draft: %w", err)
	}
	return d, nil
}

// ForkSoulDraft copies a shared soul draft into the caller's workspace as a
// new private draft. Name gets "-fork" suffix to avoid conflicts.
func (db *DB) ForkSoulDraft(sourceID, callerUserID, workspaceID string) (*SoulDraft, error) {
	src, err := db.GetSoulDraft(sourceID)
	if err != nil || src == nil {
		return nil, fmt.Errorf("source soul draft %s: not found", sourceID)
	}
	if src.Visibility != "shared" {
		return nil, fmt.Errorf("soul draft %s: not shared", sourceID)
	}
	fmJSON, err := json.Marshal(src.Frontmatter)
	if err != nil {
		return nil, fmt.Errorf("marshal frontmatter: %w", err)
	}
	d := &SoulDraft{}
	err = db.QueryRow(
		`INSERT INTO soul_drafts (name, description, author_user_id, workspace_id, frontmatter, body, schema_version)
		 VALUES ($1, $2, $3, $4, $5, $6, $7)
		 RETURNING id, name, description, author_user_id, workspace_id, frontmatter, body, schema_version, status, visibility, promoted_pr_url, promoted_commit, promoted_pr_state, created_at, updated_at`,
		src.Name+"-fork", src.Description, nullIfEmpty(callerUserID), nullIfEmpty(workspaceID), fmJSON, src.Body, src.SchemaVersion,
	).Scan(&d.ID, &d.Name, &d.Description, &d.AuthorUserID, &d.WorkspaceID, jsonScanner(&d.Frontmatter), &d.Body, &d.SchemaVersion, &d.Status, &d.Visibility, &d.PromotedPRURL, &d.PromotedCommit, &d.PromotedPRState, &d.CreatedAt, &d.UpdatedAt)
	if err != nil {
		return nil, fmt.Errorf("fork soul draft: %w", err)
	}
	return d, nil
}

// --- helpers ---------------------------------------------------------------

// jsonScanner returns a sql.Scanner that unmarshals JSONB into the
// destination pointer. Handles both []byte (pq) and string returns.
type jsonScan struct{ dst interface{} }

func jsonScanner(dst interface{}) sql.Scanner { return &jsonScan{dst: dst} }

func (j *jsonScan) Scan(src interface{}) error {
	if src == nil {
		return nil
	}
	var b []byte
	switch s := src.(type) {
	case []byte:
		b = s
	case string:
		b = []byte(s)
	default:
		return fmt.Errorf("jsonScanner: unexpected source type %T", src)
	}
	if len(b) == 0 {
		return nil
	}
	return json.Unmarshal(b, j.dst)
}

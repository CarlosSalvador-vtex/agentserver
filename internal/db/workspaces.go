package db

import (
	"database/sql"
	"errors"
	"fmt"
	"time"
)

// ErrSlugTaken is returned when a workspace slug is already in use.
var ErrSlugTaken = errors.New("slug already taken")

type Workspace struct {
	ID           string
	Name         string
	Slug         string
	K8sNamespace sql.NullString
	// ChannelRoutingStrategy controls how IM channels map to sandboxes:
	// "shared" (N channels → 1 sandbox), "per_agent" (1:1), or "hybrid"
	// (manual). Defaults to "shared".
	ChannelRoutingStrategy string
	CreatedAt              time.Time
	UpdatedAt              time.Time
}

type WorkspaceVolume struct {
	ID          string
	WorkspaceID string
	PVCName     string
	MountPath   string
	CreatedAt   time.Time
}

type WorkspaceMember struct {
	WorkspaceID string
	UserID      string
	Role        string
	CreatedAt   time.Time
}

// CreateWorkspaceWithSlug creates a workspace with an auto-generated slug
// derived from name. On collision, suffixes -2, -3, ... until unique.
func (db *DB) CreateWorkspaceWithSlug(id, name, k8sNamespace string) (string, error) {
	base := Slugify(name)
	if err := ValidateSlug(base); err != nil {
		base = base + "-1"
	}
	slug := base
	for i := 2; ; i++ {
		taken, err := db.SlugExists(slug)
		if err != nil {
			return "", err
		}
		if !taken {
			break
		}
		slug = fmt.Sprintf("%s-%d", base, i)
		if i > 1000 {
			return "", fmt.Errorf("could not allocate unique slug for %q", name)
		}
	}
	var ns sql.NullString
	if k8sNamespace != "" {
		ns = sql.NullString{String: k8sNamespace, Valid: true}
	}
	_, err := db.Exec(
		`INSERT INTO workspaces (id, name, slug, k8s_namespace, created_at, updated_at)
		 VALUES ($1, $2, $3, $4, NOW(), NOW())`,
		id, name, slug, ns,
	)
	if err != nil {
		return "", fmt.Errorf("create workspace: %w", err)
	}
	return slug, nil
}

func (db *DB) CreateWorkspace(id, name string) error {
	_, err := db.CreateWorkspaceWithSlug(id, name, "")
	return err
}

// EnsureWorkspace creates the workspace when missing. Idempotent for tests and setup.
func (db *DB) EnsureWorkspace(id, name string) error {
	w, err := db.GetWorkspace(id)
	if err != nil {
		return err
	}
	if w != nil {
		return nil
	}
	return db.CreateWorkspace(id, name)
}

// CreateWorkspaceExplicit creates a workspace with a caller-provided slug.
func (db *DB) CreateWorkspaceExplicit(id, name, slug, k8sNamespace string) error {
	if err := ValidateSlug(slug); err != nil {
		return err
	}
	taken, err := db.SlugExists(slug)
	if err != nil {
		return err
	}
	if taken {
		return ErrSlugTaken
	}
	var ns sql.NullString
	if k8sNamespace != "" {
		ns = sql.NullString{String: k8sNamespace, Valid: true}
	}
	_, err = db.Exec(
		`INSERT INTO workspaces (id, name, slug, k8s_namespace, created_at, updated_at)
		 VALUES ($1, $2, $3, $4, NOW(), NOW())`,
		id, name, slug, ns,
	)
	if err != nil {
		return fmt.Errorf("create workspace: %w", err)
	}
	return nil
}

func (db *DB) GetWorkspaceBySlug(slug string) (*Workspace, error) {
	if slug == "" {
		return nil, nil
	}
	w := &Workspace{}
	err := db.QueryRow(
		`SELECT id, name, slug, k8s_namespace, COALESCE(channel_routing_strategy, 'shared'), created_at, updated_at
		 FROM workspaces WHERE slug = $1 AND deleted_at IS NULL`,
		slug,
	).Scan(&w.ID, &w.Name, &w.Slug, &w.K8sNamespace, &w.ChannelRoutingStrategy, &w.CreatedAt, &w.UpdatedAt)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("get workspace by slug: %w", err)
	}
	return w, nil
}

func (db *DB) GetWorkspace(id string) (*Workspace, error) {
	w := &Workspace{}
	err := db.QueryRow(
		`SELECT id, name, slug, k8s_namespace, COALESCE(channel_routing_strategy, 'shared'), created_at, updated_at
		 FROM workspaces WHERE id = $1 AND deleted_at IS NULL`,
		id,
	).Scan(&w.ID, &w.Name, &w.Slug, &w.K8sNamespace, &w.ChannelRoutingStrategy, &w.CreatedAt, &w.UpdatedAt)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("get workspace: %w", err)
	}
	return w, nil
}

func (db *DB) DeleteWorkspace(id string) error {
	_, err := db.Exec("DELETE FROM workspaces WHERE id = $1", id)
	if err != nil {
		return fmt.Errorf("delete workspace: %w", err)
	}
	return nil
}

// SoftDeleteWorkspace marks a workspace as deleted without removing the row.
// Returns sql.ErrNoRows if the workspace is missing or already soft-deleted.
func (db *DB) SoftDeleteWorkspace(id string) error {
	res, err := db.Exec(
		`UPDATE workspaces SET deleted_at = NOW(), updated_at = NOW() WHERE id = $1 AND deleted_at IS NULL`,
		id,
	)
	if err != nil {
		return fmt.Errorf("soft delete workspace: %w", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("soft delete workspace rows: %w", err)
	}
	if n == 0 {
		return sql.ErrNoRows
	}
	return nil
}

// SlugExists reports whether a slug is taken, including soft-deleted workspaces.
func (db *DB) SlugExists(slug string) (bool, error) {
	var exists bool
	err := db.QueryRow(
		`SELECT EXISTS(SELECT 1 FROM workspaces WHERE slug = $1)`,
		slug,
	).Scan(&exists)
	if err != nil {
		return false, fmt.Errorf("slug exists: %w", err)
	}
	return exists, nil
}

// DeleteWorkspaceDrafts removes all skill and soul drafts for a workspace.
func (db *DB) DeleteWorkspaceDrafts(workspaceID string) error {
	if _, err := db.Exec(`DELETE FROM skill_drafts WHERE workspace_id = $1`, workspaceID); err != nil {
		return fmt.Errorf("delete skill drafts: %w", err)
	}
	if _, err := db.Exec(`DELETE FROM soul_drafts WHERE workspace_id = $1`, workspaceID); err != nil {
		return fmt.Errorf("delete soul drafts: %w", err)
	}
	return nil
}

func (db *DB) UpdateWorkspaceName(id, name string) error {
	_, err := db.Exec("UPDATE workspaces SET name = $2, updated_at = NOW() WHERE id = $1 AND deleted_at IS NULL", id, name)
	if err != nil {
		return fmt.Errorf("update workspace name: %w", err)
	}
	return nil
}

func (db *DB) ListWorkspacesByUser(userID string) ([]*Workspace, error) {
	rows, err := db.Query(
		`SELECT w.id, w.name, w.slug, w.k8s_namespace, COALESCE(w.channel_routing_strategy, 'shared'), w.created_at, w.updated_at
		 FROM workspaces w
		 JOIN workspace_members wm ON w.id = wm.workspace_id
		 WHERE wm.user_id = $1 AND w.deleted_at IS NULL
		 ORDER BY w.created_at ASC`,
		userID,
	)
	if err != nil {
		return nil, fmt.Errorf("list workspaces by user: %w", err)
	}
	defer rows.Close()

	var workspaces []*Workspace
	for rows.Next() {
		w := &Workspace{}
		if err := rows.Scan(&w.ID, &w.Name, &w.Slug, &w.K8sNamespace, &w.ChannelRoutingStrategy, &w.CreatedAt, &w.UpdatedAt); err != nil {
			return nil, fmt.Errorf("scan workspace: %w", err)
		}
		workspaces = append(workspaces, w)
	}
	return workspaces, rows.Err()
}

func (db *DB) AddWorkspaceMember(workspaceID, userID, role string) error {
	_, err := db.Exec(
		`INSERT INTO workspace_members (workspace_id, user_id, role) VALUES ($1, $2, $3)`,
		workspaceID, userID, role,
	)
	if err != nil {
		return fmt.Errorf("add workspace member: %w", err)
	}
	return nil
}

func (db *DB) RemoveWorkspaceMember(workspaceID, userID string) error {
	_, err := db.Exec(
		"DELETE FROM workspace_members WHERE workspace_id = $1 AND user_id = $2",
		workspaceID, userID,
	)
	if err != nil {
		return fmt.Errorf("remove workspace member: %w", err)
	}
	return nil
}

func (db *DB) UpdateWorkspaceMemberRole(workspaceID, userID, role string) error {
	_, err := db.Exec(
		"UPDATE workspace_members SET role = $3, updated_at = NOW() WHERE workspace_id = $1 AND user_id = $2",
		workspaceID, userID, role,
	)
	if err != nil {
		return fmt.Errorf("update workspace member role: %w", err)
	}
	return nil
}

func (db *DB) GetWorkspaceMember(workspaceID, userID string) (*WorkspaceMember, error) {
	m := &WorkspaceMember{}
	err := db.QueryRow(
		`SELECT workspace_id, user_id, role, created_at FROM workspace_members WHERE workspace_id = $1 AND user_id = $2`,
		workspaceID, userID,
	).Scan(&m.WorkspaceID, &m.UserID, &m.Role, &m.CreatedAt)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("get workspace member: %w", err)
	}
	return m, nil
}

func (db *DB) ListWorkspaceMembers(workspaceID string) ([]*WorkspaceMember, error) {
	rows, err := db.Query(
		`SELECT workspace_id, user_id, role, created_at FROM workspace_members WHERE workspace_id = $1 ORDER BY created_at ASC`,
		workspaceID,
	)
	if err != nil {
		return nil, fmt.Errorf("list workspace members: %w", err)
	}
	defer rows.Close()

	var members []*WorkspaceMember
	for rows.Next() {
		m := &WorkspaceMember{}
		if err := rows.Scan(&m.WorkspaceID, &m.UserID, &m.Role, &m.CreatedAt); err != nil {
			return nil, fmt.Errorf("scan workspace member: %w", err)
		}
		members = append(members, m)
	}
	return members, rows.Err()
}

func (db *DB) IsWorkspaceMember(workspaceID, userID string) (bool, error) {
	var exists bool
	err := db.QueryRow(
		"SELECT EXISTS(SELECT 1 FROM workspace_members WHERE workspace_id = $1 AND user_id = $2)",
		workspaceID, userID,
	).Scan(&exists)
	if err != nil {
		return false, fmt.Errorf("check workspace membership: %w", err)
	}
	return exists, nil
}

func (db *DB) GetWorkspaceMemberRole(workspaceID, userID string) (string, error) {
	var role string
	err := db.QueryRow(
		"SELECT role FROM workspace_members WHERE workspace_id = $1 AND user_id = $2",
		workspaceID, userID,
	).Scan(&role)
	if err == sql.ErrNoRows {
		return "", nil
	}
	if err != nil {
		return "", fmt.Errorf("get workspace member role: %w", err)
	}
	return role, nil
}

func (db *DB) SetWorkspaceNamespace(id, namespace string) error {
	_, err := db.Exec(
		"UPDATE workspaces SET k8s_namespace = $2, updated_at = NOW() WHERE id = $1 AND deleted_at IS NULL",
		id, namespace,
	)
	if err != nil {
		return fmt.Errorf("set workspace namespace: %w", err)
	}
	return nil
}

func (db *DB) GetAllWorkspaceNamespaces() ([]string, error) {
	rows, err := db.Query(
		`SELECT DISTINCT k8s_namespace FROM workspaces WHERE k8s_namespace IS NOT NULL AND k8s_namespace != '' AND deleted_at IS NULL`,
	)
	if err != nil {
		return nil, fmt.Errorf("get all workspace namespaces: %w", err)
	}
	defer rows.Close()

	var namespaces []string
	for rows.Next() {
		var ns string
		if err := rows.Scan(&ns); err != nil {
			return nil, fmt.Errorf("scan workspace namespace: %w", err)
		}
		namespaces = append(namespaces, ns)
	}
	return namespaces, rows.Err()
}

func (db *DB) ListWorkspacesWithoutNamespace() ([]*Workspace, error) {
	rows, err := db.Query(
		`SELECT id, name, slug, k8s_namespace, COALESCE(channel_routing_strategy, 'shared'), created_at, updated_at
		 FROM workspaces
		 WHERE (k8s_namespace IS NULL OR k8s_namespace = '') AND deleted_at IS NULL`,
	)
	if err != nil {
		return nil, fmt.Errorf("list workspaces without namespace: %w", err)
	}
	defer rows.Close()

	var workspaces []*Workspace
	for rows.Next() {
		w := &Workspace{}
		if err := rows.Scan(&w.ID, &w.Name, &w.Slug, &w.K8sNamespace, &w.ChannelRoutingStrategy, &w.CreatedAt, &w.UpdatedAt); err != nil {
			return nil, fmt.Errorf("scan workspace: %w", err)
		}
		workspaces = append(workspaces, w)
	}
	return workspaces, rows.Err()
}

func (db *DB) ListAllWorkspaces() ([]*Workspace, error) {
	rows, err := db.Query(
		`SELECT id, name, slug, k8s_namespace, COALESCE(channel_routing_strategy, 'shared'), created_at, updated_at
		 FROM workspaces WHERE deleted_at IS NULL ORDER BY created_at ASC`,
	)
	if err != nil {
		return nil, fmt.Errorf("list all workspaces: %w", err)
	}
	defer rows.Close()

	var workspaces []*Workspace
	for rows.Next() {
		w := &Workspace{}
		if err := rows.Scan(&w.ID, &w.Name, &w.Slug, &w.K8sNamespace, &w.ChannelRoutingStrategy, &w.CreatedAt, &w.UpdatedAt); err != nil {
			return nil, fmt.Errorf("scan workspace: %w", err)
		}
		workspaces = append(workspaces, w)
	}
	return workspaces, rows.Err()
}

// AdminWorkspaceInfo holds enriched workspace data for the admin panel.
type AdminWorkspaceInfo struct {
	Workspace
	OwnerID      *string
	OwnerEmail   *string
	OwnerName    *string
	OwnerPicture *string
	SandboxCount int
}

func (db *DB) ListAllWorkspacesAdmin() ([]*AdminWorkspaceInfo, error) {
	rows, err := db.Query(
		`SELECT w.id, w.name, w.slug, w.k8s_namespace, COALESCE(w.channel_routing_strategy, 'shared'), w.created_at, w.updated_at,
		        u.id, u.email, u.name, u.picture,
		        (SELECT COUNT(*) FROM sandboxes s WHERE s.workspace_id = w.id)
		 FROM workspaces w
		 LEFT JOIN workspace_members wm ON w.id = wm.workspace_id AND wm.role = 'owner'
		 LEFT JOIN users u ON wm.user_id = u.id
		 WHERE w.deleted_at IS NULL
		 ORDER BY w.created_at ASC`,
	)
	if err != nil {
		return nil, fmt.Errorf("list all workspaces admin: %w", err)
	}
	defer rows.Close()

	var workspaces []*AdminWorkspaceInfo
	for rows.Next() {
		w := &AdminWorkspaceInfo{}
		if err := rows.Scan(
			&w.ID, &w.Name, &w.Slug, &w.K8sNamespace, &w.ChannelRoutingStrategy, &w.CreatedAt, &w.UpdatedAt,
			&w.OwnerID, &w.OwnerEmail, &w.OwnerName, &w.OwnerPicture,
			&w.SandboxCount,
		); err != nil {
			return nil, fmt.Errorf("scan admin workspace: %w", err)
		}
		workspaces = append(workspaces, w)
	}
	return workspaces, rows.Err()
}

func (db *DB) AddWorkspaceVolume(id, workspaceID, pvcName, mountPath string) error {
	_, err := db.Exec(
		`INSERT INTO workspace_volumes (id, workspace_id, pvc_name, mount_path) VALUES ($1, $2, $3, $4)`,
		id, workspaceID, pvcName, mountPath,
	)
	if err != nil {
		return fmt.Errorf("add workspace volume: %w", err)
	}
	return nil
}

// ValidRoutingStrategies enumerates the values accepted by
// UpdateWorkspaceRoutingStrategy and the routing-strategy HTTP handlers.
var ValidRoutingStrategies = map[string]bool{
	"shared":    true,
	"per_agent": true,
	"hybrid":    true,
}

// UpdateWorkspaceRoutingStrategy sets the channel_routing_strategy for
// a workspace. Returns an error if the strategy is not one of
// shared/per_agent/hybrid.
func (db *DB) UpdateWorkspaceRoutingStrategy(id, strategy string) error {
	if !ValidRoutingStrategies[strategy] {
		return fmt.Errorf("invalid routing strategy: %q", strategy)
	}
	_, err := db.Exec(
		`UPDATE workspaces SET channel_routing_strategy = $2, updated_at = NOW() WHERE id = $1 AND deleted_at IS NULL`,
		id, strategy,
	)
	if err != nil {
		return fmt.Errorf("update workspace routing strategy: %w", err)
	}
	return nil
}

func (db *DB) ListWorkspaceVolumes(workspaceID string) ([]WorkspaceVolume, error) {
	rows, err := db.Query(
		`SELECT id, workspace_id, pvc_name, mount_path, created_at FROM workspace_volumes WHERE workspace_id = $1 ORDER BY created_at ASC`,
		workspaceID,
	)
	if err != nil {
		return nil, fmt.Errorf("list workspace volumes: %w", err)
	}
	defer rows.Close()

	var volumes []WorkspaceVolume
	for rows.Next() {
		var v WorkspaceVolume
		if err := rows.Scan(&v.ID, &v.WorkspaceID, &v.PVCName, &v.MountPath, &v.CreatedAt); err != nil {
			return nil, fmt.Errorf("scan workspace volume: %w", err)
		}
		volumes = append(volumes, v)
	}
	return volumes, rows.Err()
}

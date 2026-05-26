// Integration test for provisionSandbox composition ordering.
// Requires TEST_DATABASE_URL with migrations applied.
package server

import (
	"context"
	"os"
	"sync"
	"testing"
	"time"

	"github.com/agentserver/agentserver/internal/db"
	"github.com/agentserver/agentserver/internal/process"
	"github.com/agentserver/agentserver/internal/sandbox"
	"github.com/agentserver/agentserver/internal/sbxstore"
	"github.com/agentserver/agentserver/internal/storage"
)

// blockProcessManager blocks StartContainerWithIP until Release is called.
type blockProcessManager struct {
	mu       sync.Mutex
	blocking bool
	release  chan struct{}
	started  chan struct{}
}

func newBlockProcessManager() *blockProcessManager {
	return &blockProcessManager{
		blocking: true,
		release:  make(chan struct{}),
		started:  make(chan struct{}, 1),
	}
}

func (b *blockProcessManager) Release() {
	select {
	case <-b.release:
	default:
		close(b.release)
	}
}

func (b *blockProcessManager) StartContainerWithIP(id string, _ process.StartOptions) (string, error) {
	b.started <- struct{}{}
	if b.blocking {
		<-b.release
	}
	return "10.0.0.1", nil
}

func (b *blockProcessManager) StartContainer(string, process.StartOptions) error {
	return nil
}

func (b *blockProcessManager) Start(string, string, []string, []string, process.StartOptions) (process.Process, error) {
	return nil, nil
}

func (b *blockProcessManager) Get(string) (process.Process, bool) { return nil, false }

func (b *blockProcessManager) Stop(string) error { return nil }

func (b *blockProcessManager) Pause(string) error { return nil }

func (b *blockProcessManager) Resume(string, string, string, []string) (process.Process, error) {
	return nil, nil
}

func (b *blockProcessManager) Close() error { return nil }

type noopDriveManager struct{}

func (noopDriveManager) EnsureDrive(context.Context, string, string) ([]process.VolumeMount, error) {
	return nil, nil
}

func TestProvisionSandbox_CompositionPersistedBeforeGoroutine(t *testing.T) {
	url := os.Getenv("TEST_DATABASE_URL")
	if url == "" {
		t.Skip("TEST_DATABASE_URL not set; skipping provision integration test")
	}
	d, err := db.Open(url)
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()

	wsID := "ws-tier1-prov-" + t.Name()
	if _, err := d.Exec(
		`INSERT INTO workspaces (id, name) VALUES ($1, $2) ON CONFLICT DO NOTHING`,
		wsID, "tier1 provision test",
	); err != nil {
		t.Fatalf("insert workspace: %v", err)
	}
	t.Cleanup(func() {
		_, _ = d.Exec(`DELETE FROM sandbox_compositions WHERE sandbox_id IN (SELECT id FROM sandboxes WHERE workspace_id = $1)`, wsID)
		_, _ = d.Exec(`DELETE FROM sandboxes WHERE workspace_id = $1`, wsID)
		_, _ = d.Exec(`DELETE FROM workspaces WHERE id = $1`, wsID)
	})

	pm := newBlockProcessManager()
	s := &Server{
		DB:             d,
		Sandboxes:      sbxstore.NewStore(d),
		ProcessManager: pm,
		DriveManager:   noopDriveManager{},
	}

	soulRef := "draft:00000000-0000-0000-0000-000000000099"
	skillRef := "draft:00000000-0000-0000-0000-000000000098"

	sbx, err := s.provisionSandbox(context.Background(), wsID, provisionInput{
		Name: "race-test",
		Type: sandbox.SandboxTypeOpenclaw.String(),
		Composition: &SandboxCompositionRequest{
			Soul:   soulRef,
			Skills: []string{skillRef},
		},
	})
	if err != nil {
		t.Fatalf("provisionSandbox: %v", err)
	}

	// Goroutine should be blocked inside StartContainerWithIP; composition
	// row must already exist (persisted before go func()).
	comp, err := d.GetSandboxComposition(sbx.ID)
	if err != nil {
		t.Fatalf("GetSandboxComposition: %v", err)
	}
	if comp == nil {
		t.Fatal("composition row missing before container start completed")
	}
	if !comp.SoulRef.Valid || comp.SoulRef.String != soulRef {
		t.Fatalf("soul_ref = %v, want %q", comp.SoulRef, soulRef)
	}
	if len(comp.SkillRefs) != 1 || comp.SkillRefs[0] != skillRef {
		t.Fatalf("skill_refs = %v, want [%q]", comp.SkillRefs, skillRef)
	}

	pm.Release()

	select {
	case <-pm.started:
	case <-time.After(5 * time.Second):
		t.Fatal("container start goroutine did not reach StartContainerWithIP")
	}
}

// Ensure noopDriveManager implements storage.DriveManager.
var _ storage.DriveManager = noopDriveManager{}

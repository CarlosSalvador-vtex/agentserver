package db

import "testing"

func TestGetWorkspaceBySlug(t *testing.T) {
	d := newTestDB(t)

	id := "w-slug-test-" + t.Name()
	if err := d.CreateWorkspace(id, "Empresa A"); err != nil {
		t.Fatal(err)
	}
	ws, err := d.GetWorkspace(id)
	if err != nil || ws == nil {
		t.Fatal("get workspace", err)
	}

	got, err := d.GetWorkspaceBySlug(ws.Slug)
	if err != nil {
		t.Fatalf("GetWorkspaceBySlug err: %v", err)
	}
	if got == nil || got.ID != id {
		t.Fatalf("unexpected result: %+v", got)
	}

	missing, err := d.GetWorkspaceBySlug("does-not-exist-xyz")
	if err != nil {
		t.Fatalf("missing slug should not error, got %v", err)
	}
	if missing != nil {
		t.Fatalf("missing slug should return nil, got %+v", missing)
	}
}

func TestCreateWorkspaceAssignsSlug(t *testing.T) {
	d := newTestDB(t)

	id := "w-create-slug-" + t.Name()
	slug, err := d.CreateWorkspaceWithSlug(id, "Empresa de Teste", "")
	if err != nil {
		t.Fatal(err)
	}
	if slug != "empresa-de-teste" {
		t.Fatalf("slug = %q, want empresa-de-teste", slug)
	}

	id2 := "w-collide-" + t.Name()
	slug2, err := d.CreateWorkspaceWithSlug(id2, "Empresa de Teste", "")
	if err != nil {
		t.Fatal(err)
	}
	if slug2 != "empresa-de-teste-2" {
		t.Fatalf("collision slug = %q, want empresa-de-teste-2", slug2)
	}
}

package cstesting

import (
	"errors"
	"testing"

	sdk "github.com/cloudscale-ch/cloudscale-go-sdk/v8"
)

func TestMockClient_CreateAndList(t *testing.T) {
	mock := NewMockClient()
	ctx := t.Context()

	mock.AddFlavor("flex-8-2", 8, 16)
	mock.AddServer("existing-1", "node-1", map[string]string{"group": "pool1"})

	if len(mock.Servers) != 1 {
		t.Fatalf("expected 1 server, got %d", len(mock.Servers))
	}

	servers := mock.ServersByTag("group", "pool1")
	if len(servers) != 1 {
		t.Fatalf("expected 1 server by tag, got %d", len(servers))
	}

	s := mock.ServerByUUID("existing-1")
	if s == nil || s.Name != "node-1" {
		t.Fatal("expected to find server by UUID with correct name")
	}

	if mock.ServerByUUID("nonexistent") != nil {
		t.Error("expected nil for nonexistent UUID")
	}

	tags := sdk.TagMap{"group": "pool1"}
	req := sdk.ServerRequest{Name: "new-server", Flavor: "flex-8-2", TaggedResourceRequest: sdk.TaggedResourceRequest{Tags: &tags}}
	created, err := mock.CreateServer(ctx, &req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if created.UUID != "uuid-0" {
		t.Errorf("expected uuid-0, got %s", created.UUID)
	}
	if mock.CreateCount != 1 || len(mock.Servers) != 2 {
		t.Error("create count or server list wrong")
	}
}

func TestMockClient_Delete(t *testing.T) {
	mock := NewMockClient()
	mock.AddServer("s1", "n1", nil)
	mock.AddServer("s2", "n2", nil)

	if err := mock.DeleteServer(t.Context(), "s1"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(mock.Servers) != 1 || mock.Servers[0].UUID != "s2" {
		t.Error("wrong server remaining after delete")
	}
}

func TestMockClient_DeleteNonexistent(t *testing.T) {
	mock := NewMockClient()
	if err := mock.DeleteServer(t.Context(), "nope"); err == nil {
		t.Fatal("expected error")
	}
}

func TestMockClient_CreateError(t *testing.T) {
	mock := NewMockClient()
	mock.CreateErr = errors.New("quota exceeded")
	req := sdk.ServerRequest{Name: "test", Flavor: "flex-4-1"}
	if _, err := mock.CreateServer(t.Context(), &req); err == nil {
		t.Fatal("expected error")
	}
	if mock.CreateCount != 0 {
		t.Error("should not increment count on error")
	}
}

func TestMockClient_DeleteError(t *testing.T) {
	mock := NewMockClient()
	mock.DeleteErr = errors.New("api error")
	mock.AddServer("s1", "n1", nil)
	if err := mock.DeleteServer(t.Context(), "s1"); err == nil {
		t.Fatal("expected error")
	}
	if len(mock.Servers) != 1 {
		t.Error("server should still exist after failed delete")
	}
}

func TestMockClient_FlavorBySlug(t *testing.T) {
	mock := NewMockClient()
	mock.AddFlavor("flex-8-2", 8, 16)

	f := mock.FlavorBySlug("flex-8-2")
	if f == nil {
		t.Fatal("expected to find flavor")
	}
	if f.VCPUCount != 8 {
		t.Errorf("expected 8 vCPUs, got %d", f.VCPUCount)
	}
	if f.MemoryGB != 16 {
		t.Errorf("expected 16 GB, got %d", f.MemoryGB)
	}
	if mock.FlavorBySlug("nonexistent") != nil {
		t.Error("expected nil for nonexistent flavor")
	}
}

func TestMockClient_UUIDAutoIncrement(t *testing.T) {
	mock := NewMockClient()
	ctx := t.Context()
	r := sdk.ServerRequest{Name: "x", Flavor: "f"}

	s1, _ := mock.CreateServer(ctx, &r)
	s2, _ := mock.CreateServer(ctx, &r)
	s3, _ := mock.CreateServer(ctx, &r)

	if s1.UUID != "uuid-0" || s2.UUID != "uuid-1" || s3.UUID != "uuid-2" {
		t.Errorf("expected auto-incrementing UUIDs, got %s, %s, %s", s1.UUID, s2.UUID, s3.UUID)
	}
}

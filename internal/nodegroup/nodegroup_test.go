package nodegroup

import (
	"errors"
	"strings"
	"testing"

	cstesting "github.com/kubeterm-sh/autoscaler-cloudscale/internal/cloudscale/testing"
	"github.com/kubeterm-sh/autoscaler-cloudscale/internal/config"
)

func newTestGroup(t *testing.T, minSize, maxSize int) (*NodeGroup, *cstesting.MockClient) {
	t.Helper()
	mock := cstesting.NewMockClient()
	mock.AddFlavor("flex-8-2", 8, 16)

	cfg := config.NodeGroup{
		Name:         "test-pool",
		MinSize:      minSize,
		MaxSize:      maxSize,
		Flavor:       "flex-8-2",
		Image:        "debian-12",
		Zone:         "rma1",
		VolumeSizeGB: 100,
		Tags:         map[string]string{"cluster": "test"},
		Labels:       map[string]string{"role": "worker"},
	}
	return New(&cfg, mock), mock
}

func TestNodeGroup_Properties(t *testing.T) {
	ng, _ := newTestGroup(t, 0, 10)
	if ng.Name() != "test-pool" {
		t.Errorf("got name %s", ng.Name())
	}
	if ng.MinSize() != 0 {
		t.Errorf("got minSize %d", ng.MinSize())
	}
	if ng.MaxSize() != 10 {
		t.Errorf("got maxSize %d", ng.MaxSize())
	}
	if ng.TargetSize() != 0 {
		t.Errorf("got targetSize %d", ng.TargetSize())
	}
}

func TestNodeGroup_Debug(t *testing.T) {
	ng, _ := newTestGroup(t, 1, 5)
	d := ng.Debug()
	for _, want := range []string{"test-pool", "flex-8-2", "rma1"} {
		if !strings.Contains(d, want) {
			t.Errorf("debug %q missing %q", d, want)
		}
	}
}

func TestNodeGroup_IncreaseSize(t *testing.T) {
	ng, mock := newTestGroup(t, 0, 5)
	if err := ng.IncreaseSize(t.Context(), 3); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if ng.TargetSize() != 3 {
		t.Errorf("targetSize=%d want 3", ng.TargetSize())
	}
	if mock.CreateCount != 3 {
		t.Errorf("creates=%d want 3", mock.CreateCount)
	}

	for _, s := range mock.Servers {
		if s.Tags["k8s-autoscaler-group"] != "test-pool" {
			t.Error("missing managed tag")
		}
		if s.Tags["cluster"] != "test" {
			t.Error("missing custom tag")
		}
	}
}

func TestNodeGroup_IncreaseSize_UniqueNames(t *testing.T) {
	ng, mock := newTestGroup(t, 0, 10)
	if err := ng.IncreaseSize(t.Context(), 5); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	names := make(map[string]bool)
	for _, s := range mock.Servers {
		if names[s.Name] {
			t.Errorf("duplicate name: %s", s.Name)
		}
		names[s.Name] = true
	}
}

func TestNodeGroup_IncreaseSize_ExceedsMax(t *testing.T) {
	ng, _ := newTestGroup(t, 0, 3)
	if err := ng.IncreaseSize(t.Context(), 5); err == nil {
		t.Fatal("expected error")
	}
	if ng.TargetSize() != 0 {
		t.Errorf("targetSize should be 0, got %d", ng.TargetSize())
	}
}

func TestNodeGroup_IncreaseSize_ZeroDelta(t *testing.T) {
	ng, _ := newTestGroup(t, 0, 5)
	if err := ng.IncreaseSize(t.Context(), 0); err == nil {
		t.Fatal("expected error")
	}
}

func TestNodeGroup_IncreaseSize_NegativeDelta(t *testing.T) {
	ng, _ := newTestGroup(t, 0, 5)
	if err := ng.IncreaseSize(t.Context(), -1); err == nil {
		t.Fatal("expected error")
	}
}

func TestNodeGroup_IncreaseSize_AllFail_RollsBack(t *testing.T) {
	ng, mock := newTestGroup(t, 0, 5)
	mock.CreateErr = errors.New("api error")
	if err := ng.IncreaseSize(t.Context(), 2); err == nil {
		t.Fatal("expected error")
	}
	if ng.TargetSize() != 0 {
		t.Errorf("targetSize should roll back to 0, got %d", ng.TargetSize())
	}
}

func TestNodeGroup_IncreaseSize_PartialFail_RollsBack(t *testing.T) {
	ng, mock := newTestGroup(t, 0, 10)
	// First increase succeeds
	if err := ng.IncreaseSize(t.Context(), 1); err != nil {
		t.Fatal(err)
	}
	if ng.TargetSize() != 1 {
		t.Fatalf("expected 1, got %d", ng.TargetSize())
	}

	// Now make creates fail
	mock.CreateErr = errors.New("boom")
	if err := ng.IncreaseSize(t.Context(), 2); err == nil {
		t.Fatal("expected error")
	}
	// Was 1, tried +2, both failed → rolled back to 1
	if ng.TargetSize() != 1 {
		t.Errorf("expected rollback to 1, got %d", ng.TargetSize())
	}
}

func TestNodeGroup_IncreaseSize_Sequential(t *testing.T) {
	ng, mock := newTestGroup(t, 0, 10)
	ctx := t.Context()
	if err := ng.IncreaseSize(ctx, 3); err != nil {
		t.Fatal(err)
	}
	if err := ng.IncreaseSize(ctx, 2); err != nil {
		t.Fatal(err)
	}
	if ng.TargetSize() != 5 {
		t.Errorf("got %d", ng.TargetSize())
	}
	if mock.CreateCount != 5 {
		t.Errorf("creates=%d", mock.CreateCount)
	}
}

func TestNodeGroup_IncreaseSize_AtMax(t *testing.T) {
	ng, _ := newTestGroup(t, 0, 3)
	if err := ng.IncreaseSize(t.Context(), 3); err != nil {
		t.Fatal(err)
	}
	if err := ng.IncreaseSize(t.Context(), 1); err == nil {
		t.Fatal("expected error at max")
	}
}

func TestNodeGroup_DeleteNodes(t *testing.T) {
	ng, mock := newTestGroup(t, 0, 5)
	mock.AddServer("s1", "n1", map[string]string{"k8s-autoscaler-group": "test-pool"})
	mock.AddServer("s2", "n2", map[string]string{"k8s-autoscaler-group": "test-pool"})
	mock.AddServer("s3", "n3", map[string]string{"k8s-autoscaler-group": "test-pool"})
	ng.SetTargetSize(3)

	if err := ng.DeleteNodes(t.Context(), []string{"s1", "s3"}); err != nil {
		t.Fatal(err)
	}
	if ng.TargetSize() != 1 {
		t.Errorf("targetSize=%d want 1", ng.TargetSize())
	}
	if mock.DeleteCount != 2 {
		t.Errorf("deletes=%d want 2", mock.DeleteCount)
	}
	if len(mock.Servers) != 1 || mock.Servers[0].UUID != "s2" {
		t.Error("wrong server remaining")
	}
}

func TestNodeGroup_DeleteNodes_Error(t *testing.T) {
	ng, mock := newTestGroup(t, 0, 5)
	mock.AddServer("s1", "n1", map[string]string{"k8s-autoscaler-group": "test-pool"})
	ng.SetTargetSize(2)
	mock.DeleteErr = errors.New("api error")
	if err := ng.DeleteNodes(t.Context(), []string{"s1"}); err == nil {
		t.Fatal("expected error")
	}
	if ng.TargetSize() != 2 {
		t.Errorf("targetSize should be unchanged, got %d", ng.TargetSize())
	}
}

func TestNodeGroup_DeleteNodes_FloorAtMinSize(t *testing.T) {
	ng, mock := newTestGroup(t, 2, 5)
	mock.AddServer("s1", "n1", map[string]string{"k8s-autoscaler-group": "test-pool"})
	mock.AddServer("s2", "n2", map[string]string{"k8s-autoscaler-group": "test-pool"})
	mock.AddServer("s3", "n3", map[string]string{"k8s-autoscaler-group": "test-pool"})
	ng.SetTargetSize(3)

	if err := ng.DeleteNodes(t.Context(), []string{"s1", "s2"}); err != nil {
		t.Fatal(err)
	}
	if ng.TargetSize() != 2 {
		t.Errorf("should clamp to minSize 2, got %d", ng.TargetSize())
	}
}

func TestNodeGroup_SyncTargetSize(t *testing.T) {
	ng, mock := newTestGroup(t, 0, 10)
	mock.AddServer("s1", "n1", map[string]string{"k8s-autoscaler-group": "test-pool"})
	mock.AddServer("s2", "n2", map[string]string{"k8s-autoscaler-group": "test-pool"})
	mock.AddServer("x", "other", map[string]string{"k8s-autoscaler-group": "other"})
	ng.SyncTargetSize()
	if ng.TargetSize() != 2 {
		t.Errorf("expected 2, got %d", ng.TargetSize())
	}
}

func TestNodeGroup_Servers(t *testing.T) {
	ng, mock := newTestGroup(t, 0, 10)
	mock.AddServer("s1", "n1", map[string]string{"k8s-autoscaler-group": "test-pool"})
	mock.AddServer("s2", "n2", map[string]string{"k8s-autoscaler-group": "test-pool"})
	mock.AddServer("s3", "n3", map[string]string{"k8s-autoscaler-group": "other"})
	if len(ng.Servers()) != 2 {
		t.Errorf("expected 2 servers")
	}
}

func TestNodeGroup_DecreaseTargetSize(t *testing.T) {
	ng, _ := newTestGroup(t, 0, 10)
	ng.SetTargetSize(5)
	if err := ng.DecreaseTargetSize(-2); err != nil {
		t.Fatal(err)
	}
	if ng.TargetSize() != 3 {
		t.Errorf("expected 3, got %d", ng.TargetSize())
	}
}

func TestNodeGroup_DecreaseTargetSize_PositiveDelta(t *testing.T) {
	ng, _ := newTestGroup(t, 0, 10)
	ng.SetTargetSize(5)
	if err := ng.DecreaseTargetSize(1); err == nil {
		t.Fatal("expected error")
	}
}

func TestNodeGroup_ProviderID(t *testing.T) {
	ng, _ := newTestGroup(t, 0, 5)
	if id := ng.ProviderID("abc"); id != "cloudscale://abc" {
		t.Errorf("got %s", id)
	}
}

func TestUUIDFromProviderID(t *testing.T) {
	uuid, err := UUIDFromProviderID("cloudscale://abc-123")
	if err != nil {
		t.Fatal(err)
	}
	if uuid != "abc-123" {
		t.Errorf("got %s", uuid)
	}
}

func TestUUIDFromProviderID_WrongPrefix(t *testing.T) {
	if _, err := UUIDFromProviderID("hcloud://abc"); err == nil {
		t.Fatal("expected error")
	}
}

func TestNodeGroup_NodeInfo(t *testing.T) {
	ng, _ := newTestGroup(t, 0, 5)
	info, err := ng.NodeInfo()
	if err != nil {
		t.Fatal(err)
	}
	if info.VCPUCount != 8 {
		t.Errorf("expected 8 vCPU, got %d", info.VCPUCount)
	}
	if info.MemoryGB != 16 {
		t.Errorf("expected 16 GB, got %d", info.MemoryGB)
	}
	if info.DiskSizeGB != 100 {
		t.Errorf("expected 100 GB disk (from config), got %d", info.DiskSizeGB)
	}
}

func TestNodeGroup_NodeInfo_UnknownFlavor(t *testing.T) {
	mock := cstesting.NewMockClient()
	cfg := config.NodeGroup{
		Name: "test", Flavor: "nonexistent", Image: "debian-12",
		Zone: "rma1", MaxSize: 1, VolumeSizeGB: 50,
	}
	ng := New(&cfg, mock)
	if _, err := ng.NodeInfo(); err == nil {
		t.Fatal("expected error")
	}
}

func TestNodeGroup_Labels_ReturnsCopy(t *testing.T) {
	ng, _ := newTestGroup(t, 0, 5)
	labels := ng.Labels()
	if labels["role"] != "worker" {
		t.Errorf("got %s", labels["role"])
	}
	labels["mutated"] = "true"
	if _, ok := ng.Labels()["mutated"]; ok {
		t.Error("Labels should return a copy")
	}
}

func TestAllocatableResource(t *testing.T) {
	// systemReserved: cpu=50m, memory=384Mi, ephemeral-storage=256Mi
	// evictionHard:   memory.available=100Mi, nodefs.available=10%
	cpu, mem, eph := AllocatableResource(8, 16, 100)

	if m := cpu.MilliValue(); m != 7950 {
		t.Errorf("cpu=%dm want 7950m", m)
	}
	wantMem := int64(16)*1024*1024*1024 - (384+100)*1024*1024
	if v := mem.Value(); v != wantMem {
		t.Errorf("mem=%d want %d", v, wantMem)
	}
	capacity := int64(100) * 1024 * 1024 * 1024
	wantEph := capacity - 256*1024*1024 - capacity/10
	if v := eph.Value(); v != wantEph {
		t.Errorf("eph=%d want %d", v, wantEph)
	}
}

func TestAllocatableResource_Clamped(t *testing.T) {
	cpu, mem, eph := AllocatableResource(0, 0, 0)

	if m := cpu.MilliValue(); m != 0 {
		t.Errorf("cpu=%dm want 0", m)
	}
	if v := mem.Value(); v != 0 {
		t.Errorf("mem=%d want 0", v)
	}
	if v := eph.Value(); v != 0 {
		t.Errorf("eph=%d want 0", v)
	}
}

func TestRandomSuffix_Unique(t *testing.T) {
	seen := make(map[string]bool)
	for range 100 {
		s, err := randomSuffix()
		if err != nil {
			t.Fatal(err)
		}
		if len(s) != 8 {
			t.Errorf("len=%d want 8", len(s))
		}
		if seen[s] {
			t.Errorf("duplicate suffix: %s", s)
		}
		seen[s] = true
	}
}

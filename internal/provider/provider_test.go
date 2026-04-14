package provider

import (
	"testing"

	cstesting "github.com/kubeterm-sh/autoscaler-cloudscale/internal/cloudscale/testing"
	"github.com/kubeterm-sh/autoscaler-cloudscale/internal/config"
	pb "github.com/kubeterm-sh/autoscaler-cloudscale/proto"
)

func newTestProvider(t *testing.T) (*Provider, *cstesting.MockClient) {
	t.Helper()
	mock := cstesting.NewMockClient()
	mock.AddFlavor("flex-8-2", 8, 16)
	mock.AddFlavor("flex-4-1", 4, 8)

	cfg := &config.Config{
		NodeGroups: []config.NodeGroup{
			{
				Name:         "pool1",
				MinSize:      0,
				MaxSize:      5,
				Flavor:       "flex-8-2",
				Image:        "debian-12",
				Zone:         "rma1",
				VolumeSizeGB: 100,
				Tags:         map[string]string{"cluster": "test"},
				Labels:       map[string]string{"role": "worker"},
			},
			{
				Name:         "pool2",
				MinSize:      1,
				MaxSize:      3,
				Flavor:       "flex-4-1",
				Image:        "debian-12",
				Zone:         "lpg1",
				VolumeSizeGB: 50,
			},
		},
	}

	p, err := New(cfg, mock)
	if err != nil {
		t.Fatal(err)
	}
	return p, mock
}

func TestProvider_NodeGroups(t *testing.T) {
	p, _ := newTestProvider(t)
	resp, err := p.NodeGroups(t.Context(), &pb.NodeGroupsRequest{})
	if err != nil {
		t.Fatal(err)
	}

	if len(resp.NodeGroups) != 2 {
		t.Fatalf("expected 2 groups, got %d", len(resp.NodeGroups))
	}

	// Build map for order-independent checks
	groups := make(map[string]*pb.NodeGroup)
	for _, g := range resp.NodeGroups {
		groups[g.Id] = g
	}

	g1 := groups["pool1"]
	if g1 == nil {
		t.Fatal("pool1 not found")
	}
	if g1.MinSize != 0 || g1.MaxSize != 5 {
		t.Errorf("pool1: min=%d max=%d", g1.MinSize, g1.MaxSize)
	}

	g2 := groups["pool2"]
	if g2 == nil {
		t.Fatal("pool2 not found")
	}
	if g2.MinSize != 1 || g2.MaxSize != 3 {
		t.Errorf("pool2: min=%d max=%d", g2.MinSize, g2.MaxSize)
	}
}

func TestProvider_NodeGroupForNode_Found(t *testing.T) {
	p, mock := newTestProvider(t)
	mock.AddServer("uuid-abc", "node-1", map[string]string{
		"k8s-autoscaler-group": "pool1",
		"cluster":              "test",
	})

	resp, err := p.NodeGroupForNode(t.Context(), &pb.NodeGroupForNodeRequest{
		Node: &pb.ExternalGrpcNode{ProviderID: "cloudscale://uuid-abc"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if resp.NodeGroup == nil {
		t.Fatal("expected node group, got nil")
	}
	if resp.NodeGroup.Id != "pool1" {
		t.Errorf("expected pool1, got %s", resp.NodeGroup.Id)
	}
}

func TestProvider_NodeGroupForNode_EmptyProviderID(t *testing.T) {
	p, _ := newTestProvider(t)
	resp, err := p.NodeGroupForNode(t.Context(), &pb.NodeGroupForNodeRequest{
		Node: &pb.ExternalGrpcNode{ProviderID: ""},
	})
	if err != nil {
		t.Fatal(err)
	}
	if resp.NodeGroup != nil {
		t.Error("expected nil node group for empty provider ID")
	}
}

func TestProvider_NodeGroupForNode_WrongPrefix(t *testing.T) {
	p, _ := newTestProvider(t)
	resp, err := p.NodeGroupForNode(t.Context(), &pb.NodeGroupForNodeRequest{
		Node: &pb.ExternalGrpcNode{ProviderID: "hcloud://some-id"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if resp.NodeGroup != nil {
		t.Error("expected nil for wrong provider prefix")
	}
}

func TestProvider_NodeGroupForNode_UnknownServer(t *testing.T) {
	p, _ := newTestProvider(t)
	resp, err := p.NodeGroupForNode(t.Context(), &pb.NodeGroupForNodeRequest{
		Node: &pb.ExternalGrpcNode{ProviderID: "cloudscale://nonexistent"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if resp.NodeGroup != nil {
		t.Error("expected nil for unknown server")
	}
}

func TestProvider_NodeGroupForNode_UnmanagedServer(t *testing.T) {
	p, mock := newTestProvider(t)
	// Server exists but has no k8s-autoscaler-group tag
	mock.AddServer("uuid-xyz", "unmanaged", map[string]string{"other": "tag"})

	resp, err := p.NodeGroupForNode(t.Context(), &pb.NodeGroupForNodeRequest{
		Node: &pb.ExternalGrpcNode{ProviderID: "cloudscale://uuid-xyz"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if resp.NodeGroup != nil {
		t.Error("expected nil for unmanaged server")
	}
}

func TestProvider_NodeGroupForNode_UnknownGroup(t *testing.T) {
	p, mock := newTestProvider(t)
	// Server tagged with a group that doesn't exist in config
	mock.AddServer("uuid-xyz", "orphan", map[string]string{
		"k8s-autoscaler-group": "nonexistent-pool",
	})

	resp, err := p.NodeGroupForNode(t.Context(), &pb.NodeGroupForNodeRequest{
		Node: &pb.ExternalGrpcNode{ProviderID: "cloudscale://uuid-xyz"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if resp.NodeGroup != nil {
		t.Error("expected nil for unknown group tag")
	}
}

func TestProvider_NodeGroupNodes_Empty(t *testing.T) {
	p, _ := newTestProvider(t)
	resp, err := p.NodeGroupNodes(t.Context(), &pb.NodeGroupNodesRequest{Id: "pool1"})
	if err != nil {
		t.Fatal(err)
	}
	if len(resp.Instances) != 0 {
		t.Errorf("expected 0 instances, got %d", len(resp.Instances))
	}
}

func TestProvider_NodeGroupNodes_WithServers(t *testing.T) {
	p, mock := newTestProvider(t)
	mock.AddServer("s1", "n1", map[string]string{"k8s-autoscaler-group": "pool1"})
	mock.AddServerWithStatus("s2", "n2", "changing", map[string]string{"k8s-autoscaler-group": "pool1"})
	mock.AddServer("s3", "n3", map[string]string{"k8s-autoscaler-group": "pool2"}) // different group

	resp, err := p.NodeGroupNodes(t.Context(), &pb.NodeGroupNodesRequest{Id: "pool1"})
	if err != nil {
		t.Fatal(err)
	}
	if len(resp.Instances) != 2 {
		t.Fatalf("expected 2 instances, got %d", len(resp.Instances))
	}

	// Check IDs use provider ID format
	for _, inst := range resp.Instances {
		if inst.Id == "" {
			t.Error("instance ID should not be empty")
		}
		if inst.Status == nil {
			t.Error("instance status should not be nil")
		}
	}

	// Find the "changing" one and verify status mapping
	for _, inst := range resp.Instances {
		if inst.Id == "cloudscale://s2" {
			if inst.Status.InstanceState != pb.InstanceStatus_instanceCreating {
				t.Errorf("changing server should map to instanceCreating, got %v",
					inst.Status.InstanceState)
			}
		}
		if inst.Id == "cloudscale://s1" {
			if inst.Status.InstanceState != pb.InstanceStatus_instanceRunning {
				t.Errorf("running server should map to instanceRunning, got %v",
					inst.Status.InstanceState)
			}
		}
	}
}

func TestProvider_NodeGroupNodes_NotFound(t *testing.T) {
	p, _ := newTestProvider(t)
	_, err := p.NodeGroupNodes(t.Context(), &pb.NodeGroupNodesRequest{Id: "nope"})
	if err == nil {
		t.Fatal("expected error for unknown group")
	}
}

func TestProvider_NodeGroupTargetSize(t *testing.T) {
	p, _ := newTestProvider(t)
	resp, err := p.NodeGroupTargetSize(t.Context(), &pb.NodeGroupTargetSizeRequest{Id: "pool1"})
	if err != nil {
		t.Fatal(err)
	}
	if resp.TargetSize != 0 {
		t.Errorf("expected 0, got %d", resp.TargetSize)
	}
}

func TestProvider_IncreaseAndDeleteFlow(t *testing.T) {
	p, mock := newTestProvider(t)
	ctx := t.Context()

	// Increase pool1 by 2
	_, err := p.NodeGroupIncreaseSize(ctx, &pb.NodeGroupIncreaseSizeRequest{
		Id: "pool1", Delta: 2,
	})
	if err != nil {
		t.Fatal(err)
	}

	// Check target size
	sizeResp, _ := p.NodeGroupTargetSize(ctx, &pb.NodeGroupTargetSizeRequest{Id: "pool1"})
	if sizeResp.TargetSize != 2 {
		t.Errorf("expected target 2, got %d", sizeResp.TargetSize)
	}

	// Check nodes exist
	nodesResp, _ := p.NodeGroupNodes(ctx, &pb.NodeGroupNodesRequest{Id: "pool1"})
	if len(nodesResp.Instances) != 2 {
		t.Fatalf("expected 2 instances, got %d", len(nodesResp.Instances))
	}

	// Delete one node
	_, err = p.NodeGroupDeleteNodes(ctx, &pb.NodeGroupDeleteNodesRequest{
		Id: "pool1",
		Nodes: []*pb.ExternalGrpcNode{
			{ProviderID: nodesResp.Instances[0].Id},
		},
	})
	if err != nil {
		t.Fatal(err)
	}

	// Verify
	sizeResp, _ = p.NodeGroupTargetSize(ctx, &pb.NodeGroupTargetSizeRequest{Id: "pool1"})
	if sizeResp.TargetSize != 1 {
		t.Errorf("expected target 1 after delete, got %d", sizeResp.TargetSize)
	}
	if mock.DeleteCount != 1 {
		t.Errorf("expected 1 delete, got %d", mock.DeleteCount)
	}
}

func TestProvider_IncreaseSize_NotFound(t *testing.T) {
	p, _ := newTestProvider(t)
	_, err := p.NodeGroupIncreaseSize(t.Context(), &pb.NodeGroupIncreaseSizeRequest{
		Id: "nope", Delta: 1,
	})
	if err == nil {
		t.Fatal("expected error")
	}
}

func TestProvider_DeleteNodes_InvalidProviderID(t *testing.T) {
	p, _ := newTestProvider(t)
	_, err := p.NodeGroupDeleteNodes(t.Context(), &pb.NodeGroupDeleteNodesRequest{
		Id: "pool1",
		Nodes: []*pb.ExternalGrpcNode{
			{ProviderID: "hcloud://wrong-prefix"},
		},
	})
	if err == nil {
		t.Fatal("expected error for invalid provider ID prefix")
	}
}

func TestProvider_DecreaseTargetSize(t *testing.T) {
	p, mock := newTestProvider(t)
	ctx := t.Context()

	// Increase by 3 (creates 3 servers, target=3)
	if _, err := p.NodeGroupIncreaseSize(ctx, &pb.NodeGroupIncreaseSizeRequest{Id: "pool1", Delta: 3}); err != nil {
		t.Fatal(err)
	}

	// Delete one server so actual=2 but target still=3
	// (simulates autoscaler deleting a node, then wanting to decrease target)
	nodesResp, err := p.NodeGroupNodes(ctx, &pb.NodeGroupNodesRequest{Id: "pool1"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := p.NodeGroupDeleteNodes(ctx, &pb.NodeGroupDeleteNodesRequest{
		Id:    "pool1",
		Nodes: []*pb.ExternalGrpcNode{{ProviderID: nodesResp.Instances[0].Id}},
	}); err != nil {
		t.Fatal(err)
	}

	// Now target=2 (decremented by delete), actual=2. Manually bump target to 3 to test decrease.
	// Actually, after delete target=2 and servers=2. Let's just test that we can't go below server count.
	_ = mock

	// Decrease by 1 would go from target=2 to target=1, but 2 servers exist — should fail
	_, err = p.NodeGroupDecreaseTargetSize(ctx, &pb.NodeGroupDecreaseTargetSizeRequest{
		Id: "pool1", Delta: -1,
	})
	if err == nil {
		t.Fatal("expected error: cannot decrease below actual server count")
	}

	// Verify target unchanged at 2
	sizeResp, _ := p.NodeGroupTargetSize(ctx, &pb.NodeGroupTargetSizeRequest{Id: "pool1"})
	if sizeResp.TargetSize != 2 {
		t.Errorf("expected 2, got %d", sizeResp.TargetSize)
	}
}

func TestProvider_Refresh(t *testing.T) {
	p, mock := newTestProvider(t)
	ctx := t.Context()

	// Add servers behind the provider's back (simulates reality)
	mock.AddServer("ext-1", "n1", map[string]string{"k8s-autoscaler-group": "pool1"})
	mock.AddServer("ext-2", "n2", map[string]string{"k8s-autoscaler-group": "pool1"})

	// Before refresh, target is still 0
	sizeResp, _ := p.NodeGroupTargetSize(ctx, &pb.NodeGroupTargetSizeRequest{Id: "pool1"})
	if sizeResp.TargetSize != 0 {
		t.Errorf("expected 0 before refresh, got %d", sizeResp.TargetSize)
	}

	// Refresh
	_, err := p.Refresh(ctx, &pb.RefreshRequest{})
	if err != nil {
		t.Fatal(err)
	}

	// After refresh, target should match actual servers
	sizeResp, _ = p.NodeGroupTargetSize(ctx, &pb.NodeGroupTargetSizeRequest{Id: "pool1"})
	if sizeResp.TargetSize != 2 {
		t.Errorf("expected 2 after refresh, got %d", sizeResp.TargetSize)
	}
}

func TestProvider_TemplateNodeInfo(t *testing.T) {
	p, _ := newTestProvider(t)
	resp, err := p.NodeGroupTemplateNodeInfo(t.Context(), &pb.NodeGroupTemplateNodeInfoRequest{Id: "pool1"})
	if err != nil {
		t.Fatal(err)
	}
	if len(resp.NodeBytes) == 0 {
		t.Error("expected non-empty nodeBytes")
	}
	// We can't easily deserialize the protobuf Node here without
	// importing k8s.io/api, but we verify it doesn't error.
}

func TestProvider_TemplateNodeInfo_NotFound(t *testing.T) {
	p, _ := newTestProvider(t)
	_, err := p.NodeGroupTemplateNodeInfo(t.Context(), &pb.NodeGroupTemplateNodeInfoRequest{Id: "nope"})
	if err == nil {
		t.Fatal("expected error")
	}
}

func TestProvider_OptionalRPCs_ReturnUnimplemented(t *testing.T) {
	p, _ := newTestProvider(t)
	ctx := t.Context()

	if _, err := p.PricingNodePrice(ctx, &pb.PricingNodePriceRequest{}); err == nil {
		t.Error("PricingNodePrice should return Unimplemented")
	}
	if _, err := p.PricingPodPrice(ctx, &pb.PricingPodPriceRequest{}); err == nil {
		t.Error("PricingPodPrice should return Unimplemented")
	}
	if resp, err := p.GPULabel(ctx, &pb.GPULabelRequest{}); err != nil {
		t.Errorf("GPULabel should succeed, got %v", err)
	} else if resp.Label == "" {
		t.Error("GPULabel should return a non-empty label")
	}
	if _, err := p.GetAvailableGPUTypes(ctx, &pb.GetAvailableGPUTypesRequest{}); err != nil {
		t.Errorf("GetAvailableGPUTypes should succeed, got %v", err)
	}
	if _, err := p.NodeGroupGetOptions(ctx, &pb.NodeGroupAutoscalingOptionsRequest{}); err == nil {
		t.Error("NodeGroupGetOptions should return Unimplemented")
	}
}

func TestProvider_Cleanup(t *testing.T) {
	p, _ := newTestProvider(t)
	_, err := p.Cleanup(t.Context(), &pb.CleanupRequest{})
	if err != nil {
		t.Errorf("Cleanup should succeed, got %v", err)
	}
}

func TestProvider_MapServerStatus(t *testing.T) {
	tests := []struct {
		status string
		want   pb.InstanceStatus_InstanceState
	}{
		{"running", pb.InstanceStatus_instanceRunning},
		{"stopped", pb.InstanceStatus_instanceDeleting},
		{"changing", pb.InstanceStatus_instanceCreating},
		{"unknown", pb.InstanceStatus_instanceCreating},
		{"", pb.InstanceStatus_instanceCreating},
	}

	for _, tt := range tests {
		got := mapServerStatus(tt.status)
		if got.InstanceState != tt.want {
			t.Errorf("mapServerStatus(%q)=%v, want %v", tt.status, got.InstanceState, tt.want)
		}
	}
}

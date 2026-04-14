// Package provider implements the externalgrpc CloudProvider gRPC service for cloudscale.ch.
package provider

import (
	"context"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/klog/v2"

	"github.com/kubeterm-sh/autoscaler-cloudscale/internal/cloudscale"
	"github.com/kubeterm-sh/autoscaler-cloudscale/internal/config"
	"github.com/kubeterm-sh/autoscaler-cloudscale/internal/metrics"
	"github.com/kubeterm-sh/autoscaler-cloudscale/internal/nodegroup"

	pb "github.com/kubeterm-sh/autoscaler-cloudscale/proto"
)

type Provider struct {
	pb.UnimplementedCloudProviderServer

	client     cloudscale.Client
	nodeGroups map[string]*nodegroup.NodeGroup
	cfg        *config.Config
}

func New(cfg *config.Config, client cloudscale.Client) (*Provider, error) {
	ngs := make(map[string]*nodegroup.NodeGroup, len(cfg.NodeGroups))

	for i := range cfg.NodeGroups {
		ngs[cfg.NodeGroups[i].Name] = nodegroup.New(&cfg.NodeGroups[i], client)
	}

	for i := range cfg.NodeGroups {
		metrics.NodeGroupMinSize.WithLabelValues(cfg.NodeGroups[i].Name).Set(float64(cfg.NodeGroups[i].MinSize))
		metrics.NodeGroupMaxSize.WithLabelValues(cfg.NodeGroups[i].Name).Set(float64(cfg.NodeGroups[i].MaxSize))
	}

	return &Provider{client: client, nodeGroups: ngs, cfg: cfg}, nil
}

func (p *Provider) NodeGroups(ctx context.Context, req *pb.NodeGroupsRequest) (*pb.NodeGroupsResponse, error) {
	klog.V(5).InfoS("NodeGroups called")

	groups := make([]*pb.NodeGroup, 0, len(p.nodeGroups))

	for _, ng := range p.nodeGroups {
		groups = append(groups, pbNodeGroup(ng))
	}

	return &pb.NodeGroupsResponse{NodeGroups: groups}, nil
}

func (p *Provider) NodeGroupForNode(ctx context.Context, req *pb.NodeGroupForNodeRequest) (*pb.NodeGroupForNodeResponse, error) {
	providerID := req.GetNode().GetProviderID()
	klog.V(5).InfoS("NodeGroupForNode called", "providerID", providerID)

	if providerID == "" {
		return &pb.NodeGroupForNodeResponse{}, nil
	}

	uuid, err := nodegroup.UUIDFromProviderID(providerID)
	if err != nil {
		return &pb.NodeGroupForNodeResponse{}, nil //nolint:nilerr // unknown prefix means not our node
	}

	server := p.client.ServerByUUID(uuid)
	if server == nil {
		klog.V(3).InfoS("server not found in cache", "uuid", uuid)
		return &pb.NodeGroupForNodeResponse{}, nil
	}

	groupName, ok := server.Tags["k8s-autoscaler-group"]
	if !ok {
		return &pb.NodeGroupForNodeResponse{}, nil
	}

	ng, ok := p.nodeGroups[groupName]
	if !ok {
		klog.V(3).InfoS("server tagged with unknown group", "uuid", uuid, "group", groupName)
		return &pb.NodeGroupForNodeResponse{}, nil
	}

	return &pb.NodeGroupForNodeResponse{NodeGroup: pbNodeGroup(ng)}, nil
}

func (p *Provider) NodeGroupNodes(ctx context.Context, req *pb.NodeGroupNodesRequest) (*pb.NodeGroupNodesResponse, error) {
	ngID := req.GetId()
	klog.V(5).InfoS("NodeGroupNodes called", "group", ngID)

	ng, ok := p.nodeGroups[ngID]
	if !ok {
		return nil, status.Errorf(codes.NotFound, "node group %q not found", ngID)
	}

	servers := ng.Servers()
	instances := make([]*pb.Instance, 0, len(servers))

	for i := range servers {
		instances = append(instances, &pb.Instance{
			Id:     ng.ProviderID(servers[i].UUID),
			Status: mapServerStatus(servers[i].Status),
		})
	}

	return &pb.NodeGroupNodesResponse{Instances: instances}, nil
}

func (p *Provider) NodeGroupTargetSize(ctx context.Context, req *pb.NodeGroupTargetSizeRequest) (*pb.NodeGroupTargetSizeResponse, error) {
	ng, ok := p.nodeGroups[req.GetId()]
	if !ok {
		return nil, status.Errorf(codes.NotFound, "node group %q not found", req.GetId())
	}
	return &pb.NodeGroupTargetSizeResponse{TargetSize: int32(ng.TargetSize())}, nil
}

func (p *Provider) NodeGroupIncreaseSize(ctx context.Context, req *pb.NodeGroupIncreaseSizeRequest) (*pb.NodeGroupIncreaseSizeResponse, error) {
	ngID := req.GetId()
	delta := int(req.GetDelta())
	klog.InfoS("NodeGroupIncreaseSize called", "group", ngID, "delta", delta)

	ng, ok := p.nodeGroups[ngID]
	if !ok {
		return nil, status.Errorf(codes.NotFound, "node group %q not found", ngID)
	}
	if err := ng.IncreaseSize(ctx, delta); err != nil {
		return nil, status.Errorf(codes.Internal, "increasing size: %v", err)
	}
	return &pb.NodeGroupIncreaseSizeResponse{}, nil
}

func (p *Provider) NodeGroupDeleteNodes(ctx context.Context, req *pb.NodeGroupDeleteNodesRequest) (*pb.NodeGroupDeleteNodesResponse, error) {
	ngID := req.GetId()
	klog.InfoS("NodeGroupDeleteNodes called", "group", ngID, "nodes", len(req.GetNodes()))

	ng, ok := p.nodeGroups[ngID]
	if !ok {
		return nil, status.Errorf(codes.NotFound, "node group %q not found", ngID)
	}

	uuids := make([]string, 0, len(req.GetNodes()))
	for _, node := range req.GetNodes() {
		uuid, err := nodegroup.UUIDFromProviderID(node.GetProviderID())
		if err != nil {
			return nil, status.Errorf(codes.InvalidArgument, "invalid provider ID: %v", err)
		}
		uuids = append(uuids, uuid)
	}

	if err := ng.DeleteNodes(ctx, uuids); err != nil {
		return nil, status.Errorf(codes.Internal, "deleting nodes: %v", err)
	}
	return &pb.NodeGroupDeleteNodesResponse{}, nil
}

func (p *Provider) NodeGroupDecreaseTargetSize(ctx context.Context, req *pb.NodeGroupDecreaseTargetSizeRequest) (*pb.NodeGroupDecreaseTargetSizeResponse, error) {
	ng, ok := p.nodeGroups[req.GetId()]
	if !ok {
		return nil, status.Errorf(codes.NotFound, "node group %q not found", req.GetId())
	}
	if err := ng.DecreaseTargetSize(int(req.GetDelta())); err != nil {
		return nil, status.Errorf(codes.Internal, "decreasing target size: %v", err)
	}
	return &pb.NodeGroupDecreaseTargetSizeResponse{}, nil
}

func (p *Provider) NodeGroupTemplateNodeInfo(ctx context.Context, req *pb.NodeGroupTemplateNodeInfoRequest) (*pb.NodeGroupTemplateNodeInfoResponse, error) {
	ngID := req.GetId()
	ng, ok := p.nodeGroups[ngID]
	if !ok {
		return nil, status.Errorf(codes.NotFound, "node group %q not found", ngID)
	}

	info, err := ng.NodeInfo()
	if err != nil {
		return nil, status.Errorf(codes.Internal, "getting node info: %v", err)
	}

	cpu, memory, ephemeral := nodegroup.AllocatableResource(info.VCPUCount, info.MemoryGB, info.DiskSizeGB)

	node := &corev1.Node{
		ObjectMeta: metav1.ObjectMeta{
			Name:   "template-" + ngID,
			Labels: ng.Labels(),
		},
		Spec: corev1.NodeSpec{
			Taints: convertTaints(ng.Taints()),
		},
		Status: corev1.NodeStatus{
			Capacity: corev1.ResourceList{
				corev1.ResourceCPU:              *resource.NewQuantity(int64(info.VCPUCount), resource.DecimalSI),
				corev1.ResourceMemory:           *resource.NewQuantity(int64(info.MemoryGB)*1024*1024*1024, resource.BinarySI),
				corev1.ResourceEphemeralStorage: *resource.NewQuantity(int64(info.DiskSizeGB)*1024*1024*1024, resource.BinarySI),
				corev1.ResourcePods:             *resource.NewQuantity(110, resource.DecimalSI),
			},
			Allocatable: corev1.ResourceList{
				corev1.ResourceCPU:              cpu,
				corev1.ResourceMemory:           memory,
				corev1.ResourceEphemeralStorage: ephemeral,
				corev1.ResourcePods:             *resource.NewQuantity(110, resource.DecimalSI),
			},
			Conditions: []corev1.NodeCondition{
				{Type: corev1.NodeReady, Status: corev1.ConditionTrue},
			},
		},
	}

	nodeBytes, err := node.Marshal()
	if err != nil {
		return nil, status.Errorf(codes.Internal, "marshaling template node: %v", err)
	}

	return &pb.NodeGroupTemplateNodeInfoResponse{NodeBytes: nodeBytes}, nil
}

func (p *Provider) Refresh(ctx context.Context, req *pb.RefreshRequest) (*pb.RefreshResponse, error) {
	klog.V(5).InfoS("Refresh called")
	if err := p.client.Refresh(ctx); err != nil {
		return nil, status.Errorf(codes.Internal, "refreshing cache: %v", err)
	}
	for _, ng := range p.nodeGroups {
		ng.SyncTargetSize()
	}
	return &pb.RefreshResponse{}, nil
}

func (p *Provider) PricingNodePrice(ctx context.Context, req *pb.PricingNodePriceRequest) (*pb.PricingNodePriceResponse, error) {
	return nil, status.Errorf(codes.Unimplemented, "not implemented")
}

func (p *Provider) PricingPodPrice(ctx context.Context, req *pb.PricingPodPriceRequest) (*pb.PricingPodPriceResponse, error) {
	return nil, status.Errorf(codes.Unimplemented, "not implemented")
}

func (p *Provider) GPULabel(ctx context.Context, req *pb.GPULabelRequest) (*pb.GPULabelResponse, error) {
	return &pb.GPULabelResponse{Label: "cloudscale.ch/gpu-node"}, nil
}

func (p *Provider) GetAvailableGPUTypes(ctx context.Context, req *pb.GetAvailableGPUTypesRequest) (*pb.GetAvailableGPUTypesResponse, error) {
	return &pb.GetAvailableGPUTypesResponse{}, nil
}

func (p *Provider) NodeGroupGetOptions(ctx context.Context, req *pb.NodeGroupAutoscalingOptionsRequest) (*pb.NodeGroupAutoscalingOptionsResponse, error) {
	return nil, status.Errorf(codes.Unimplemented, "not implemented")
}

func (p *Provider) Cleanup(ctx context.Context, req *pb.CleanupRequest) (*pb.CleanupResponse, error) {
	return &pb.CleanupResponse{}, nil
}

func pbNodeGroup(ng *nodegroup.NodeGroup) *pb.NodeGroup {
	return &pb.NodeGroup{
		Id:      ng.Name(),
		MinSize: int32(ng.MinSize()),
		MaxSize: int32(ng.MaxSize()),
		Debug:   ng.Debug(),
	}
}

func mapServerStatus(serverStatus string) *pb.InstanceStatus {
	var state pb.InstanceStatus_InstanceState
	switch serverStatus {
	case "running":
		state = pb.InstanceStatus_instanceRunning
	case "stopped":
		state = pb.InstanceStatus_instanceDeleting
	case "changing":
		state = pb.InstanceStatus_instanceCreating
	default:
		state = pb.InstanceStatus_instanceCreating
	}
	return &pb.InstanceStatus{InstanceState: state}
}

func convertTaints(taints []config.Taint) []corev1.Taint {
	if len(taints) == 0 {
		return nil
	}
	result := make([]corev1.Taint, len(taints))
	for i, t := range taints {
		result[i] = corev1.Taint{
			Key:    t.Key,
			Value:  t.Value,
			Effect: corev1.TaintEffect(t.Effect),
		}
	}
	return result
}

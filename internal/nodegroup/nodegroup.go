// Package nodegroup manages cloudscale.ch server lifecycle for autoscaler node groups.
package nodegroup

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"maps"
	"strings"
	"sync"

	sdk "github.com/cloudscale-ch/cloudscale-go-sdk/v8"
	"k8s.io/apimachinery/pkg/api/resource"
	"k8s.io/klog/v2"

	"github.com/kubeterm-sh/autoscaler-cloudscale/internal/cloudscale"
	"github.com/kubeterm-sh/autoscaler-cloudscale/internal/config"
	"github.com/kubeterm-sh/autoscaler-cloudscale/internal/metrics"
)

type NodeGroup struct {
	cfg    config.NodeGroup
	client cloudscale.Client

	mu         sync.Mutex
	targetSize int
}

func New(cfg *config.NodeGroup, client cloudscale.Client) *NodeGroup {
	return &NodeGroup{cfg: *cfg, client: client}
}

func (ng *NodeGroup) Name() string { return ng.cfg.Name }
func (ng *NodeGroup) MinSize() int { return ng.cfg.MinSize }
func (ng *NodeGroup) MaxSize() int { return ng.cfg.MaxSize }

func (ng *NodeGroup) TargetSize() int {
	ng.mu.Lock()
	defer ng.mu.Unlock()
	return ng.targetSize
}

func (ng *NodeGroup) SetTargetSize(size int) {
	ng.mu.Lock()
	defer ng.mu.Unlock()
	ng.targetSize = size
}

func (ng *NodeGroup) Debug() string {
	return fmt.Sprintf("%s (flavor=%s, zone=%s, min=%d, max=%d)",
		ng.cfg.Name, ng.cfg.Flavor, ng.cfg.Zone, ng.cfg.MinSize, ng.cfg.MaxSize)
}

func (ng *NodeGroup) Servers() []sdk.Server {
	key, val := ng.cfg.ManagedTag()
	return ng.client.ServersByTag(key, val)
}

// SyncTargetSize sets targetSize to the actual server count.
func (ng *NodeGroup) SyncTargetSize() {
	servers := ng.Servers()
	ng.mu.Lock()
	old := ng.targetSize
	ng.targetSize = len(servers)
	current := ng.targetSize
	ng.mu.Unlock()
	klog.V(5).InfoS("synced target size", "nodeGroup", ng.cfg.Name, "from", old, "to", current)
	metrics.NodeGroupCurrentSize.WithLabelValues(ng.cfg.Name).Set(float64(len(servers)))
	metrics.NodeGroupTargetSize.WithLabelValues(ng.cfg.Name).Set(float64(current))
}

// IncreaseSize creates delta servers. On partial failure, targetSize
// is rolled back to reflect only successfully created servers.
func (ng *NodeGroup) IncreaseSize(ctx context.Context, delta int) error {
	if delta <= 0 {
		return fmt.Errorf("delta must be positive, got %d", delta)
	}

	ng.mu.Lock()
	newTarget := ng.targetSize + delta
	if newTarget > ng.cfg.MaxSize {
		ng.mu.Unlock()
		return fmt.Errorf("increasing by %d would exceed max %d (current: %d)",
			delta, ng.cfg.MaxSize, ng.targetSize)
	}
	ng.targetSize = newTarget
	ng.mu.Unlock()

	klog.InfoS("increasing node group size", "nodeGroup", ng.cfg.Name, "delta", delta, "newTarget", newTarget)

	var wg sync.WaitGroup
	sem := make(chan struct{}, maxConcurrentAPICalls)
	errsCh := make(chan error, delta)
	for range delta {
		wg.Go(func() {
			sem <- struct{}{}
			defer func() { <-sem }()
			if err := ng.createServer(ctx); err != nil {
				errsCh <- err
			}
		})
	}
	wg.Wait()
	close(errsCh)

	var errs []error
	for err := range errsCh {
		errs = append(errs, err)
	}

	if len(errs) > 0 {
		ng.mu.Lock()
		ng.targetSize -= len(errs)
		ng.mu.Unlock()
		metrics.ScaleUpTotal.WithLabelValues(ng.cfg.Name, "partial_failure").Inc()
		metrics.NodeGroupTargetSize.WithLabelValues(ng.cfg.Name).Set(float64(ng.TargetSize()))
		return fmt.Errorf("failed to create %d/%d servers: %v", len(errs), delta, errs)
	}
	metrics.ScaleUpTotal.WithLabelValues(ng.cfg.Name, "success").Inc()
	metrics.NodeGroupTargetSize.WithLabelValues(ng.cfg.Name).Set(float64(newTarget))
	metrics.NodeGroupCurrentSize.WithLabelValues(ng.cfg.Name).Set(float64(len(ng.Servers())))
	return nil
}

func (ng *NodeGroup) DeleteNodes(ctx context.Context, uuids []string) error {
	tagKey, tagVal := ng.cfg.ManagedTag()

	for _, uuid := range uuids {
		server := ng.client.ServerByUUID(uuid)
		if server == nil {
			return fmt.Errorf("server %q not found", uuid)
		}

		if server.Tags[tagKey] != tagVal {
			return fmt.Errorf("server %q does not belong to node group %q", uuid, ng.cfg.Name)
		}
	}

	klog.InfoS("deleting nodes", "nodeGroup", ng.cfg.Name, "count", len(uuids))

	var (
		wg   sync.WaitGroup
		emu  sync.Mutex
		errs []error
		sem  = make(chan struct{}, maxConcurrentAPICalls)
	)

	for _, uuid := range uuids {
		wg.Go(func() {
			sem <- struct{}{}
			defer func() { <-sem }()

			if err := ng.client.DeleteServer(ctx, uuid); err != nil {
				emu.Lock()
				errs = append(errs, err)
				emu.Unlock()
				return
			}
			ng.mu.Lock()
			ng.targetSize--
			if ng.targetSize < ng.cfg.MinSize {
				ng.targetSize = ng.cfg.MinSize
			}
			ng.mu.Unlock()
		})
	}
	wg.Wait()

	metrics.NodeGroupTargetSize.WithLabelValues(ng.cfg.Name).Set(float64(ng.TargetSize()))
	metrics.NodeGroupCurrentSize.WithLabelValues(ng.cfg.Name).Set(float64(len(ng.Servers())))
	if len(errs) > 0 {
		metrics.ScaleDownTotal.WithLabelValues(ng.cfg.Name, "partial_failure").Inc()
		return fmt.Errorf("failed to delete %d/%d nodes: %v", len(errs), len(uuids), errs)
	}
	metrics.ScaleDownTotal.WithLabelValues(ng.cfg.Name, "success").Inc()
	return nil
}

func (ng *NodeGroup) DecreaseTargetSize(delta int) error {
	if delta >= 0 {
		return fmt.Errorf("delta must be negative, got %d", delta)
	}

	// Get server count BEFORE acquiring ng.mu to avoid lock ordering
	// issues with client.mu inside ServersByTag.
	serverCount := len(ng.Servers())

	ng.mu.Lock()
	defer ng.mu.Unlock()

	newTarget := ng.targetSize + delta
	if newTarget < serverCount {
		return fmt.Errorf("cannot decrease target to %d, %d servers exist", newTarget, serverCount)
	}
	if newTarget < ng.cfg.MinSize {
		return fmt.Errorf("cannot decrease target to %d, min size is %d", newTarget, ng.cfg.MinSize)
	}
	ng.targetSize = newTarget
	return nil
}

const (
	providerIDPrefix = "cloudscale"

	// maxConcurrentAPICalls limits parallel cloudscale API requests
	// during IncreaseSize/DeleteNodes to avoid rate limiting and socket exhaustion.
	maxConcurrentAPICalls = 10
)

func (ng *NodeGroup) ProviderID(uuid string) string {
	return providerIDPrefix + "://" + uuid
}

func UUIDFromProviderID(providerID string) (string, error) {
	uuid, ok := strings.CutPrefix(providerID, providerIDPrefix+"://")
	if !ok {
		return "", fmt.Errorf("provider id %q does not have prefix %q://", providerID, providerIDPrefix)
	}
	return uuid, nil
}

type NodeInfo struct {
	VCPUCount  int
	MemoryGB   int
	DiskSizeGB int // from config, not flavor (cloudscale flavors have no disk field)
}

func (ng *NodeGroup) NodeInfo() (*NodeInfo, error) {
	flavor := ng.client.FlavorBySlug(ng.cfg.Flavor)
	if flavor == nil {
		return nil, fmt.Errorf("flavor %q not found", ng.cfg.Flavor)
	}

	return &NodeInfo{
		VCPUCount:  flavor.VCPUCount,
		MemoryGB:   flavor.MemoryGB,
		DiskSizeGB: ng.cfg.VolumeSizeGB,
	}, nil
}

func (ng *NodeGroup) Labels() map[string]string {
	return maps.Clone(ng.cfg.Labels)
}

func (ng *NodeGroup) Taints() []config.Taint {
	return ng.cfg.Taints
}

// AllocatableResource reserves 100m CPU, 100Mi memory, and 1Gi
// ephemeral storage for system overhead.
func AllocatableResource(vcpus, memoryGB, diskSizeGB int) (cpu, memory, ephemeral resource.Quantity) {
	cpu = *resource.NewMilliQuantity(int64(vcpus*1000-100), resource.DecimalSI)
	memory = *resource.NewQuantity(int64(memoryGB)*1024*1024*1024-100*1024*1024, resource.BinarySI)
	ephemeral = *resource.NewQuantity(int64(diskSizeGB)*1024*1024*1024-1*1024*1024*1024, resource.BinarySI)
	return cpu, memory, ephemeral
}

func (ng *NodeGroup) createServer(ctx context.Context) error {
	suffix, err := randomSuffix()
	if err != nil {
		return fmt.Errorf("generating name: %w", err)
	}

	req := ng.buildServerRequest(suffix)
	server, err := ng.client.CreateServer(ctx, req)
	if err != nil {
		return fmt.Errorf("creating server: %w", err)
	}
	klog.InfoS("created server", "nodeGroup", ng.cfg.Name, "serverName", req.Name, "uuid", server.UUID)
	return nil
}

func (ng *NodeGroup) buildServerRequest(suffix string) *sdk.ServerRequest {
	tags := sdk.TagMap(ng.cfg.AllTags())
	usePublic := ng.cfg.UsePublicNetwork
	usePrivate := ng.cfg.UsePrivateNetwork

	req := &sdk.ServerRequest{
		Name:              ng.cfg.Name + "-" + suffix,
		Flavor:            ng.cfg.Flavor,
		Image:             ng.cfg.Image,
		Zone:              ng.cfg.Zone,
		VolumeSizeGB:      ng.cfg.VolumeSizeGB,
		SSHKeys:           ensureSSHKeys(ng.cfg.SSHKeys),
		UserData:          ng.cfg.UserData,
		UsePublicNetwork:  &usePublic,
		UsePrivateNetwork: &usePrivate,
		TaggedResourceRequest: sdk.TaggedResourceRequest{
			Tags: &tags,
		},
	}
	if ng.cfg.ServerGroupUUID != "" {
		req.ServerGroups = []string{ng.cfg.ServerGroupUUID}
	}
	if ng.cfg.NetworkUUID != "" {
		req.Interfaces = ng.buildNetworkInterfaces()
	}
	return req
}

func (ng *NodeGroup) buildNetworkInterfaces() *[]sdk.InterfaceRequest {
	iface := sdk.InterfaceRequest{
		Network: ng.cfg.NetworkUUID,
	}
	if ng.cfg.SubnetUUID != "" {
		iface.Addresses = &[]sdk.AddressRequest{
			{Subnet: ng.cfg.SubnetUUID},
		}
	}
	return &[]sdk.InterfaceRequest{iface}
}

// ensureSSHKeys returns an empty slice if nil (cloudscale API rejects null ssh_keys).
func ensureSSHKeys(keys []string) []string {
	if keys == nil {
		return []string{}
	}
	return keys
}

func randomSuffix() (string, error) {
	b := make([]byte, 4)
	if _, err := rand.Read(b); err != nil {
		return "", fmt.Errorf("reading random bytes: %w", err)
	}
	return hex.EncodeToString(b), nil
}

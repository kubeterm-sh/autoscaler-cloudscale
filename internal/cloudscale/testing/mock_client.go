// Package cstesting provides mock implementations of cloudscale.Client for testing.
package cstesting

import (
	"context"
	"fmt"
	"sync"

	sdk "github.com/cloudscale-ch/cloudscale-go-sdk/v8"

	"github.com/kubeterm-sh/autoscaler-cloudscale/internal/cloudscale"
)

var _ cloudscale.Client = (*MockClient)(nil)

// MockClient implements cloudscale.Client for testing.
type MockClient struct {
	mu      sync.RWMutex
	Servers []sdk.Server
	Flavors []sdk.Flavor

	CreateCount int
	DeleteCount int
	CreateErr   error
	DeleteErr   error

	nextIndex int
}

// NewMockClient creates a new mock cloudscale client for testing.
func NewMockClient() *MockClient {
	return &MockClient{}
}

// Refresh implements cloudscale.Client.
func (m *MockClient) Refresh(ctx context.Context) error { return nil }

// RefreshFlavors implements cloudscale.Client.
func (m *MockClient) RefreshFlavors(ctx context.Context) error { return nil }

// ServersByTag implements cloudscale.Client.
func (m *MockClient) ServersByTag(key, value string) []sdk.Server {
	m.mu.RLock()
	defer m.mu.RUnlock()

	var result []sdk.Server
	for i := range m.Servers {
		if v, ok := m.Servers[i].Tags[key]; ok && v == value {
			result = append(result, m.Servers[i])
		}
	}
	return result
}

// ServerByUUID implements cloudscale.Client.
func (m *MockClient) ServerByUUID(uuid string) *sdk.Server {
	m.mu.RLock()
	defer m.mu.RUnlock()

	for i := range m.Servers {
		if m.Servers[i].UUID == uuid {
			out := m.Servers[i]
			return &out
		}
	}
	return nil
}

// FlavorBySlug implements cloudscale.Client.
func (m *MockClient) FlavorBySlug(slug string) *sdk.Flavor {
	m.mu.RLock()
	defer m.mu.RUnlock()

	for i := range m.Flavors {
		if m.Flavors[i].Slug == slug {
			out := m.Flavors[i]
			return &out
		}
	}
	return nil
}

// CreateServer implements cloudscale.Client.
func (m *MockClient) CreateServer(ctx context.Context, req *sdk.ServerRequest) (*sdk.Server, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	if m.CreateErr != nil {
		return nil, m.CreateErr
	}

	uuid := fmt.Sprintf("uuid-%d", m.nextIndex)
	m.nextIndex++
	m.CreateCount++

	var tags sdk.TagMap
	if req.Tags != nil {
		tags = *req.Tags
	}

	server := sdk.Server{
		UUID:           uuid,
		Name:           req.Name,
		Status:         "running",
		TaggedResource: sdk.TaggedResource{Tags: tags},
		ZonalResource: sdk.ZonalResource{
			Zone: sdk.ZoneStub{Slug: req.Zone},
		},
	}

	m.Servers = append(m.Servers, server)
	return &server, nil
}

// DeleteServer implements cloudscale.Client.
func (m *MockClient) DeleteServer(ctx context.Context, uuid string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if m.DeleteErr != nil {
		return m.DeleteErr
	}

	for i := range m.Servers {
		if m.Servers[i].UUID == uuid {
			m.Servers = append(m.Servers[:i], m.Servers[i+1:]...)
			m.DeleteCount++
			return nil
		}
	}
	return fmt.Errorf("server %q not found", uuid)
}

// AddServer adds a server to the mock with status "running".
func (m *MockClient) AddServer(uuid, name string, tags map[string]string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.Servers = append(m.Servers, sdk.Server{
		UUID: uuid, Name: name, Status: "running",
		TaggedResource: sdk.TaggedResource{Tags: sdk.TagMap(tags)},
	})
}

// AddServerWithStatus adds a server with a specific status.
func (m *MockClient) AddServerWithStatus(uuid, name, status string, tags map[string]string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.Servers = append(m.Servers, sdk.Server{
		UUID: uuid, Name: name, Status: status,
		TaggedResource: sdk.TaggedResource{Tags: sdk.TagMap(tags)},
	})
}

// AddFlavor adds a flavor. Note: cloudscale flavors use VCPUCount (not VCPUs)
// and have no disk size field — disk is configured per-server via volumes.
func (m *MockClient) AddFlavor(slug string, vcpuCount, memoryGB int) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.Flavors = append(m.Flavors, sdk.Flavor{Slug: slug, VCPUCount: vcpuCount, MemoryGB: memoryGB})
}

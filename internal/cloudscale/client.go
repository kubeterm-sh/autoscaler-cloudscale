// Package cloudscale wraps the cloudscale.ch SDK with caching and tag-based server filtering.
package cloudscale

import (
	"context"
	"fmt"
	"net/http"
	"sync"
	"time"

	"github.com/cloudscale-ch/cloudscale-go-sdk/v8"
	"golang.org/x/oauth2"
	"k8s.io/klog/v2"

	"github.com/kubeterm-sh/autoscaler-cloudscale/internal/metrics"
)

// Client defines the operations needed by the autoscaler.
type Client interface {
	Refresh(ctx context.Context) error
	RefreshFlavors(ctx context.Context) error
	ServersByTag(key, value string) []cloudscale.Server
	ServerByUUID(uuid string) *cloudscale.Server
	FlavorBySlug(slug string) *cloudscale.Flavor
	CreateServer(ctx context.Context, req *cloudscale.ServerRequest) (*cloudscale.Server, error)
	DeleteServer(ctx context.Context, uuid string) error
}

var _ Client = (*APIClient)(nil)

// flavorCacheTTL controls how long cached flavors are considered fresh.
// Cloudscale flavors rarely change, so 1 hour avoids unnecessary API calls.
const flavorCacheTTL = 1 * time.Hour

// APIClient wraps the cloudscale.ch SDK client with cached server and flavor maps.
type APIClient struct {
	api        *cloudscale.Client
	clusterTag string

	mu                 sync.RWMutex
	serversByUUID      map[string]*cloudscale.Server
	flavorsBySlug      map[string]*cloudscale.Flavor
	flavorsLastRefresh time.Time
}

// New creates a new cloudscale API client.
func New(apiToken, clusterTag string) *APIClient {
	httpClient := &http.Client{
		Timeout: 30 * time.Second,
		Transport: &oauth2.Transport{
			Base: &http.Transport{
				MaxIdleConnsPerHost: 10,
				IdleConnTimeout:     90 * time.Second,
			},
			Source: oauth2.StaticTokenSource(&oauth2.Token{AccessToken: apiToken}),
		},
	}
	return &APIClient{
		api:           cloudscale.NewClient(httpClient),
		clusterTag:    clusterTag,
		serversByUUID: make(map[string]*cloudscale.Server),
		flavorsBySlug: make(map[string]*cloudscale.Flavor),
	}
}

// Refresh reloads the server cache from the cloudscale API.
func (c *APIClient) Refresh(ctx context.Context) error {
	klog.V(5).InfoS("refreshing cloudscale server cache")

	var modifiers []cloudscale.ListRequestModifier
	if c.clusterTag != "" {
		modifiers = append(modifiers, cloudscale.WithTagFilter(cloudscale.TagMap{
			"k8s-cluster": c.clusterTag,
		}))
	}

	start := time.Now()
	servers, err := c.api.Servers.List(ctx, modifiers...)
	duration := time.Since(start).Seconds()
	metrics.APIRequestDuration.WithLabelValues("list_servers").Observe(duration)
	if err != nil {
		metrics.APIRequestsTotal.WithLabelValues("list_servers", "error").Inc()
		return fmt.Errorf("listing servers: %w", err)
	}
	metrics.APIRequestsTotal.WithLabelValues("list_servers", "success").Inc()

	byUUID := make(map[string]*cloudscale.Server, len(servers))
	for i := range servers {
		byUUID[servers[i].UUID] = &servers[i]
	}

	c.mu.Lock()
	c.serversByUUID = byUUID
	c.mu.Unlock()

	metrics.CacheServersTotal.Set(float64(len(servers)))
	klog.V(5).InfoS("cached servers from cloudscale API", "count", len(servers))
	return nil
}

// RefreshFlavors reloads the flavor cache if the TTL has expired.
func (c *APIClient) RefreshFlavors(ctx context.Context) error {
	c.mu.RLock()
	fresh := time.Since(c.flavorsLastRefresh) < flavorCacheTTL
	c.mu.RUnlock()
	if fresh {
		klog.V(5).InfoS("flavor cache still fresh, skipping refresh")
		return nil
	}

	start := time.Now()
	flavors, err := c.api.Flavors.List(ctx)
	duration := time.Since(start).Seconds()
	metrics.APIRequestDuration.WithLabelValues("list_flavors").Observe(duration)
	if err != nil {
		metrics.APIRequestsTotal.WithLabelValues("list_flavors", "error").Inc()
		return fmt.Errorf("listing flavors: %w", err)
	}
	metrics.APIRequestsTotal.WithLabelValues("list_flavors", "success").Inc()

	bySlug := make(map[string]*cloudscale.Flavor, len(flavors))
	for i := range flavors {
		bySlug[flavors[i].Slug] = &flavors[i]
	}

	c.mu.Lock()
	c.flavorsBySlug = bySlug
	c.flavorsLastRefresh = time.Now()
	c.mu.Unlock()

	metrics.CacheFlavorsTotal.Set(float64(len(flavors)))
	klog.V(5).InfoS("cached flavors from cloudscale API", "count", len(flavors))
	return nil
}

func (c *APIClient) ServersByTag(key, value string) []cloudscale.Server {
	c.mu.RLock()
	defer c.mu.RUnlock()

	result := make([]cloudscale.Server, 0, len(c.serversByUUID))
	for _, s := range c.serversByUUID {
		if v, ok := s.Tags[key]; ok && v == value {
			result = append(result, *s)
		}
	}
	return result
}

func (c *APIClient) ServerByUUID(uuid string) *cloudscale.Server {
	c.mu.RLock()
	defer c.mu.RUnlock()

	s, ok := c.serversByUUID[uuid]
	if !ok {
		return nil
	}
	out := *s
	return &out
}

func (c *APIClient) FlavorBySlug(slug string) *cloudscale.Flavor {
	c.mu.RLock()
	defer c.mu.RUnlock()

	f, ok := c.flavorsBySlug[slug]
	if !ok {
		return nil
	}
	out := *f
	return &out
}

func (c *APIClient) CreateServer(ctx context.Context, req *cloudscale.ServerRequest) (*cloudscale.Server, error) {
	start := time.Now()
	server, err := c.api.Servers.Create(ctx, req)
	duration := time.Since(start).Seconds()
	metrics.APIRequestDuration.WithLabelValues("create_server").Observe(duration)
	if err != nil {
		metrics.APIRequestsTotal.WithLabelValues("create_server", "error").Inc()
		return nil, fmt.Errorf("creating server: %w", err)
	}
	metrics.APIRequestsTotal.WithLabelValues("create_server", "success").Inc()

	serverCopy := *server
	c.mu.Lock()
	c.serversByUUID[server.UUID] = &serverCopy
	c.mu.Unlock()

	return server, nil
}

func (c *APIClient) DeleteServer(ctx context.Context, uuid string) error {
	start := time.Now()
	err := c.api.Servers.Delete(ctx, uuid)
	duration := time.Since(start).Seconds()
	metrics.APIRequestDuration.WithLabelValues("delete_server").Observe(duration)
	if err != nil {
		metrics.APIRequestsTotal.WithLabelValues("delete_server", "error").Inc()
		return fmt.Errorf("deleting server %s: %w", uuid, err)
	}
	metrics.APIRequestsTotal.WithLabelValues("delete_server", "success").Inc()

	c.mu.Lock()
	delete(c.serversByUUID, uuid)
	c.mu.Unlock()

	return nil
}

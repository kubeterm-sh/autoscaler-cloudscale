// Package config handles YAML configuration loading and validation.
package config

import (
	"cmp"
	"errors"
	"fmt"
	"maps"
	"os"
	"path/filepath"

	"gopkg.in/yaml.v3"
)

type Config struct {
	Listen             string      `yaml:"listen"`
	TLS                *TLS        `yaml:"tls,omitempty"`
	CloudscaleAPIToken string      `yaml:"cloudscaleAPIToken,omitempty"`
	ClusterTag         string      `yaml:"clusterTag,omitempty"` // filters servers by "k8s-cluster=<value>" at API level
	NodeGroups         []NodeGroup `yaml:"nodeGroups"`
}

type TLS struct {
	CertFile string `yaml:"certFile"`
	KeyFile  string `yaml:"keyFile"`
	CAFile   string `yaml:"caFile"`
}

type NodeGroup struct {
	Name              string            `yaml:"name"`
	MinSize           int               `yaml:"minSize"`
	MaxSize           int               `yaml:"maxSize"`
	Flavor            string            `yaml:"flavor"` // e.g. "flex-8-2"
	Image             string            `yaml:"image"`  // slug or UUID, e.g. "debian-12"
	Zone              string            `yaml:"zone"`   // e.g. "rma1", "lpg1"
	SSHKeys           []string          `yaml:"sshKeys,omitempty"`
	UserData          string            `yaml:"userData,omitempty"` // inline or @filepath
	Tags              map[string]string `yaml:"tags,omitempty"`
	VolumeSizeGB      int               `yaml:"volumeSizeGB"` // required: cloudscale flavors have no disk
	ServerGroupUUID   string            `yaml:"serverGroupUUID,omitempty"`
	UsePrivateNetwork bool              `yaml:"usePrivateNetwork"`
	UsePublicNetwork  bool              `yaml:"usePublicNetwork"`
	NetworkUUID       string            `yaml:"networkUUID,omitempty"`
	SubnetUUID        string            `yaml:"subnetUUID,omitempty"`
	Labels            map[string]string `yaml:"labels,omitempty"`
	Taints            []Taint           `yaml:"taints,omitempty"`
}

type Taint struct {
	Key    string `yaml:"key"`
	Value  string `yaml:"value"`
	Effect string `yaml:"effect"`
}

func Load(path string) (*Config, error) {
	data, err := os.ReadFile(path) //nolint:gosec // config path is trusted input from CLI flag
	if err != nil {
		return nil, fmt.Errorf("reading config file: %w", err)
	}

	var cfg Config
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return nil, fmt.Errorf("parsing config file: %w", err)
	}

	cfg.expandEnv()

	if err := cfg.resolveUserData(path); err != nil {
		return nil, fmt.Errorf("resolving userData: %w", err)
	}

	if err := cfg.validate(); err != nil {
		return nil, fmt.Errorf("invalid config: %w", err)
	}

	return &cfg, nil
}

// resolveUserData replaces "@filepath" userData values with file contents.
func (c *Config) resolveUserData(configPath string) error {
	baseDir := filepath.Dir(configPath)
	for i := range c.NodeGroups {
		ud := c.NodeGroups[i].UserData
		if len(ud) > 1 && ud[0] == '@' {
			path := ud[1:]
			if !filepath.IsAbs(path) {
				path = filepath.Join(baseDir, path)
			}
			data, err := os.ReadFile(path) //nolint:gosec // path from config file userData reference
			if err != nil {
				return fmt.Errorf("nodeGroups[%d] %q: reading userData file %q: %w", i, c.NodeGroups[i].Name, path, err)
			}
			c.NodeGroups[i].UserData = string(data)
		}
	}
	return nil
}

// expandEnv expands environment variables in fields that support it,
// deliberately skipping userData and other fields that may contain
// shell-like syntax (e.g. cloud-init scripts).
func (c *Config) expandEnv() {
	c.CloudscaleAPIToken = os.ExpandEnv(c.CloudscaleAPIToken)
	c.ClusterTag = os.ExpandEnv(c.ClusterTag)
	c.Listen = os.ExpandEnv(c.Listen)
	if c.TLS != nil {
		c.TLS.CertFile = os.ExpandEnv(c.TLS.CertFile)
		c.TLS.KeyFile = os.ExpandEnv(c.TLS.KeyFile)
		c.TLS.CAFile = os.ExpandEnv(c.TLS.CAFile)
	}
}

func (c *Config) validate() error {
	c.Listen = cmp.Or(c.Listen, ":8086")

	if c.CloudscaleAPIToken == "" {
		return errors.New("cloudscaleAPIToken is required")
	}

	if len(c.NodeGroups) == 0 {
		return errors.New("at least one node group must be defined")
	}

	seen := make(map[string]bool)
	for i := range c.NodeGroups {
		ng := &c.NodeGroups[i]
		if ng.Name == "" {
			return fmt.Errorf("nodeGroups[%d]: name is required", i)
		}
		if seen[ng.Name] {
			return fmt.Errorf("nodeGroups[%d]: duplicate name %q", i, ng.Name)
		}
		seen[ng.Name] = true

		if ng.Flavor == "" {
			return fmt.Errorf("nodeGroups[%d] %q: flavor is required", i, ng.Name)
		}
		if ng.Image == "" {
			return fmt.Errorf("nodeGroups[%d] %q: image is required", i, ng.Name)
		}
		if ng.Zone == "" {
			return fmt.Errorf("nodeGroups[%d] %q: zone is required", i, ng.Name)
		}
		if ng.MinSize < 0 {
			return fmt.Errorf("nodeGroups[%d] %q: minSize must be >= 0", i, ng.Name)
		}
		if ng.MaxSize < ng.MinSize {
			return fmt.Errorf("nodeGroups[%d] %q: maxSize must be >= minSize", i, ng.Name)
		}
		if ng.VolumeSizeGB <= 0 {
			return fmt.Errorf("nodeGroups[%d] %q: volumeSizeGB is required and must be > 0", i, ng.Name)
		}
		if ng.NetworkUUID != "" && !ng.UsePrivateNetwork {
			return fmt.Errorf("nodeGroups[%d] %q: networkUUID requires usePrivateNetwork to be true", i, ng.Name)
		}
		if ng.SubnetUUID != "" && ng.NetworkUUID == "" {
			return fmt.Errorf("nodeGroups[%d] %q: subnetUUID requires networkUUID", i, ng.Name)
		}

		// Auto-inject clusterTag into node group tags so new servers
		// stay visible after Refresh() tag-filtered API calls.
		if c.ClusterTag != "" {
			if ng.Tags == nil {
				ng.Tags = make(map[string]string)
			}
			ng.Tags["k8s-cluster"] = c.ClusterTag
		}
	}

	return nil
}

func (ng *NodeGroup) ManagedTag() (key, value string) {
	return "k8s-autoscaler-group", ng.Name
}

// AllTags returns user tags merged with the managed tag.
func (ng *NodeGroup) AllTags() map[string]string {
	tags := maps.Clone(ng.Tags)
	if tags == nil {
		tags = make(map[string]string)
	}
	key, val := ng.ManagedTag()
	tags[key] = val
	return tags
}

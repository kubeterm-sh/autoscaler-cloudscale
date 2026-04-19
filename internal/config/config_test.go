package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLoad_Valid(t *testing.T) {
	cfg, err := Load(writeTemp(t, `
listen: ":9090"
cloudscaleAPIToken: test-token
nodeGroups:
  - name: pool1
    minSize: 0
    maxSize: 5
    flavor: flex-8-2
    image: debian-12
    zone: rma1
    volumeSizeGB: 100
    tags:
      cluster: test
    labels:
      role: worker
`))
	if err != nil {
		t.Fatal(err)
	}

	if cfg.Listen != ":9090" {
		t.Errorf("listen=%s", cfg.Listen)
	}
	if len(cfg.NodeGroups) != 1 {
		t.Fatalf("nodeGroups=%d", len(cfg.NodeGroups))
	}

	ng := cfg.NodeGroups[0]
	if ng.Name != "pool1" || ng.Flavor != "flex-8-2" || ng.VolumeSizeGB != 100 {
		t.Errorf("ng=%+v", ng)
	}
}

func TestLoad_Defaults(t *testing.T) {
	cfg, err := Load(writeTemp(t, `
cloudscaleAPIToken: test-token
nodeGroups:
  - name: pool1
    maxSize: 3
    flavor: flex-4-1
    image: ubuntu-22.04
    zone: lpg1
    volumeSizeGB: 50
`))
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Listen != ":8086" {
		t.Errorf("default listen=%s", cfg.Listen)
	}
}

func TestLoad_EnvExpansion(t *testing.T) {
	t.Setenv("TEST_API_TOKEN", "secret-token")
	cfg, err := Load(writeTemp(t, `
cloudscaleAPIToken: ${TEST_API_TOKEN}
nodeGroups:
  - name: pool1
    maxSize: 1
    flavor: flex-4-1
    image: debian-12
    zone: rma1
    volumeSizeGB: 50
`))
	if err != nil {
		t.Fatal(err)
	}
	if cfg.CloudscaleAPIToken != "secret-token" {
		t.Errorf("cloudscaleAPIToken=%s", cfg.CloudscaleAPIToken)
	}
}

func TestLoad_NoNodeGroups(t *testing.T) {
	_, err := Load(writeTemp(t, `nodeGroups: []`))
	if err == nil {
		t.Fatal("expected error")
	}
}

func TestValidate_MissingName(t *testing.T) {
	if err := (&Config{NodeGroups: []NodeGroup{
		{Flavor: "f", Image: "i", Zone: "z", MaxSize: 1, VolumeSizeGB: 50},
	}}).validate(); err == nil {
		t.Error("expected error")
	}
}

func TestValidate_DuplicateName(t *testing.T) {
	if err := (&Config{NodeGroups: []NodeGroup{
		{Name: "a", Flavor: "f", Image: "i", Zone: "z", MaxSize: 1, VolumeSizeGB: 50},
		{Name: "a", Flavor: "f", Image: "i", Zone: "z", MaxSize: 2, VolumeSizeGB: 50},
	}}).validate(); err == nil {
		t.Error("expected error")
	}
}

func TestValidate_MissingFlavor(t *testing.T) {
	if err := (&Config{NodeGroups: []NodeGroup{
		{Name: "a", Image: "i", Zone: "z", MaxSize: 1, VolumeSizeGB: 50},
	}}).validate(); err == nil {
		t.Error("expected error")
	}
}

func TestValidate_MissingImage(t *testing.T) {
	if err := (&Config{NodeGroups: []NodeGroup{
		{Name: "a", Flavor: "f", Zone: "z", MaxSize: 1, VolumeSizeGB: 50},
	}}).validate(); err == nil {
		t.Error("expected error")
	}
}

func TestValidate_MissingZone(t *testing.T) {
	if err := (&Config{NodeGroups: []NodeGroup{
		{Name: "a", Flavor: "f", Image: "i", MaxSize: 1, VolumeSizeGB: 50},
	}}).validate(); err == nil {
		t.Error("expected error")
	}
}

func TestValidate_NegativeMinSize(t *testing.T) {
	if err := (&Config{NodeGroups: []NodeGroup{
		{Name: "a", Flavor: "f", Image: "i", Zone: "z", MinSize: -1, MaxSize: 1, VolumeSizeGB: 50},
	}}).validate(); err == nil {
		t.Error("expected error")
	}
}

func TestValidate_MaxLessThanMin(t *testing.T) {
	if err := (&Config{NodeGroups: []NodeGroup{
		{Name: "a", Flavor: "f", Image: "i", Zone: "z", MinSize: 5, MaxSize: 3, VolumeSizeGB: 50},
	}}).validate(); err == nil {
		t.Error("expected error")
	}
}

func TestValidate_MissingVolumeSizeGB(t *testing.T) {
	if err := (&Config{NodeGroups: []NodeGroup{
		{Name: "a", Flavor: "f", Image: "i", Zone: "z", MaxSize: 1},
	}}).validate(); err == nil {
		t.Error("expected error for missing volumeSizeGB")
	}
}

func TestManagedTag(t *testing.T) {
	k, v := (&NodeGroup{Name: "pool"}).ManagedTag()
	if k != "k8s-autoscaler-group" || v != "pool" {
		t.Errorf("got %s=%s", k, v)
	}
}

func TestAllTags(t *testing.T) {
	ng := NodeGroup{Name: "p1", Tags: map[string]string{"a": "1", "b": "2"}}
	tags := ng.AllTags()
	if len(tags) != 3 {
		t.Fatalf("expected 3, got %d", len(tags))
	}
	if tags["a"] != "1" || tags["b"] != "2" || tags["k8s-autoscaler-group"] != "p1" {
		t.Errorf("tags=%v", tags)
	}
}

func TestAllTags_NilTags(t *testing.T) {
	tags := (&NodeGroup{Name: "p1"}).AllTags()
	if len(tags) != 1 || tags["k8s-autoscaler-group"] != "p1" {
		t.Errorf("tags=%v", tags)
	}
}

func writeTemp(t *testing.T, content string) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "config.yaml")
	if err := os.WriteFile(p, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	return p
}

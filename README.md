# autoscaler-cloudscale

[![Go Version](https://img.shields.io/github/go-mod/go-version/kubeterm-sh/autoscaler-cloudscale)](https://go.dev/)
[![License](https://img.shields.io/github/license/kubeterm-sh/autoscaler-cloudscale)](./LICENSE)
[![Build Status](https://img.shields.io/github/actions/workflow/status/kubeterm-sh/autoscaler-cloudscale/test.yml?branch=main)](https://github.com/kubeterm-sh/autoscaler-cloudscale/actions)

An [external gRPC cloud provider](https://github.com/kubernetes/autoscaler/tree/master/cluster-autoscaler/cloudprovider/externalgrpc) for the Kubernetes [Cluster Autoscaler](https://github.com/kubernetes/autoscaler/tree/master/cluster-autoscaler) that manages [cloudscale.ch](https://cloudscale.ch) servers.

The Cluster Autoscaler decides *when* to scale. This project handles the *how*: it receives gRPC calls from the Cluster Autoscaler and translates them into cloudscale.ch API calls to create and delete servers. Node groups are defined in a YAML config, where each group maps to a set of servers with a common flavor, zone, image, and volume size.

### Prerequisites

- [cluster-autoscaler](https://github.com/kubernetes/autoscaler/tree/master/cluster-autoscaler) deployed in your cluster
- [cert-manager](https://cert-manager.io) for mTLS between cluster-autoscaler and this service
- A [cloud controller manager](https://github.com/cloudscale-ch/cloudscale-cloud-controller-manager) (CCM) that sets `spec.providerID` on nodes so the autoscaler can match Kubernetes nodes to cloudscale.ch servers
- A cloudscale.ch API token with read/write access

## How it works

This project implements the Kubernetes Cluster Autoscaler [externalgrpc CloudProvider](https://github.com/kubernetes/autoscaler/tree/master/cluster-autoscaler/cloudprovider/externalgrpc) interface. The cluster-autoscaler connects over gRPC (with mTLS) and drives all scaling decisions through these RPCs:

- **`Refresh`**: called each loop (~10s). Fetches the current server and flavor list from the cloudscale.ch API and syncs each node group's target size to the actual server count.
- **`NodeGroups`**: returns the configured node groups with their min/max sizes.
- **`NodeGroupForNode`**: maps a Kubernetes node to a node group by extracting the server UUID from `spec.providerID` (set by the CCM) and looking up its `k8s-autoscaler-group` tag.
- **`NodeGroupTemplateNodeInfo`**: returns a template `Node` object with capacity, labels, and taints derived from the configured flavor. The cluster-autoscaler uses this to simulate scheduling without creating a real server.
- **`NodeGroupIncreaseSize`**: creates servers concurrently via the cloudscale.ch API with the configured flavor, image, zone, and userData (cloud-init / ignition). If some creations fail, the target size is rolled back to reflect only successfully created servers.
- **`NodeGroupDeleteNodes`**: deletes servers concurrently. The target size is decremented per successful deletion and clamped to `minSize`.

Each node group is a set of identically-configured servers. Servers are tagged with `k8s-autoscaler-group=<name>` so the autoscaler can track group membership. The `clusterTag` setting filters API calls to only see servers tagged with `k8s-cluster=<value>`, keeping multi-cluster setups isolated.

## Configuration

Node group fields:

| Field | Required | Description |
|-------|----------|-------------|
| `name` | yes | Node group name, used in server names and tags |
| `minSize` | yes | Minimum number of servers |
| `maxSize` | yes | Maximum number of servers |
| `flavor` | yes | cloudscale.ch flavor slug (e.g. `flex-8-2`) |
| `image` | yes | Image slug (e.g. `debian-12`) or custom image (`custom:my-image`) |
| `zone` | yes | Zone slug (`rma1`, `lpg1`) |
| `volumeSizeGB` | yes | Root volume size in GB |
| `usePublicNetwork` | no | Attach a public network interface (default `false`) |
| `usePrivateNetwork` | no | Attach a private network interface (default `false`) |
| `networkUUID` | no | Private network UUID |
| `subnetUUID` | no | Subnet UUID within the private network |
| `serverGroupUUID` | no | Server group for anti-affinity placement |
| `sshKeys` | no | SSH key names to inject |
| `userData` | no | Inline string or `@/path/to/file` for cloud-init / machineconfig |
| `tags` | no | Additional server tags |
| `labels` | no | Kubernetes node labels applied via the template node info |
| `taints` | no | Kubernetes node taints (`key`, `value`, `effect`) |

## Deployment

Two components are needed:

1. **autoscaler-cloudscale** (this project), manages cloudscale.ch servers
2. **[cluster-autoscaler](https://github.com/kubernetes/autoscaler/tree/master/cluster-autoscaler)**, the upstream Kubernetes autoscaler, configured to use `externalgrpc` as its cloud provider

They run as separate Deployments connected over gRPC with mTLS (certificates managed by [cert-manager](https://cert-manager.io)).

### 1. Deploy autoscaler-cloudscale

```bash
helm install autoscaler-cloudscale \
  oci://ghcr.io/kubeterm-sh/charts/autoscaler-cloudscale \
  --namespace kube-system \
  --set cloudscaleAPI.token="your-token" \
  -f my-values.yaml
```

The chart creates the Deployment, Service, cert-manager certificates for mTLS, and a `cloud-config` ConfigMap for cluster-autoscaler to consume. See [chart/values.yaml](chart/values.yaml) for all options.

Example `my-values.yaml`:

```yaml
config:
  clusterTag: my-cluster
  nodeGroups:
    - name: worker
      minSize: 0
      maxSize: 10
      flavor: flex-8-2
      image: debian-12  # use "custom:my-image" for custom images
      zone: rma1
      volumeSizeGB: 100
      usePublicNetwork: true
      usePrivateNetwork: false
      sshKeys:
        - my-ssh-key
      tags:
        k8s-cluster: my-cluster
      labels:
        node.kubernetes.io/role: worker
      # userData loads a machineconfig file at server creation time.
      # The @ prefix tells autoscaler-cloudscale to read the file from disk.
      userData: "@/etc/autoscaler-cloudscale/machineconfig/machineconfig.yaml"

# Machineconfig (cloud-init / ignition) mounted from a Secret.
# Use existingSecret to reference a pre-existing Secret, or set content inline.
machineconfig:
  enabled: true
  existingSecret: my-machineconfig-secret
```

### 2. Deploy cluster-autoscaler

Deploy via the official [Helm chart](https://github.com/kubernetes/autoscaler/tree/master/cluster-autoscaler/charts/cluster-autoscaler). Set `cloudProvider` to `externalgrpc` and mount the cloud-config ConfigMap (`autoscaler-cloudscale-cloud-config`) and TLS secret (`autoscaler-cloudscale-tls`), both created automatically by step 1:

```yaml
# cluster-autoscaler Helm values
cloudProvider: externalgrpc

# Required by the chart even though node groups come from the gRPC provider.
autoDiscovery:
  clusterName: my-cluster

extraArgs:
  cloud-config: /etc/cloud-config/cloud-config.yaml

extraVolumes:
  - name: cloud-config
    configMap:
      name: autoscaler-cloudscale-cloud-config
  - name: autoscaler-tls
    secret:
      secretName: autoscaler-cloudscale-tls

extraVolumeMounts:
  - name: cloud-config
    mountPath: /etc/cloud-config
  - name: autoscaler-tls
    mountPath: /etc/autoscaler-cloudscale/tls
    readOnly: true
```

## Observability

The metrics/health HTTP server (default `:9090`) exposes:

| Endpoint | Description |
|----------|-------------|
| `/metrics` | Prometheus metrics (scale operations, gRPC latency, node group sizes) |
| `/healthz` | Health check |
| `/debug/pprof/` | pprof (requires `PPROF_ENABLED=true`) |

Log verbosity is controlled via `logVerbosity` in the Helm values (default `4`). Set to `2` for quiet operation, `5` for full trace.

All metrics use the `autoscaler_cloudscale_` prefix:

| Metric | Type | Description |
|--------|------|-------------|
| `node_group_current_size` | gauge | Actual server count per node group |
| `node_group_target_size` | gauge | Target server count per node group |
| `node_group_scale_up_total` | counter | Scale-up events by node group and result |
| `node_group_scale_down_total` | counter | Scale-down events by node group and result |
| `api_requests_total` | counter | cloudscale.ch API calls by operation and result |
| `api_request_duration_seconds` | histogram | cloudscale.ch API call latency |
| `grpc_requests_total` | counter | gRPC requests by method and status code |
| `grpc_request_duration_seconds` | histogram | gRPC request latency |

## License

Apache License 2.0, see [LICENSE](./LICENSE) for details.

# Changelog

All notable changes to this project will be documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/),
and this project adheres to [Semantic Versioning](https://semver.org/).

## [0.1.0] - 2026-04-19

Initial release of autoscaler-cloudscale, an external gRPC cloud provider that connects the Kubernetes Cluster Autoscaler to cloudscale.ch. It receives scaling decisions over gRPC and translates them into cloudscale.ch API calls to create and delete servers.

- External gRPC cloud provider implementing the `externalgrpc` CloudProvider interface
- Parallel server creation and deletion with bounded concurrency
- Partial failure rollback for scale-up operations
- Tag-based node group management (`k8s-autoscaler-group`, `clusterTag` for multi-cluster isolation)
- YAML-based configuration with per-group flavor, zone, image, volume size, network, and placement settings
- userData support for cloud-init / ignition (inline or `@filepath`)
- Node labels and taints via template node info
- mTLS between cluster-autoscaler and this service via cert-manager
- Prometheus metrics for node group sizes, scale operations, API calls, and gRPC requests
- Health check endpoint (`/healthz`) and optional pprof
- Helm chart with cert-manager integration, cloud-config generation, and machineconfig secret support
- Multi-stage scratch-based Docker image

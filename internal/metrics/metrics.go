package metrics

import "github.com/prometheus/client_golang/prometheus"

const namespace = "cloudscale_autoscaler"

var GRPCRequestsTotal = prometheus.NewCounterVec(prometheus.CounterOpts{
	Namespace: namespace,
	Subsystem: "grpc",
	Name:      "requests_total",
	Help:      "Total gRPC requests by method and status code.",
}, []string{"method", "code"})

var GRPCRequestDuration = prometheus.NewHistogramVec(prometheus.HistogramOpts{
	Namespace: namespace,
	Subsystem: "grpc",
	Name:      "request_duration_seconds",
	Help:      "Duration of gRPC requests in seconds.",
	Buckets:   []float64{0.001, 0.005, 0.01, 0.05, 0.1, 0.25, 0.5, 1, 2.5, 5, 10},
}, []string{"method"})

var APIRequestsTotal = prometheus.NewCounterVec(prometheus.CounterOpts{
	Namespace: namespace,
	Subsystem: "api",
	Name:      "requests_total",
	Help:      "Total cloudscale.ch API calls by operation and result.",
}, []string{"operation", "result"})

var APIRequestDuration = prometheus.NewHistogramVec(prometheus.HistogramOpts{
	Namespace: namespace,
	Subsystem: "api",
	Name:      "request_duration_seconds",
	Help:      "Duration of cloudscale.ch API calls in seconds.",
	Buckets:   []float64{0.05, 0.1, 0.25, 0.5, 1, 2.5, 5, 10, 30},
}, []string{"operation"})

var NodeGroupCurrentSize = prometheus.NewGaugeVec(prometheus.GaugeOpts{
	Namespace: namespace,
	Subsystem: "node_group",
	Name:      "current_size",
	Help:      "Current number of servers in the node group.",
}, []string{"node_group"})

var NodeGroupTargetSize = prometheus.NewGaugeVec(prometheus.GaugeOpts{
	Namespace: namespace,
	Subsystem: "node_group",
	Name:      "target_size",
	Help:      "Target number of servers in the node group.",
}, []string{"node_group"})

var NodeGroupMinSize = prometheus.NewGaugeVec(prometheus.GaugeOpts{
	Namespace: namespace,
	Subsystem: "node_group",
	Name:      "min_size",
	Help:      "Configured minimum size of the node group.",
}, []string{"node_group"})

var NodeGroupMaxSize = prometheus.NewGaugeVec(prometheus.GaugeOpts{
	Namespace: namespace,
	Subsystem: "node_group",
	Name:      "max_size",
	Help:      "Configured maximum size of the node group.",
}, []string{"node_group"})

var ScaleUpTotal = prometheus.NewCounterVec(prometheus.CounterOpts{
	Namespace: namespace,
	Subsystem: "node_group",
	Name:      "scale_up_total",
	Help:      "Total scale-up events by node group and result.",
}, []string{"node_group", "result"})

var ScaleDownTotal = prometheus.NewCounterVec(prometheus.CounterOpts{
	Namespace: namespace,
	Subsystem: "node_group",
	Name:      "scale_down_total",
	Help:      "Total scale-down events by node group and result.",
}, []string{"node_group", "result"})

var CacheServersTotal = prometheus.NewGauge(prometheus.GaugeOpts{
	Namespace: namespace,
	Subsystem: "cache",
	Name:      "servers_total",
	Help:      "Number of servers in the cache.",
})

var CacheFlavorsTotal = prometheus.NewGauge(prometheus.GaugeOpts{
	Namespace: namespace,
	Subsystem: "cache",
	Name:      "flavors_total",
	Help:      "Number of flavors in the cache.",
})

func init() {
	prometheus.MustRegister(
		GRPCRequestsTotal,
		GRPCRequestDuration,
		APIRequestsTotal,
		APIRequestDuration,
		NodeGroupCurrentSize,
		NodeGroupTargetSize,
		NodeGroupMinSize,
		NodeGroupMaxSize,
		ScaleUpTotal,
		ScaleDownTotal,
		CacheServersTotal,
		CacheFlavorsTotal,
	)
}

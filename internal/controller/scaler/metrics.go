package scaler

import (
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
)

var (
	// Gauge metrics
	idleNodesGauge = promauto.NewGauge(prometheus.GaugeOpts{
		Name: "agent_stack_k8s_idle_nodes_count",
		Help: "The number of nodes that are currently idle",
	})

	idleTimeMaxGauge = promauto.NewGauge(prometheus.GaugeOpts{
		Name: "agent_stack_k8s_node_idle_time_max_seconds",
		Help: "The maximum idle time for any node in seconds",
	})

	scalingInNodesGauge = promauto.NewGauge(prometheus.GaugeOpts{
		Name: "agent_stack_k8s_scaling_in_eligible_nodes_count",
		Help: "The number of nodes eligible for scaling in during the current check",
	})

	// Counters
	nodeScaleInAttemptsCounter = promauto.NewCounter(prometheus.CounterOpts{
		Name: "agent_stack_k8s_node_scale_in_attempts_total",
		Help: "The total number of attempts to scale in nodes",
	})

	nodeScaleInSuccessCounter = promauto.NewCounter(prometheus.CounterOpts{
		Name: "agent_stack_k8s_node_scale_in_success_total",
		Help: "The total number of successful node scale in operations",
	})

	nodeScaleInFailedCounter = promauto.NewCounter(prometheus.CounterOpts{
		Name: "agent_stack_k8s_node_scale_in_failed_total",
		Help: "The total number of failed node scale in operations",
	})

	// Histograms
	nodeIdleTimeBeforeScaleInHistogram = promauto.NewHistogram(prometheus.HistogramOpts{
		Name:    "agent_stack_k8s_node_idle_time_before_scale_in_seconds",
		Help:    "The time a node was idle before being scaled in",
		Buckets: prometheus.ExponentialBuckets(60, 2, 10), // Starting at 60s, doubling 10 times
	})
)

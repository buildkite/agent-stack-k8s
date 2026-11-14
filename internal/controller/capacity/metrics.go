package capacity

import (
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
)

const (
	promNamespace = "buildkite"
	promSubsystem = "capacity_gate"
)

var (
	admittedCounter = promauto.NewCounter(prometheus.CounterOpts{
		Namespace: promNamespace,
		Subsystem: promSubsystem,
		Name:      "admitted_total",
		Help:      "Jobs admitted because their pod fit on a node",
	})
	declinedCounter = promauto.NewCounter(prometheus.CounterOpts{
		Namespace: promNamespace,
		Subsystem: promSubsystem,
		Name:      "declined_total",
		Help:      "Job admission attempts declined because no node had room, leaving the job for another stack",
	})
	freeCPUGauge = promauto.NewGauge(prometheus.GaugeOpts{
		Namespace: promNamespace,
		Subsystem: promSubsystem,
		Name:      "free_cpu_cores",
		Help:      "Unrequested CPU across eligible nodes at the last admission check, net of unbound pods and admissions",
	})
)

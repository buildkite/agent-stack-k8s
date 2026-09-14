package deduper

import (
	"sync"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
)

const (
	promNamespace = "buildkite"
	promSubsystem = "deduper"
)

// Overridden by New to return len(inFlight). Guarded by metricCallbackMu
// because New can be called concurrently (at least in tests).
var (
	metricCallbackMu     sync.Mutex
	jobsRunningGaugeFunc = func() int { return 0 }
)

func jobsRunningGaugeValue() float64 {
	metricCallbackMu.Lock()
	defer metricCallbackMu.Unlock()
	return float64(jobsRunningGaugeFunc())
}

var (
	_ = promauto.NewGaugeFunc(prometheus.GaugeOpts{
		Namespace: promNamespace,
		Subsystem: promSubsystem,
		Name:      "jobs_running",
		Help:      "Current number of running jobs according to deduper",
	}, jobsRunningGaugeValue)

	jobHandlerCallsCounter = promauto.NewCounter(prometheus.CounterOpts{
		Namespace: promNamespace,
		Subsystem: promSubsystem,
		Name:      "job_handler_calls_total",
		Help:      "Count of jobs that were passed to the next handler in the chain",
	})
	jobHandlerErrorCounter = promauto.NewCounter(prometheus.CounterOpts{
		Namespace: promNamespace,
		Subsystem: promSubsystem,
		Name:      "job_handler_errors_total",
		Help:      "Count of jobs that weren't scheduled because the next handler in the chain returned an error",
	})

	onAddEventCounter = promauto.NewCounter(prometheus.CounterOpts{
		Namespace: promNamespace,
		Subsystem: promSubsystem,
		Name:      "onadd_events_total",
		Help:      "Count of OnAdd informer events",
	})
	onUpdateEventCounter = promauto.NewCounter(prometheus.CounterOpts{
		Namespace: promNamespace,
		Subsystem: promSubsystem,
		Name:      "onupdate_events_total",
		Help:      "Count of OnUpdate informer events",
	})
	onDeleteEventCounter = promauto.NewCounter(prometheus.CounterOpts{
		Namespace: promNamespace,
		Subsystem: promSubsystem,
		Name:      "ondelete_events_total",
		Help:      "Count of OnDelete informer events",
	})

	jobsMarkedRunningCounter = promauto.NewCounterVec(prometheus.CounterOpts{
		Namespace: promNamespace,
		Subsystem: promSubsystem,
		Name:      "jobs_marked_running_total",
		Help:      "Count of times a job was added to inFlight",
	}, []string{"source"})
	jobsUnmarkedRunningCounter = promauto.NewCounterVec(prometheus.CounterOpts{
		Namespace: promNamespace,
		Subsystem: promSubsystem,
		Name:      "jobs_unmarked_running_total",
		Help:      "Count of times a job was removed from inFlight",
	}, []string{"source"})
	jobsAlreadyRunningCounter = promauto.NewCounterVec(prometheus.CounterOpts{
		Namespace: promNamespace,
		Subsystem: promSubsystem,
		Name:      "jobs_already_running_total",
		Help:      "Count of times a job was already present in inFlight",
	}, []string{"source"})
	jobsAlreadyNotRunningCounter = promauto.NewCounterVec(prometheus.CounterOpts{
		Namespace: promNamespace,
		Subsystem: promSubsystem,
		Name:      "jobs_already_not_running_total",
		Help:      "Count of times a job was already missing from inFlight",
	}, []string{"source"})
)

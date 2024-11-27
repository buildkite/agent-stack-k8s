package monitor

import (
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
)

const (
	promNamespace = "buildkite"
	promSubsystem = "monitor"
)

var (
	monitorUpGauge = promauto.NewGauge(prometheus.GaugeOpts{
		Namespace: promNamespace,
		Subsystem: promSubsystem,
		Name:      "monitor_up",
		Help:      "Whether the monitor loop is running (0 = stopped, 1 = running)",
	})

	jobQueryCounter = promauto.NewCounter(prometheus.CounterOpts{
		Namespace: promNamespace,
		Subsystem: promSubsystem,
		Name:      "job_queries_total",
		Help:      "Count of queries to Buildkite to fetch jobs",
	})
	jobQueryErrorCounter = promauto.NewCounter(prometheus.CounterOpts{
		Namespace: promNamespace,
		Subsystem: promSubsystem,
		Name:      "job_query_errors_total",
		Help:      "Count of errors from queries to Buildkite to fetch jobs",
	})
	jobQueryDurationHistogram = promauto.NewHistogram(prometheus.HistogramOpts{
		Namespace:                    promNamespace,
		Subsystem:                    promSubsystem,
		Name:                         "job_query_seconds",
		Help:                         "Time taken to fetch jobs from Buildkite",
		NativeHistogramBucketFactor:  1.1,
		NativeHistogramZeroThreshold: 0.001,
	})
	jobsReturnedCounter = promauto.NewCounter(prometheus.CounterOpts{
		Namespace: promNamespace,
		Subsystem: promSubsystem,
		Name:      "jobs_returned_total",
		Help:      "Count of jobs returned from queries to Buildkite",
	})
	jobsReachedWorkerCounter = promauto.NewCounter(prometheus.CounterOpts{
		Namespace: promNamespace,
		Subsystem: promSubsystem,
		Name:      "jobs_reached_worker_total",
		Help:      "Count of jobs received by a jobHandlerWorker",
	})
	jobsFilteredOutCounter = promauto.NewCounter(prometheus.CounterOpts{
		Namespace: promNamespace,
		Subsystem: promSubsystem,
		Name:      "jobs_filtered_out_total",
		Help:      "Count of jobs that didn't match the configured agent tags",
	})
	jobHandlerCallsCounter = promauto.NewCounter(prometheus.CounterOpts{
		Namespace: promNamespace,
		Subsystem: promSubsystem,
		Name:      "jobs_handled_total",
		Help:      "Count of jobs that were passed to the next handler in the chain",
	})
	jobHandlerErrorCounter = promauto.NewCounterVec(prometheus.CounterOpts{
		Namespace: promNamespace,
		Subsystem: promSubsystem,
		Name:      "job_handler_errors_total",
		Help:      "Count of jobs that weren't scheduled because the next handler in the chain returned an error",
	}, []string{"reason"})
	staleJobsCounter = promauto.NewCounter(prometheus.CounterOpts{
		Namespace: promNamespace,
		Subsystem: promSubsystem,
		Name:      "stale_jobs_total",
		Help:      "Count of jobs that weren't scheduled because their information was queried too long ago",
	})
)

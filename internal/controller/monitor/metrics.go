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
	jobsFilteredOutCounter = promauto.NewCounter(prometheus.CounterOpts{
		Namespace: promNamespace,
		Subsystem: promSubsystem,
		Name:      "jobs_filtered_out_total",
		Help:      "Count of jobs that didn't match the configured agent tags",
	})
	jobsHandledCounter = promauto.NewCounter(prometheus.CounterOpts{
		Namespace: promNamespace,
		Subsystem: promSubsystem,
		Name:      "jobs_handled_total",
		Help:      "Count of jobs that were passed to the next handler in the chain",
	})
	jobsHandledErrorsCounter = promauto.NewCounter(prometheus.CounterOpts{
		Namespace: promNamespace,
		Subsystem: promSubsystem,
		Name:      "jobs_handled_errors_total",
		Help:      "Count of jobs that were passed to the next handler in the chain but the handler returned an error",
	})
	jobHandlerCallsCounter = promauto.NewCounter(prometheus.CounterOpts{
		Namespace: promNamespace,
		Subsystem: promSubsystem,
		Name:      "job_handler_calls_total",
		Help:      "Count of calls to the next handler",
	})
	jobHandlerErrorCounter = promauto.NewCounter(prometheus.CounterOpts{
		Namespace: promNamespace,
		Subsystem: promSubsystem,
		Name:      "job_handler_errors_total",
		Help:      "Count of calls to the next handler where the call returned an error",
	})
)

package scheduler

import (
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
)

const promNamespace = "buildkite"

// General metrics

var jobEndToEndDurationHistogram = promauto.NewHistogram(prometheus.HistogramOpts{
	Namespace:                    promNamespace,
	Name:                         "job_end_to_end_seconds",
	Help:                         "End-to-end processing times of jobs. Specifically, for each job, the duration between starting the query that returned the job from Buildkite, and successfully creating that job in Kubernetes.",
	NativeHistogramBucketFactor:  1.1,
	NativeHistogramZeroThreshold: 0.01,
})

// Scheduler metrics

var (
	jobCreateCallsCounter = promauto.NewCounter(prometheus.CounterOpts{
		Namespace: promNamespace,
		Subsystem: "scheduler",
		Name:      "job_create_calls_total",
		Help:      "Count of jobs that were passed to Kubernetes to create",
	})
	jobCreateSuccessCounter = promauto.NewCounter(prometheus.CounterOpts{
		Namespace: promNamespace,
		Subsystem: "scheduler",
		Name:      "job_create_success_total",
		Help:      "Count of jobs that were successfully created in Kubernetes",
	})
	jobCreateErrorCounter = promauto.NewCounterVec(prometheus.CounterOpts{
		Namespace: promNamespace,
		Subsystem: "scheduler",
		Name:      "job_create_errors_total",
		Help:      "Count of jobs that weren't created in Kubernetes because of an error",
	}, []string{"reason"})

	schedulerBuildkiteJobFailsCounter = promauto.NewCounter(prometheus.CounterOpts{
		Namespace: promNamespace,
		Subsystem: "scheduler",
		Name:      "jobs_failed_on_buildkite_total",
		Help:      "Count of jobs that scheduler successfully acquired and failed on Buildkite",
	})
	schedulerBuildkiteJobFailErrorsCounter = promauto.NewCounter(prometheus.CounterOpts{
		Namespace: promNamespace,
		Subsystem: "scheduler",
		Name:      "job_fail_on_buildkite_errors_total",
		Help:      "Count of errors when scheduler tried to acquire and fail a job on Buildkite",
	})
)

// Pod watcher metrics

var (
	// Overridden to return len(jobCancelCheckers) by podWatcher.
	jobCancelCheckerGaugeFunc = func() int { return 0 }
	ignoredJobsGaugeFunc      = func() int { return 0 }

	_ = promauto.NewGaugeFunc(prometheus.GaugeOpts{
		Namespace: promNamespace,
		Subsystem: "pod_watcher",
		Name:      "num_job_cancel_checkers",
		Help:      "Current count of job cancellation checkers",
	}, func() float64 { return float64(jobCancelCheckerGaugeFunc()) })
	_ = promauto.NewGaugeFunc(prometheus.GaugeOpts{
		Namespace: promNamespace,
		Subsystem: "pod_watcher",
		Name:      "num_ignored_jobs",
		Help:      "Current count of jobs ignored for podWatcher checks",
	}, func() float64 { return float64(ignoredJobsGaugeFunc()) })

	podWatcherOnAddEventCounter = promauto.NewCounter(prometheus.CounterOpts{
		Namespace: promNamespace,
		Subsystem: "pod_watcher",
		Name:      "onadd_events_total",
		Help:      "Count of OnAdd informer events",
	})
	podWatcherOnUpdateEventCounter = promauto.NewCounter(prometheus.CounterOpts{
		Namespace: promNamespace,
		Subsystem: "pod_watcher",
		Name:      "onupdate_events_total",
		Help:      "Count of OnUpdate informer events",
	})
	podWatcherOnDeleteEventCounter = promauto.NewCounter(prometheus.CounterOpts{
		Namespace: promNamespace,
		Subsystem: "pod_watcher",
		Name:      "ondelete_events_total",
		Help:      "Count of OnDelete informer events",
	})

	podWatcherBuildkiteJobFailsCounter = promauto.NewCounter(prometheus.CounterOpts{
		Namespace: promNamespace,
		Subsystem: "pod_watcher",
		Name:      "jobs_failed_on_buildkite_total",
		Help:      "Count of jobs that podWatcher successfully acquired and failed on Buildkite",
	})
	podWatcherBuildkiteJobFailErrorsCounter = promauto.NewCounter(prometheus.CounterOpts{
		Namespace: promNamespace,
		Subsystem: "pod_watcher",
		Name:      "job_fail_on_buildkite_errors_total",
		Help:      "Count of errors when podWatcher tried to acquire and fail a job on Buildkite",
	})
	podWatcherBuildkiteJobCancelsCounter = promauto.NewCounter(prometheus.CounterOpts{
		Namespace: promNamespace,
		Subsystem: "pod_watcher",
		Name:      "jobs_cancelled_on_buildkite_total",
		Help:      "Count of jobs that podWatcher successfully cancelled on Buildkite",
	})
	podWatcherBuildkiteJobCancelErrorsCounter = promauto.NewCounter(prometheus.CounterOpts{
		Namespace: promNamespace,
		Subsystem: "pod_watcher",
		Name:      "job_cancel_on_buildkite_errors_total",
		Help:      "Count of errors when podWatcher tried to cancel a job on Buildkite",
	})

	podsEvictedCounter = promauto.NewCounterVec(prometheus.CounterOpts{
		Namespace: promNamespace,
		Subsystem: "pod_watcher",
		Name:      "pods_evicted_total",
		Help:      "Count of evictions created for pods by podWatcher",
	}, []string{"eviction_reason"})
	podEvictionErrorsCounter = promauto.NewCounterVec(prometheus.CounterOpts{
		Namespace: promNamespace,
		Subsystem: "pod_watcher",
		Name:      "pod_eviction_errors_total",
		Help:      "Count of failures to create pod evictions by podWatcher",
	}, []string{"eviction_reason", "error_reason"})
)

// Completion watcher metrics

var (
	completionWatcherOnAddEventCounter = promauto.NewCounter(prometheus.CounterOpts{
		Namespace: promNamespace,
		Subsystem: "completion_watcher",
		Name:      "onadd_events_total",
		Help:      "Count of OnAdd informer events",
	})
	completionWatcherOnUpdateEventCounter = promauto.NewCounter(prometheus.CounterOpts{
		Namespace: promNamespace,
		Subsystem: "completion_watcher",
		Name:      "onupdate_events_total",
		Help:      "Count of OnUpdate informer events",
	})

	completionWatcherJobCleanupsCounter = promauto.NewCounter(prometheus.CounterOpts{
		Namespace: promNamespace,
		Subsystem: "completion_watcher",
		Name:      "cleanups_total",
		Help:      "Count of jobs successfully cleaned up",
	})
	completionWatcherJobCleanupErrorsCounter = promauto.NewCounterVec(prometheus.CounterOpts{
		Namespace: promNamespace,
		Subsystem: "completion_watcher",
		Name:      "cleanup_errors_total",
		Help:      "Count of errors during attempts to clean up a job",
	}, []string{"reason"})
)

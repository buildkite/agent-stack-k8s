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

// Job watcher metrics
var (
	// Overridden by NewJobWatcher
	jobsStallingGaugeFunc          = func() int { return 0 }
	jobWatcherIgnoredJobsGaugeFunc = func() int { return 0 }

	_ = promauto.NewGaugeFunc(prometheus.GaugeOpts{
		Namespace: promNamespace,
		Subsystem: "job_watcher",
		Name:      "num_stalling_jobs",
		Help:      "Current number of jobs that are running but have no pods",
	}, func() float64 { return float64(jobsStallingGaugeFunc()) })
	_ = promauto.NewGaugeFunc(prometheus.GaugeOpts{
		Namespace: promNamespace,
		Subsystem: "job_watcher",
		Name:      "num_ignored_jobs",
		Help:      "Current count of jobs ignored for jobWatcher checks",
	}, func() float64 { return float64(jobWatcherIgnoredJobsGaugeFunc()) })

	jobWatcherOnAddEventCounter = promauto.NewCounter(prometheus.CounterOpts{
		Namespace: promNamespace,
		Subsystem: "job_watcher",
		Name:      "onadd_events_total",
		Help:      "Count of OnAdd informer events",
	})
	jobWatcherOnUpdateEventCounter = promauto.NewCounter(prometheus.CounterOpts{
		Namespace: promNamespace,
		Subsystem: "job_watcher",
		Name:      "onupdate_events_total",
		Help:      "Count of OnUpdate informer events",
	})
	jobWatcherOnDeleteEventCounter = promauto.NewCounter(prometheus.CounterOpts{
		Namespace: promNamespace,
		Subsystem: "job_watcher",
		Name:      "ondelete_events_total",
		Help:      "Count of OnDelete informer events",
	})

	jobWatcherFinishedWithoutPodCounter = promauto.NewCounter(prometheus.CounterOpts{
		Namespace: promNamespace,
		Subsystem: "job_watcher",
		Name:      "jobs_finished_without_pod_total",
		Help:      "Count of jobs that entered a terminal state (Failed or Succeeded) without a pod",
	})
	jobWatcherStalledWithoutPodCounter = promauto.NewCounter(prometheus.CounterOpts{
		Namespace: promNamespace,
		Subsystem: "job_watcher",
		Name:      "jobs_stalled_without_pod_total",
		Help:      "Count of jobs that ran for too long without a pod",
	})

	jobWatcherBuildkiteJobFailsCounter = promauto.NewCounter(prometheus.CounterOpts{
		Namespace: promNamespace,
		Subsystem: "job_watcher",
		Name:      "jobs_failed_on_buildkite_total",
		Help:      "Count of jobs that jobWatcher successfully acquired and failed on Buildkite",
	})
	jobWatcherBuildkiteJobFailErrorsCounter = promauto.NewCounter(prometheus.CounterOpts{
		Namespace: promNamespace,
		Subsystem: "job_watcher",
		Name:      "job_fail_on_buildkite_errors_total",
		Help:      "Count of errors when jobWatcher tried to acquire and fail a job on Buildkite",
	})
	jobWatcherJobCleanupsCounter = promauto.NewCounter(prometheus.CounterOpts{
		Namespace: promNamespace,
		Subsystem: "job_watcher",
		Name:      "cleanups_total",
		Help:      "Count of stalled jobs successfully cleaned up",
	})
	jobWatcherJobCleanupErrorsCounter = promauto.NewCounterVec(prometheus.CounterOpts{
		Namespace: promNamespace,
		Subsystem: "job_watcher",
		Name:      "cleanup_errors_total",
		Help:      "Count of errors during attempts to clean up a stalled job",
	}, []string{"reason"})
)

// Pod watcher metrics

var (
	// Overridden by podWatcher.
	jobCancelCheckerGaugeFunc        = func() int { return 0 }
	podWatcherIgnoredJobsGaugeFunc   = func() int { return 0 }
	watchingForImageFailureGaugeFunc = func() int { return 0 }

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
	}, func() float64 { return float64(podWatcherIgnoredJobsGaugeFunc()) })
	_ = promauto.NewGaugeFunc(prometheus.GaugeOpts{
		Namespace: promNamespace,
		Subsystem: "pod_watcher",
		Name:      "num_watching_for_image_failure",
		Help:      "Current count of pods being watched for potential image-related failures",
	}, func() float64 { return float64(watchingForImageFailureGaugeFunc()) })

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
		Help:      "Count of jobs with finished agents successfully cleaned up",
	})
	completionWatcherJobCleanupErrorsCounter = promauto.NewCounterVec(prometheus.CounterOpts{
		Namespace: promNamespace,
		Subsystem: "completion_watcher",
		Name:      "cleanup_errors_total",
		Help:      "Count of errors during attempts to clean up a job with a finished agent",
	}, []string{"reason"})
)

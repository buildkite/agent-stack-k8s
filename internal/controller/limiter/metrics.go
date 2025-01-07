package limiter

import (
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
)

const (
	promNamespace = "buildkite"
	promSubsystem = "limiter"
)

// Overridden by New to return len(tokenBucket).
var tokensAvailableFunc = func() int { return 0 }

var (
	maxInFlightGauge = promauto.NewGauge(prometheus.GaugeOpts{
		Namespace: promNamespace,
		Subsystem: promSubsystem,
		Name:      "max_in_flight",
		Help:      "Configured limit on number of jobs simultaneously in flight",
	})
	_ = promauto.NewGaugeFunc(prometheus.GaugeOpts{
		Namespace: promNamespace,
		Subsystem: promSubsystem,
		Name:      "tokens_available",
		Help:      "Limiter tokens currently available",
	}, func() float64 { return float64(tokensAvailableFunc()) })
	tokenWaitDurationHistogram = promauto.NewHistogram(prometheus.HistogramOpts{
		Namespace:                    promNamespace,
		Subsystem:                    promSubsystem,
		Name:                         "token_wait_duration_seconds",
		Help:                         "Time spent waiting for a limiter token to become available",
		NativeHistogramBucketFactor:  1.1,
		NativeHistogramZeroThreshold: 0.01,
	})

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

	tokenUnderflowCounter = promauto.NewCounterVec(prometheus.CounterOpts{
		Namespace: promNamespace,
		Subsystem: promSubsystem,
		Name:      "token_underflows_total",
		Help:      "Count of attempts to take a token when the bucket was empty",
	}, []string{"source"})
	tokenOverflowCounter = promauto.NewCounterVec(prometheus.CounterOpts{
		Namespace: promNamespace,
		Subsystem: promSubsystem,
		Name:      "token_overflows_total",
		Help:      "Count of attempts to return a token when the bucket was full",
	}, []string{"source"})
)

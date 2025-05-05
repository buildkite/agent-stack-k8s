# Prometheus metrics

All metrics exported by agent-stack-k8s begin with `buildkite_`. The second
component of the metric name refers to the controller component that produces
the metric.

## Notes on using the metrics

Most metrics below are counter metrics, designed to be used in conjunction with
the `rate` and a time window. These are named ending in `_total`. 
PromQL examples:

- `rate(buildkite_scheduler_job_create_success_total[10m])` - jobs successfully
  created per second over a 10 minute window
- `rate(buildkite_scheduler_job_create_errors_total[10m])` - errors per second
  of failures to create jobs over a 10 minute window

Some metrics are gauges, which can be useful for diagnosing particular issues.

A few metrics are native histograms, which requires the Prometheus feature flag
to be enabled (`--enable-feature=native-histograms`). These are mostly latency
histograms named ending in `_seconds`, and again work well with `rate`:

- `rate(buildkite_job_end_to_end_seconds[10m])` - histogram of time in seconds
  that jobs spent between being returned from a query to Buildkite and being
  created in Kubernetes, over a 10 minute window
- `rate(buildkite_monitor_job_query_seconds[10m])` - histogram of time spent
  querying Buildkite for jobs that can be scheduled, over a 10 minute window

## Labels and their meanings

Label name | Description | Values
--- | --- | ---
`source` | The event that caused a counter to increase. | <ul><li>`Handle` - the previous component</li><li>`OnAdd` - the Kubernetes Informer (e.g. an existing job, or a job created by another instance of agent-stack-k8s)</li><li>`OnDelete` - the Kubernetes Informer (e.g. the job was deleted externally)</li><li>`OnUpdate` - the Kubernetes Informer (e.g. the job was modified externally or changed state automatically)</li></ul>
`reason`, `error_reason` | For operations on Kubernetes, the Kubernetes reason associated with an error | Examples <ul><li>`TooManyRequests` - the Kubernetes server is overloaded</li><li>`AlreadyExists` - the resource (e.g. job) already exists in the cluster</li><li>`Invalid` - the resource (e.g. job) couldn't be created because it was invalid</li></ul>
`reason` | For the limiter, a classification of the error returned by the downstream component | <ul><li>`duplicate` - a latter component or Kubernetes determined the job is a duplicate</li><li>`stale` - the job was cancelled or no longer existed by the time it was possible to start work on it</li><li>`other` - some other error prevented the job from being handled</li></ul>
`eviction_reason` | The reason an eviction was created | <ul><li>`image_pull_failure` - One or more container images couldn't be pulled within a timeout<li>`bk_job_cancelled` - The corresponding Buildkite job was cancelled on Buildkite</li></ul>

<!--START generate_metrics_doc.go-->

## completion_watcher

Full metric name | Labels | Description
--- | --- | ---
`buildkite_completion_watcher_cleanup_errors_total` | `reason` | Count of errors during attempts to clean up a job with a finished agent
`buildkite_completion_watcher_cleanups_total` | - | Count of jobs with finished agents successfully cleaned up
`buildkite_completion_watcher_onadd_events_total` | - | Count of OnAdd informer events
`buildkite_completion_watcher_onupdate_events_total` | - | Count of OnUpdate informer events

## deduper

Full metric name | Labels | Description
--- | --- | ---
`buildkite_deduper_job_handler_calls_total` | - | Count of jobs that were passed to the next handler in the chain
`buildkite_deduper_job_handler_errors_total` | - | Count of jobs that weren't scheduled because the next handler in the chain returned an error
`buildkite_deduper_jobs_already_not_running_total` | `source` | Count of times a job was already missing from inFlight
`buildkite_deduper_jobs_already_running_total` | `source` | Count of times a job was already present in inFlight
`buildkite_deduper_jobs_marked_running_total` | `source` | Count of times a job was added to inFlight
`buildkite_deduper_jobs_running` | - | Current number of running jobs according to deduper
`buildkite_deduper_jobs_unmarked_running_total` | `source` | Count of times a job was removed from inFlight
`buildkite_deduper_onadd_events_total` | - | Count of OnAdd informer events
`buildkite_deduper_ondelete_events_total` | - | Count of OnDelete informer events
`buildkite_deduper_onupdate_event_total` | - | Count of OnUpdate informer events

## job_watcher

Full metric name | Labels | Description
--- | --- | ---
`buildkite_job_watcher_cleanup_errors_total` | `reason` | Count of errors during attempts to clean up a stalled job
`buildkite_job_watcher_cleanups_total` | - | Count of stalled jobs successfully cleaned up
`buildkite_job_watcher_job_fail_on_buildkite_errors_total` | - | Count of errors when jobWatcher tried to acquire and fail a job on Buildkite
`buildkite_job_watcher_jobs_failed_on_buildkite_total` | - | Count of jobs that jobWatcher successfully acquired and failed on Buildkite
`buildkite_job_watcher_jobs_finished_without_pod_total` | - | Count of jobs that entered a terminal state (Failed or Succeeded) without a pod
`buildkite_job_watcher_jobs_stalled_without_pod_total` | - | Count of jobs that ran for too long without a pod
`buildkite_job_watcher_num_ignored_jobs` | - | Current count of jobs ignored for jobWatcher checks
`buildkite_job_watcher_num_stalling_jobs` | - | Current number of jobs that are running but have no pods
`buildkite_job_watcher_onadd_events_total` | - | Count of OnAdd informer events
`buildkite_job_watcher_ondelete_events_total` | - | Count of OnDelete informer events
`buildkite_job_watcher_onupdate_events_total` | - | Count of OnUpdate informer events

## limiter

Full metric name | Labels | Description
--- | --- | ---
`buildkite_limiter_job_handler_calls_total` | - | Count of jobs that were passed to the next handler in the chain
`buildkite_limiter_job_handler_errors_total` | `reason` | Count of jobs that weren't scheduled because the next handler in the chain returned an error
`buildkite_limiter_max_in_flight` | - | Configured limit on number of jobs simultaneously in flight
`buildkite_limiter_onadd_events_total` | - | Count of OnAdd informer events
`buildkite_limiter_ondelete_events_total` | - | Count of OnDelete informer events
`buildkite_limiter_onupdate_events_total` | - | Count of OnUpdate informer events
`buildkite_limiter_token_overflows_total` | `source` | Count of attempts to return a token when the bucket was full
`buildkite_limiter_token_underflows_total` | `source` | Count of attempts to take a token when the bucket was empty
`buildkite_limiter_token_wait_duration_seconds` | - | Time spent waiting for a limiter token to become available
`buildkite_limiter_tokens_available` | - | Limiter tokens currently available
`buildkite_limiter_waiting_for_token` | - | Number of limiter workers currently waiting for a token
`buildkite_limiter_waiting_for_work` | - | Number of limiter workers currently waiting for work
`buildkite_limiter_work_queue_length` | - | Amount of enqueued work in the limiter
`buildkite_limiter_work_wait_duration_seconds` | - | Time spent waiting in the limiter for work to become available

## monitor

Full metric name | Labels | Description
--- | --- | ---
`buildkite_monitor_job_handler_errors_total` | - | Count of jobs that weren't scheduled because the next handler in the chain returned an error
`buildkite_monitor_job_queries_total` | - | Count of queries to Buildkite to fetch jobs
`buildkite_monitor_job_query_errors_total` | - | Count of errors from queries to Buildkite to fetch jobs
`buildkite_monitor_job_query_seconds` | - | Time taken to fetch jobs from Buildkite
`buildkite_monitor_jobs_filtered_out_total` | - | Count of jobs that didn't match the configured agent tags
`buildkite_monitor_jobs_handled_total` | - | Count of jobs that were passed to the next handler in the chain
`buildkite_monitor_jobs_returned_total` | - | Count of jobs returned from queries to Buildkite
`buildkite_monitor_monitor_up` | - | Whether the monitor loop is running (0 = stopped, 1 = running)

## pod_watcher

Full metric name | Labels | Description
--- | --- | ---
`buildkite_pod_watcher_job_fail_on_buildkite_errors_total` | - | Count of errors when podWatcher tried to acquire and fail a job on Buildkite
`buildkite_pod_watcher_jobs_failed_on_buildkite_total` | - | Count of jobs that podWatcher successfully acquired and failed on Buildkite
`buildkite_pod_watcher_num_ignored_jobs` | - | Current count of jobs ignored for podWatcher checks
`buildkite_pod_watcher_num_job_cancel_checkers` | - | Current count of job cancellation checkers
`buildkite_pod_watcher_num_watching_for_image_failure` | - | Current count of pods being watched for potential image-related failures
`buildkite_pod_watcher_onadd_events_total` | - | Count of OnAdd informer events
`buildkite_pod_watcher_ondelete_events_total` | - | Count of OnDelete informer events
`buildkite_pod_watcher_onupdate_events_total` | - | Count of OnUpdate informer events
`buildkite_pod_watcher_pod_eviction_errors_total` | `eviction_reason`, `error_reason` | Count of failures to create pod evictions by podWatcher
`buildkite_pod_watcher_pods_evicted_total` | `eviction_reason` | Count of evictions created for pods by podWatcher

## scheduler

Full metric name | Labels | Description
--- | --- | ---
`buildkite_scheduler_job_create_calls_total` | - | Count of jobs that were passed to Kubernetes to create
`buildkite_scheduler_job_create_errors_total` | `reason` | Count of jobs that weren't created in Kubernetes because of an error
`buildkite_scheduler_job_create_success_total` | - | Count of jobs that were successfully created in Kubernetes
`buildkite_scheduler_job_fail_on_buildkite_errors_total` | - | Count of errors when scheduler tried to acquire and fail a job on Buildkite
`buildkite_scheduler_jobs_failed_on_buildkite_total` | - | Count of jobs that scheduler successfully acquired and failed on Buildkite

## Other

Full metric name | Labels | Description
--- | --- | ---
`buildkite_job_end_to_end_seconds` | - | End-to-end processing times of jobs. Specifically, for each job, the duration between starting the query that returned the job from Buildkite, and successfully creating that job in Kubernetes.

<!--END generate_metrics_doc.go-->

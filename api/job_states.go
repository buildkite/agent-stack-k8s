package api

// All the possible states a job can be in
type JobState string

const (
	// The job was accepted by the agent, and now it's waiting to start running
	JobStateAccepted JobState = "accepted"
	// The job has been assigned to an agent, and it's waiting for it to accept
	JobStateAssigned JobState = "assigned"
	// The job is waiting on a `block` step to finish
	JobStateBlocked JobState = "blocked"
	// The job was in a `blocked` state when the build failed
	JobStateBlockedFailed JobState = "blocked_failed"
	// The jobs configuration means that it can't be run
	JobStateBroken JobState = "broken"
	// The job was canceled
	JobStateCanceled JobState = "canceled"
	// The job is currently canceling
	JobStateCanceling JobState = "canceling"
	// The job expired before it was started on an agent
	JobStateExpired JobState = "expired"
	// The job has finished
	JobStateFinished JobState = "finished"
	// The job is waiting for jobs with the same concurrency group to finish
	JobStateLimited JobState = "limited"
	// The job is waiting on a concurrency group check before becoming either `limited` or `scheduled`
	JobStateLimiting JobState = "limiting"
	// The job has just been created and doesn't have a state yet
	JobStatePending JobState = "pending"
	// The job is running
	JobStateRunning JobState = "running"
	// The job is scheduled and waiting for an agent
	JobStateScheduled JobState = "scheduled"
	// The job is reserved.
	JobStateReserved JobState = "reserved"
	// The job was skipped
	JobStateSkipped JobState = "skipped"
	// The job timed out
	JobStateTimedOut JobState = "timed_out"
	// The job is timing out for taking too long
	JobStateTimingOut JobState = "timing_out"
	// This `block` job has been manually unblocked
	JobStateUnblocked JobState = "unblocked"
	// This `block` job was in an `unblocked` state when the build failed
	JobStateUnblockedFailed JobState = "unblocked_failed"
	// The job is waiting on a `wait` step to finish
	JobStateWaiting JobState = "waiting"
	// The job was in a `waiting` state when the build failed
	JobStateWaitingFailed JobState = "waiting_failed"
)

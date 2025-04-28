package api

// All the possible states a job can be in
type JobStates string

const (
	// The job was accepted by the agent, and now it's waiting to start running
	JobStatesAccepted JobStates = "ACCEPTED"
	// The job has been assigned to an agent, and it's waiting for it to accept
	JobStatesAssigned JobStates = "ASSIGNED"
	// The job is waiting on a `block` step to finish
	JobStatesBlocked JobStates = "BLOCKED"
	// The job was in a `BLOCKED` state when the build failed
	JobStatesBlockedFailed JobStates = "BLOCKED_FAILED"
	// The jobs configuration means that it can't be run
	JobStatesBroken JobStates = "BROKEN"
	// The job was canceled
	JobStatesCanceled JobStates = "CANCELED"
	// The job is currently canceling
	JobStatesCanceling JobStates = "CANCELING"
	// The job expired before it was started on an agent
	JobStatesExpired JobStates = "EXPIRED"
	// The job has finished
	JobStatesFinished JobStates = "FINISHED"
	// The job is waiting for jobs with the same concurrency group to finish
	JobStatesLimited JobStates = "LIMITED"
	// The job is waiting on a concurrency group check before becoming either `LIMITED` or `SCHEDULED`
	JobStatesLimiting JobStates = "LIMITING"
	// The job has just been created and doesn't have a state yet
	JobStatesPending JobStates = "PENDING"
	// The job is running
	JobStatesRunning JobStates = "RUNNING"
	// The job is scheduled and waiting for an agent
	JobStatesScheduled JobStates = "SCHEDULED"
	// The job was skipped
	JobStatesSkipped JobStates = "SKIPPED"
	// The job timed out
	JobStatesTimedOut JobStates = "TIMED_OUT"
	// The job is timing out for taking too long
	JobStatesTimingOut JobStates = "TIMING_OUT"
	// This `block` job has been manually unblocked
	JobStatesUnblocked JobStates = "UNBLOCKED"
	// This `block` job was in an `UNBLOCKED` state when the build failed
	JobStatesUnblockedFailed JobStates = "UNBLOCKED_FAILED"
	// The job is waiting on a `wait` step to finish
	JobStatesWaiting JobStates = "WAITING"
	// The job was in a `WAITING` state when the build failed
	JobStatesWaitingFailed JobStates = "WAITING_FAILED"
)

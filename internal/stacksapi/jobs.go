package stacksapi

import (
	"context"
	"fmt"
)

// Job represents the information necessary to run a job -- its command and environment variables
type Job struct {
	ID      string            `json:"id"`      // The Job's UUID
	Command string            `json:"command"` // The command to run for the job
	Env     map[string]string `json:"env"`     // Environment variables for the job
}

// GetJob retrieves the information necessary to run a job by its ID. Stacks should call this method once they're ready
// to run a job
func (s *Stack) GetJob(ctx context.Context, jobID string, opts ...RequestOption) (Job, error) {
	path := fmt.Sprintf("/stacks/%s/jobs/%s", s.Key, jobID)
	req, err := s.apiClient.NewRequest(ctx, "GET", path, nil, opts...)
	if err != nil {
		return Job{}, err
	}

	var job Job
	resp, err := s.apiClient.Do(ctx, req, &job)
	if err != nil {
		return Job{}, err
	}
	defer resp.Body.Close()

	return job, nil
}

// JobState is the current state of a job
type JobState struct {
	ID    string     `json:"id"`
	State StackState `json:"state"`
}

// GetJobState retrieves the current state of a job by its ID
func (s *Stack) GetJobState(ctx context.Context, jobID string, opts ...RequestOption) (JobState, error) {
	path := fmt.Sprintf("/stacks/%s/jobs/%s/state", s.Key, jobID)
	req, err := s.apiClient.NewRequest(ctx, "GET", path, nil, opts...)
	if err != nil {
		return JobState{}, err
	}

	var state JobState
	resp, err := s.apiClient.Do(ctx, req, &state)
	if err != nil {
		return JobState{}, err
	}
	defer resp.Body.Close()

	return state, nil
}

// FailJobParams is the request type for [FailJob].
type FailJobParams struct {
	Reason     string `json:"reason,omitempty"` // Optional message to include with the failure
	ExitStatus *int   `json:"exit_status"`      // The exit status code for the failed job. Defaults to -1 serverside. May not be zero
}

// FailJob fails a job by its ID, indicating to Buildkite that the stack was unable to provision infrastructure for a job,
// or was otherwise unable to fulfill a request to run the given job.
func (s *Stack) FailJob(ctx context.Context, jobID string, params FailJobParams, opts ...RequestOption) error {
	path := fmt.Sprintf("/stacks/%s/jobs/%s/fail", s.Key, jobID)
	req, err := s.apiClient.NewRequest(ctx, "POST", path, params, opts...)
	if err != nil {
		return fmt.Errorf("failed to create request: %w", err)
	}

	resp, err := s.apiClient.Do(ctx, req, nil)
	if err != nil {
		return fmt.Errorf("request failed: %w", err)
	}
	defer resp.Body.Close()

	return nil
}

// ReserveJobsParams is the request type for [ReserveJobs].
type ReserveJobsParams struct {
	JobIDs                   []string `json:"job_ids"`                    // The IDs of the jobs to reserve.
	ReservationExpirySeconds *int     `json:"reservation_expiry_seconds"` // How long to reserve the jobs for, as an integer number of seconds. If omitted, a sensible default will be supplied by the backend.
}

// JobReservations is the response type for [ReserveJobs].
type JobReservations struct {
	Reserved    []string `json:"reserved"`     // IDs of the jobs that were reserved as a result of this call
	NotReserved []string `json:"not_reserved"` // IDs of the jobs that were not reserved as a result of this call
}

// ReserveJobs takes a list of job IDs and reserves them for the current stack. While jobs are reserved, they won't appear
// in the job queue for other stacks to pick up, and an event will be emitted to indicate the reservation.
// Once jobs are reserved, the stack should take care to run them as soon as possible.
//
// Note that IDs of already-reserved jobs may be passed into this method to have their reservation expiry extended, provided
// that the same stack that reserved them in the first place is the one re-reserving them.
//
// Note that a 2xx response from this API (and nil error from this method) may result in no jobs being reserved -- jobs
// in the wrong state (anything other than `scheduled` or `reserved`) cannot be reserved. When calling this method, ensure
// that the `NotReserved` field is checked to see which jobs were not reserved.
func (s *Stack) ReserveJobs(ctx context.Context, params ReserveJobsParams, opts ...RequestOption) (JobReservations, error) {
	path := fmt.Sprintf("/stacks/%s/jobs/reserve", s.Key)
	req, err := s.apiClient.NewRequest(ctx, "POST", path, params, opts...)
	if err != nil {
		return JobReservations{}, fmt.Errorf("failed to create request: %w", err)
	}

	var reservations JobReservations
	resp, err := s.apiClient.Do(ctx, req, &reservations)
	if err != nil {
		return JobReservations{}, fmt.Errorf("request failed: %w", err)
	}
	defer resp.Body.Close()

	return reservations, nil
}

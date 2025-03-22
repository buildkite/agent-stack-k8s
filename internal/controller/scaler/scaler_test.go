package scaler

import (
	"testing"
	"time"

	"github.com/buildkite/agent-stack-k8s/v2/internal/controller/config"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap/zaptest"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes/fake"
)

func TestRecordNodeActivity(t *testing.T) {
	logger := zaptest.NewLogger(t)
	client := fake.NewSimpleClientset()

	scaler := New(logger, client, Config{
		IdleThreshold: 5 * time.Minute,
		CheckInterval: 1 * time.Minute,
	})

	// Test marking a node as active
	scaler.recordNodeActivity("node1", "job-123", true)

	scaler.nodesMutex.RLock()
	state, exists := scaler.nodeStates["node1"]
	scaler.nodesMutex.RUnlock()

	require.True(t, exists, "Node state should exist")
	assert.False(t, state.IsIdle, "Node should not be idle")
	assert.True(t, state.Jobs["job-123"], "Job should be marked active")

	// Test marking a job as inactive
	scaler.recordNodeActivity("node1", "job-123", false)

	scaler.nodesMutex.RLock()
	state = scaler.nodeStates["node1"]
	scaler.nodesMutex.RUnlock()

	require.NotNil(t, state, "Node state should exist")
	assert.True(t, state.IsIdle, "Node should be idle")
	assert.Empty(t, state.Jobs, "Jobs should be empty")

	// Test multiple jobs on one node
	scaler.recordNodeActivity("node1", "job-123", true)
	scaler.recordNodeActivity("node1", "job-456", true)

	scaler.nodesMutex.RLock()
	state = scaler.nodeStates["node1"]
	scaler.nodesMutex.RUnlock()

	require.NotNil(t, state, "Node state should exist")
	assert.False(t, state.IsIdle, "Node should not be idle")
	assert.Len(t, state.Jobs, 2, "Should have 2 active jobs")

	// Make sure removing one job doesn't make the node idle
	scaler.recordNodeActivity("node1", "job-123", false)

	scaler.nodesMutex.RLock()
	state = scaler.nodeStates["node1"]
	scaler.nodesMutex.RUnlock()

	assert.False(t, state.IsIdle, "Node should not be idle")
	assert.Len(t, state.Jobs, 1, "Should have 1 active job")

	// Removing last job should mark node as idle
	scaler.recordNodeActivity("node1", "job-456", false)

	scaler.nodesMutex.RLock()
	state = scaler.nodeStates["node1"]
	scaler.nodesMutex.RUnlock()

	assert.True(t, state.IsIdle, "Node should be idle")
	assert.Empty(t, state.Jobs, "Jobs should be empty")
}

func TestHandlePodEvents(t *testing.T) {
	logger := zaptest.NewLogger(t)
	client := fake.NewSimpleClientset()

	scaler := New(logger, client, Config{
		IdleThreshold: 5 * time.Minute,
		CheckInterval: 1 * time.Minute,
	})

	// Create test pod with job owner ref
	pod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name: "test-pod",
			Labels: map[string]string{
				config.UUIDLabel: "job-123",
			},
			OwnerReferences: []metav1.OwnerReference{
				{
					APIVersion: "batch/v1",
					Kind:       "Job",
					Name:       "test-job",
					UID:        "test-uid",
					Controller: ptr(true),
				},
			},
		},
		Spec: corev1.PodSpec{
			NodeName: "node1",
		},
		Status: corev1.PodStatus{
			Phase: corev1.PodRunning,
		},
	}

	// Test pod add
	scaler.handlePodAdd(pod)

	scaler.nodesMutex.RLock()
	state, exists := scaler.nodeStates["node1"]
	scaler.nodesMutex.RUnlock()

	require.True(t, exists, "Node state should exist")
	assert.False(t, state.IsIdle, "Node should not be idle")
	assert.True(t, state.Jobs["job-123"], "Job should be marked active")

	// Test pod update to completed
	updatedPod := pod.DeepCopy()
	updatedPod.Status.Phase = corev1.PodSucceeded

	scaler.handlePodUpdate(updatedPod)

	scaler.nodesMutex.RLock()
	state = scaler.nodeStates["node1"]
	scaler.nodesMutex.RUnlock()

	require.NotNil(t, state, "Node state should exist")
	assert.True(t, state.IsIdle, "Node should be idle")
	assert.Empty(t, state.Jobs, "Jobs should be empty")

	// Test pod delete
	scaler.recordNodeActivity("node1", "job-123", true) // Reset to active
	scaler.handlePodDelete(pod)

	scaler.nodesMutex.RLock()
	state = scaler.nodeStates["node1"]
	scaler.nodesMutex.RUnlock()

	require.NotNil(t, state, "Node state should exist")
	assert.True(t, state.IsIdle, "Node should be idle")
	assert.Empty(t, state.Jobs, "Jobs should be empty")
}

// Helper function to create pointer to bool
func ptr(b bool) *bool {
	return &b
}

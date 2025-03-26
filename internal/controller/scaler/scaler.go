package scaler

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/buildkite/agent-stack-k8s/v2/internal/controller/config"
	"go.uber.org/zap"
	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/informers"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/tools/cache"
)

// Config contains all parameters for the node scaler
type Config struct {
	// IdleThreshold is the time a node must be idle before being considered for scale in
	IdleThreshold time.Duration

	// CheckInterval is how often to check for idle nodes
	CheckInterval time.Duration

	// NodeSelector specifies which nodes to track and scale
	NodeSelector map[string]string

	// NodeTaintKey is the taint key to apply to nodes being scaled in
	// This prevents new pods from being scheduled onto nodes marked for removal
	NodeTaintKey string

	// ScaleDownNodeGroup indicates which node group to scale down (e.g., for cloud-specific NodeGroup labels)
	ScaleDownNodeGroup string

	// MaxScaleInRate limits the number of nodes that can be scaled in during each check interval
	// Zero means no limit (all idle nodes above threshold will be scaled in)
	MaxScaleInRate int
}

// NodeState tracks the activity state of an individual node
type NodeState struct {
	NodeName       string
	LastActiveTime time.Time
	IsIdle         bool
	Jobs           map[string]bool // jobUUID -> active state
}

// Scaler monitors agent activity and identifies nodes for scaling in
type Scaler struct {
	client kubernetes.Interface
	config Config
	logger *zap.Logger

	// nodesMutex protects access to the nodeStates map
	nodesMutex sync.RWMutex
	nodeStates map[string]*NodeState // nodeName -> NodeState
}

// New creates a new node scaler
func New(logger *zap.Logger, client kubernetes.Interface, cfg Config) *Scaler {
	// Set defaults for required configuration
	if cfg.IdleThreshold == 0 {
		cfg.IdleThreshold = 10 * time.Minute
	}
	if cfg.CheckInterval == 0 {
		cfg.CheckInterval = 1 * time.Minute
	}
	if cfg.NodeTaintKey == "" {
		cfg.NodeTaintKey = "agent-stack-k8s.io/scaling-in"
	}
	// MaxScaleInRate is optional, default 0 (unlimited)

	return &Scaler{
		client:     client,
		config:     cfg,
		logger:     logger.Named("scaler"),
		nodeStates: make(map[string]*NodeState),
	}
}

// RegisterInformer registers informers to track jobs and pods
func (s *Scaler) RegisterInformer(ctx context.Context, factory informers.SharedInformerFactory) error {
	// Register job informer to track job assignments
	jobInformer := factory.Batch().V1().Jobs().Informer()
	reg, err := jobInformer.AddEventHandler(cache.ResourceEventHandlerFuncs{
		AddFunc: func(obj interface{}) {
			s.handleJobAdd(obj)
		},
		UpdateFunc: func(_, newObj interface{}) {
			s.handleJobUpdate(newObj)
		},
		DeleteFunc: func(obj interface{}) {
			s.handleJobDelete(obj)
		},
	})
	if err != nil {
		return fmt.Errorf("failed to add job event handler: %w", err)
	}

	// Register pod informer to track which nodes pods are running on
	podInformer := factory.Core().V1().Pods().Informer()
	podReg, err := podInformer.AddEventHandler(cache.ResourceEventHandlerFuncs{
		AddFunc: func(obj interface{}) {
			s.handlePodAdd(obj)
		},
		UpdateFunc: func(_, newObj interface{}) {
			s.handlePodUpdate(newObj)
		},
		DeleteFunc: func(obj interface{}) {
			s.handlePodDelete(obj)
		},
	})
	if err != nil {
		return fmt.Errorf("failed to add pod event handler: %w", err)
	}

	// Start the informer factory
	go factory.Start(ctx.Done())

	// Wait for caches to sync
	if !cache.WaitForCacheSync(ctx.Done(), reg.HasSynced, podReg.HasSynced) {
		return fmt.Errorf("failed to sync informer caches")
	}

	// Start the periodic checker
	// Use a separate goroutine with context handling to ensure cleanup
	go func() {
		// Check if context is already done to prevent starting the checker
		// in test environments where it's not needed
		select {
		case <-ctx.Done():
			s.logger.Debug("Context already done, not starting periodic checker")
			return
		default:
			s.logger.Info("Starting node scaler periodic checker")
			s.startPeriodicChecker(ctx)
		}
	}()

	return nil
}

// handleJobAdd processes a new job
func (s *Scaler) handleJobAdd(obj interface{}) {
	job, ok := obj.(*batchv1.Job)
	if !ok {
		s.logger.Error("Expected Job object but got something else")
		return
	}

	jobUUID, exists := job.Labels[config.UUIDLabel]
	if !exists {
		// Not a job we care about
		return
	}

	// We'll track actual node assignment through the pod events
	s.logger.Debug("Job added to tracking", zap.String("job-uuid", jobUUID))
}

// handleJobUpdate handles job updates
func (s *Scaler) handleJobUpdate(obj interface{}) {
	job, ok := obj.(*batchv1.Job)
	if !ok {
		s.logger.Error("Expected Job object but got something else")
		return
	}

	jobUUID, exists := job.Labels[config.UUIDLabel]
	if !exists {
		// Not a job we care about
		return
	}

	// Check if job has completed
	if job.Status.CompletionTime != nil {
		s.logger.Debug("Job completed", zap.String("job-uuid", jobUUID))
		// We'll update node activity when we see the pod event
	}
}

// handleJobDelete processes job deletions
func (s *Scaler) handleJobDelete(obj interface{}) {
	job, ok := obj.(*batchv1.Job)
	if !ok {
		// Try to recover the object from tombstone
		tombstone, ok := obj.(cache.DeletedFinalStateUnknown)
		if !ok {
			s.logger.Error("Couldn't get object from tombstone")
			return
		}
		job, ok = tombstone.Obj.(*batchv1.Job)
		if !ok {
			s.logger.Error("Tombstone contained object that is not a Job")
			return
		}
	}

	jobUUID, exists := job.Labels[config.UUIDLabel]
	if !exists {
		// Not a job we care about
		return
	}

	s.logger.Debug("Job deleted", zap.String("job-uuid", jobUUID))
	// Any cleanup for this job will be handled via pod events
}

// handlePodAdd tracks new pods and their node assignments
func (s *Scaler) handlePodAdd(obj interface{}) {
	pod, ok := obj.(*corev1.Pod)
	if !ok {
		s.logger.Error("Expected Pod object but got something else")
		return
	}

	// Check if this pod is part of a job we're tracking
	ownerRef := metav1.GetControllerOf(pod)
	if ownerRef == nil || ownerRef.Kind != "Job" {
		// Not owned by a Job
		return
	}

	jobUUID, exists := pod.Labels[config.UUIDLabel]
	if !exists {
		// Not a job pod we care about
		return
	}

	// If pod has been assigned to a node, update that node's activity
	if pod.Spec.NodeName != "" {
		s.logger.Debug("Pod scheduled to node",
			zap.String("job-uuid", jobUUID),
			zap.String("node", pod.Spec.NodeName))

		s.recordNodeActivity(pod.Spec.NodeName, jobUUID, true)
	}
}

// handlePodUpdate tracks pod changes including node assignments
func (s *Scaler) handlePodUpdate(obj interface{}) {
	pod, ok := obj.(*corev1.Pod)
	if !ok {
		s.logger.Error("Expected Pod object but got something else")
		return
	}

	// Check if this pod is part of a job we're tracking
	ownerRef := metav1.GetControllerOf(pod)
	if ownerRef == nil || ownerRef.Kind != "Job" {
		// Not owned by a Job
		return
	}

	jobUUID, exists := pod.Labels[config.UUIDLabel]
	if !exists {
		// Not a job pod we care about
		return
	}

	// If pod has been assigned to a node, update that node's activity
	if pod.Spec.NodeName != "" {
		// Check if pod has finished
		isActive := pod.Status.Phase == corev1.PodRunning || pod.Status.Phase == corev1.PodPending
		s.recordNodeActivity(pod.Spec.NodeName, jobUUID, isActive)
	}
}

// handlePodDelete processes pod deletions
func (s *Scaler) handlePodDelete(obj interface{}) {
	pod, ok := obj.(*corev1.Pod)
	if !ok {
		// Try to recover the object from tombstone
		tombstone, ok := obj.(cache.DeletedFinalStateUnknown)
		if !ok {
			s.logger.Error("Couldn't get object from tombstone")
			return
		}
		pod, ok = tombstone.Obj.(*corev1.Pod)
		if !ok {
			s.logger.Error("Tombstone contained object that is not a Pod")
			return
		}
	}

	// Check if this pod is part of a job we're tracking
	jobUUID, exists := pod.Labels[config.UUIDLabel]
	if !exists {
		// Not a job pod we care about
		return
	}

	// If pod was on a node, mark the job as inactive
	if pod.Spec.NodeName != "" {
		s.logger.Debug("Pod deleted",
			zap.String("job-uuid", jobUUID),
			zap.String("node", pod.Spec.NodeName))

		s.recordNodeActivity(pod.Spec.NodeName, jobUUID, false)
	}
}

// recordNodeActivity updates a node's activity status for a specific job
func (s *Scaler) recordNodeActivity(nodeName, jobUUID string, isActive bool) {
	s.nodesMutex.Lock()
	defer s.nodesMutex.Unlock()

	// Get or create the node state
	state, exists := s.nodeStates[nodeName]
	if !exists {
		state = &NodeState{
			NodeName:       nodeName,
			LastActiveTime: time.Now(),
			Jobs:           make(map[string]bool),
		}
		s.nodeStates[nodeName] = state
	}

	if isActive {
		// Mark the job as active on this node
		state.Jobs[jobUUID] = true
		state.LastActiveTime = time.Now()
		state.IsIdle = false
		s.logger.Debug("Node activity recorded",
			zap.String("node", nodeName),
			zap.String("job-uuid", jobUUID))
	} else {
		// Remove the job from this node
		delete(state.Jobs, jobUUID)

		// If no active jobs, mark node as idle
		if len(state.Jobs) == 0 {
			state.IsIdle = true
			s.logger.Debug("Node now idle", zap.String("node", nodeName))
		}
	}
}

// startPeriodicChecker runs the idle node check at regular intervals
func (s *Scaler) startPeriodicChecker(ctx context.Context) {
	ticker := time.NewTicker(s.config.CheckInterval)
	defer ticker.Stop()

	s.logger.Debug("Periodic node checker started")

	for {
		select {
		case <-ctx.Done():
			s.logger.Debug("Periodic node checker stopping due to context cancellation")
			return
		case <-ticker.C:
			// Check again if context is done before running the check
			select {
			case <-ctx.Done():
				s.logger.Debug("Context cancelled before idle node check")
				return
			default:
				s.checkForIdleNodesToScaleIn(ctx)
			}
		}
	}
}

// checkForIdleNodesToScaleIn identifies and initiates scale-in for idle nodes
func (s *Scaler) checkForIdleNodesToScaleIn(ctx context.Context) {
	s.nodesMutex.RLock()
	var nodesToScaleIn []string
	var maxIdleTime float64

	now := time.Now()
	idleCount := 0

	// First pass: identify idle nodes
	for nodeName, state := range s.nodeStates {
		if !state.IsIdle {
			continue
		}

		idleCount++
		idleTime := now.Sub(state.LastActiveTime).Seconds()

		// Update max idle time metric
		if idleTime > maxIdleTime {
			maxIdleTime = idleTime
		}

		// Check if node has been idle long enough to scale in
		if now.Sub(state.LastActiveTime) >= s.config.IdleThreshold {
			nodesToScaleIn = append(nodesToScaleIn, nodeName)
			s.logger.Info("Node identified for scale-in",
				zap.String("node", nodeName),
				zap.Duration("idle_time", now.Sub(state.LastActiveTime)))
		}
	}
	s.nodesMutex.RUnlock()

	// Update metrics
	idleNodesGauge.Set(float64(idleCount))
	idleTimeMaxGauge.Set(maxIdleTime)

	// Apply scale-in rate limit if configured
	nodesToScaleInCount := len(nodesToScaleIn)
	scalingInNodesGauge.Set(float64(nodesToScaleInCount))

	if s.config.MaxScaleInRate > 0 && nodesToScaleInCount > s.config.MaxScaleInRate {
		s.logger.Info("Limiting scale-in rate",
			zap.Int("eligible_nodes", nodesToScaleInCount),
			zap.Int("max_scale_in_rate", s.config.MaxScaleInRate))

		// Sort nodes by idle time (oldest idle first)
		// This is a simple approach - we're already iterating through the map randomly
		// but in a production environment, you might want to sort by idle time
		nodesToScaleIn = nodesToScaleIn[:s.config.MaxScaleInRate]
	}

	// Second pass: scale in the identified nodes
	for _, nodeName := range nodesToScaleIn {
		s.initiateNodeScaleIn(ctx, nodeName)
	}
}

// initiateNodeScaleIn prepares a node for removal
func (s *Scaler) initiateNodeScaleIn(ctx context.Context, nodeName string) {
	nodeScaleInAttemptsCounter.Inc()

	// Get the current node state
	s.nodesMutex.RLock()
	state, exists := s.nodeStates[nodeName]
	if !exists {
		s.nodesMutex.RUnlock()
		s.logger.Error("Node state not found", zap.String("node", nodeName))
		nodeScaleInFailedCounter.Inc()
		return
	}

	idleTime := time.Since(state.LastActiveTime)
	s.nodesMutex.RUnlock()

	// Record the idle time before scale-in
	nodeIdleTimeBeforeScaleInHistogram.Observe(idleTime.Seconds())

	// 1. Apply taint to prevent new pods from being scheduled
	err := s.taintNode(ctx, nodeName)
	if err != nil {
		s.logger.Error("Failed to taint node",
			zap.String("node", nodeName),
			zap.Error(err))
		nodeScaleInFailedCounter.Inc()
		return
	}

	// 2. Label node for cleanup by cluster autoscaler or other mechanism
	err = s.markNodeForScaleIn(ctx, nodeName)
	if err != nil {
		s.logger.Error("Failed to mark node for scale-in",
			zap.String("node", nodeName),
			zap.Error(err))
		nodeScaleInFailedCounter.Inc()
		return
	}

	// Success
	nodeScaleInSuccessCounter.Inc()
	s.logger.Info("Node prepared for scale-in",
		zap.String("node", nodeName),
		zap.Duration("idle_time", idleTime))

	// Remove node from tracking
	s.nodesMutex.Lock()
	delete(s.nodeStates, nodeName)
	s.nodesMutex.Unlock()
}

// taintNode applies a taint to prevent new pods from scheduling
func (s *Scaler) taintNode(ctx context.Context, nodeName string) error {
	// Get the node
	node, err := s.client.CoreV1().Nodes().Get(ctx, nodeName, metav1.GetOptions{})
	if err != nil {
		return fmt.Errorf("error getting node: %w", err)
	}

	// Check if taint already exists
	taintExists := false
	for _, taint := range node.Spec.Taints {
		if taint.Key == s.config.NodeTaintKey {
			taintExists = true
			break
		}
	}

	if !taintExists {
		// Add taint
		newTaint := corev1.Taint{
			Key:    s.config.NodeTaintKey,
			Value:  "true",
			Effect: corev1.TaintEffectNoSchedule,
		}
		node.Spec.Taints = append(node.Spec.Taints, newTaint)

		// Update the node
		_, err = s.client.CoreV1().Nodes().Update(ctx, node, metav1.UpdateOptions{})
		if err != nil {
			return fmt.Errorf("error updating node with taint: %w", err)
		}
	}

	return nil
}

// markNodeForScaleIn labels a node to be removed by cluster autoscaler or other mechanisms
func (s *Scaler) markNodeForScaleIn(ctx context.Context, nodeName string) error {
	// Get the node
	node, err := s.client.CoreV1().Nodes().Get(ctx, nodeName, metav1.GetOptions{})
	if err != nil {
		return fmt.Errorf("error getting node: %w", err)
	}

	// Add scale-in label
	if node.Labels == nil {
		node.Labels = make(map[string]string)
	}
	node.Labels["agent-stack-k8s.io/scale-in"] = "true"

	// Add node group label if configured
	if s.config.ScaleDownNodeGroup != "" {
		node.Labels["agent-stack-k8s.io/node-group"] = s.config.ScaleDownNodeGroup
	}

	// Update the node
	_, err = s.client.CoreV1().Nodes().Update(ctx, node, metav1.UpdateOptions{})
	if err != nil {
		return fmt.Errorf("error updating node with scale-in label: %w", err)
	}

	return nil
}

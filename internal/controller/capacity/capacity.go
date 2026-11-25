// Package capacity admits a job only while the cluster can schedule its pod
// right now. Controllers that share nodes then fill them without per-queue
// caps, and every job a controller declines stays unreserved in Buildkite for
// another stack to run.
package capacity

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/buildkite/agent-stack-k8s/v2/internal/controller/config"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/client-go/informers"
	corelisters "k8s.io/client-go/listers/core/v1"
	"k8s.io/client-go/tools/cache"
)

// DefaultAdmissionTTL bounds how long an admitted job counts against capacity
// before its pod appears. A Job's pod normally appears within seconds.
const DefaultAdmissionTTL = 2 * time.Minute

// DefaultSyncTimeout bounds the wait for the first list of pods and nodes.
// Without cluster-wide access the informers never sync, so Register fails
// instead of leaving the controller waiting with no sign but RBAC errors.
const DefaultSyncTimeout = 2 * time.Minute

// recheckInterval wakes waiting workers even without informer events, so an
// expired admission frees its capacity.
const recheckInterval = 10 * time.Second

// Gate decides whether one more job pod fits on a node. It counts bound pods
// from every namespace and controller, Buildkite job pods waiting for one of
// its nodes, and jobs this controller admitted whose pods the informer has not
// reported yet.
//
// Pod informer handlers keep running totals of requests per node, so a
// decision costs the number of nodes and unbound pods, not every pod in the
// cluster.
//
// Controllers decide independently, so two can admit into the same free space
// within the moment before either pod appears. The losing pod stays Pending
// until a running pod finishes. Every controller counts it from then on.
type Gate struct {
	logger       *slog.Logger
	cpu          resource.Quantity
	memory       resource.Quantity
	nodeSelector labels.Selector
	admissionTTL time.Duration
	syncTimeout  time.Duration
	now          func() time.Time

	nodes corelisters.NodeLister

	mu sync.Mutex
	// requested holds the requests of non-terminal pods bound to each node.
	requested map[string]usage
	// unbound holds the non-terminal Buildkite job pods with no node yet.
	unbound map[string]*corev1.Pod
	// observed counts the pods seen for each job UUID, terminal ones included.
	observed map[string]int
	admitted map[string]time.Time
}

type usage struct {
	cpu    resource.Quantity
	memory resource.Quantity
}

// New builds a Gate from cfg. Call Register before TryAdmit.
func New(logger *slog.Logger, cfg *config.CapacityGateConfig) (*Gate, error) {
	cpu, err := resource.ParseQuantity(cfg.CPU)
	if err != nil {
		return nil, fmt.Errorf("capacity-gate cpu %q: %w", cfg.CPU, err)
	}
	memory, err := resource.ParseQuantity(cfg.Memory)
	if err != nil {
		return nil, fmt.Errorf("capacity-gate memory %q: %w", cfg.Memory, err)
	}
	return &Gate{
		logger:       logger,
		cpu:          cpu,
		memory:       memory,
		nodeSelector: labels.SelectorFromSet(cfg.NodeSelector),
		admissionTTL: DefaultAdmissionTTL,
		syncTimeout:  DefaultSyncTimeout,
		now:          time.Now,
		requested:    make(map[string]usage),
		unbound:      make(map[string]*corev1.Pod),
		observed:     make(map[string]int),
		admitted:     make(map[string]time.Time),
	}, nil
}

// ErrSyncTimeout is returned by Register when pods and nodes could not be
// listed within the sync timeout, most often for lack of RBAC.
var ErrSyncTimeout = errors.New("capacity-gate needs cluster-wide get/list/watch on pods and nodes")

// Register watches nodes and pods cluster-wide through factory, which must
// have no namespace or label scope. onChange runs when capacity may have been
// freed: a pod finished or was deleted, a node changed, or the recheck
// interval passed. The Gate holds no lock while calling it.
func (g *Gate) Register(ctx context.Context, factory informers.SharedInformerFactory, onChange func()) error {
	nodeInformer := factory.Core().V1().Nodes()
	podInformer := factory.Core().V1().Pods()
	g.nodes = nodeInformer.Lister()

	nodeReg, err := nodeInformer.Informer().AddEventHandler(cache.ResourceEventHandlerFuncs{
		AddFunc:    func(any) { onChange() },
		UpdateFunc: func(any, any) { onChange() },
		DeleteFunc: func(any) { onChange() },
	})
	if err != nil {
		return err
	}
	podReg, err := podInformer.Informer().AddEventHandler(cache.ResourceEventHandlerFuncs{
		AddFunc: func(obj any) {
			if pod, ok := obj.(*corev1.Pod); ok {
				g.updatePod(nil, pod)
			}
		},
		UpdateFunc: func(prev, curr any) {
			prevPod, _ := prev.(*corev1.Pod)
			currPod, ok := curr.(*corev1.Pod)
			if !ok {
				return
			}
			if g.updatePod(prevPod, currPod) {
				onChange()
			}
		},
		DeleteFunc: func(obj any) {
			if tombstone, ok := obj.(cache.DeletedFinalStateUnknown); ok {
				obj = tombstone.Obj
			}
			if pod, ok := obj.(*corev1.Pod); ok {
				g.updatePod(pod, nil)
				onChange()
			}
		},
	})
	if err != nil {
		return err
	}

	go factory.Start(ctx.Done())
	syncCtx, cancel := context.WithTimeout(ctx, g.syncTimeout)
	defer cancel()
	if !cache.WaitForCacheSync(syncCtx.Done(), nodeReg.HasSynced, podReg.HasSynced) {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		return fmt.Errorf("%w: pods and nodes not listed within %s", ErrSyncTimeout, g.syncTimeout)
	}

	go func() {
		ticker := time.NewTicker(recheckInterval)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				onChange()
			}
		}
	}()
	return nil
}

// updatePod moves a pod's requests from its previous state to its current
// one. Either may be nil, for an add or a delete. It reports whether the pod
// gave capacity back: it finished, left its node, shrank, or stopped waiting
// for a node without getting one.
func (g *Gate) updatePod(prev, curr *corev1.Pod) (freed bool) {
	g.mu.Lock()
	defer g.mu.Unlock()

	_, wasUnbound := g.unbound[podKey(prev)]
	delete(g.unbound, podKey(prev))
	g.observe(prev, -1)
	g.observe(curr, 1)
	if pendingJobPod(curr) {
		g.unbound[podKey(curr)] = curr
	}

	prevNode, prevUsage, wasBound := boundRequests(prev)
	currNode, currUsage, isBound := boundRequests(curr)
	if wasBound {
		g.addRequests(prevNode, prevUsage, -1)
	}
	if isBound {
		g.addRequests(currNode, currUsage, 1)
	}
	if wasBound {
		return !isBound || currNode != prevNode ||
			prevUsage.cpu.Cmp(currUsage.cpu) > 0 || prevUsage.memory.Cmp(currUsage.memory) > 0
	}
	return wasUnbound && !isBound && !pendingJobPod(curr)
}

// observe counts pod, terminal or not, toward its job UUID. g.mu must be held.
func (g *Gate) observe(pod *corev1.Pod, delta int) {
	if pod == nil {
		return
	}
	id := pod.Labels[config.UUIDLabel]
	if id == "" {
		return
	}
	if g.observed[id] += delta; g.observed[id] <= 0 {
		delete(g.observed, id)
	}
}

// addRequests adds (sign 1) or removes (sign -1) u from node's total. g.mu must be held.
func (g *Gate) addRequests(node string, u usage, sign int) {
	total := g.requested[node]
	if sign > 0 {
		total.cpu.Add(u.cpu)
		total.memory.Add(u.memory)
	} else {
		total.cpu.Sub(u.cpu)
		total.memory.Sub(u.memory)
	}
	if total.cpu.IsZero() && total.memory.IsZero() {
		delete(g.requested, node)
		return
	}
	g.requested[node] = total
}

// boundRequests returns the node a non-terminal pod is bound to and its requests.
func boundRequests(pod *corev1.Pod) (string, usage, bool) {
	if pod == nil || terminal(pod) || pod.Spec.NodeName == "" {
		return "", usage{}, false
	}
	cpu, memory := podRequests(pod)
	return pod.Spec.NodeName, usage{cpu: cpu, memory: memory}, true
}

// pendingJobPod reports whether pod is a non-terminal Buildkite job pod with no node yet.
func pendingJobPod(pod *corev1.Pod) bool {
	return pod != nil && !terminal(pod) && pod.Spec.NodeName == "" && pod.Labels[config.UUIDLabel] != ""
}

func podKey(pod *corev1.Pod) string {
	if pod == nil {
		return ""
	}
	return pod.Namespace + "/" + pod.Name
}

// TryAdmit reports whether a pod for jobID fits now, and if so counts it
// against capacity until its pod appears, Release is called, or the
// admission expires.
func (g *Gate) TryAdmit(jobID string) bool {
	nodes, err := g.nodes.List(g.nodeSelector)
	if err != nil {
		g.logger.Warn("capacity gate could not list nodes, declining job", "error", err)
		return false
	}

	g.mu.Lock()
	defer g.mu.Unlock()
	snap := g.snapshot(nodes)
	admit := snap.fits(g.cpu, g.memory)
	if admit {
		g.admitted[jobID] = g.now()
	}
	freeCPU := snap.totalFreeCPU()
	freeCPUGauge.Set(freeCPU.AsApproximateFloat64())
	if admit {
		admittedCounter.Inc()
	} else {
		declinedCounter.Inc()
	}
	g.logger.Debug("capacity gate decision",
		"job-uuid", jobID,
		"admit", admit,
		"free-cpu", freeCPU.String(),
		"pending-cpu", snap.pendingCPU.String(),
	)
	return admit
}

// Release stops counting an admission whose job was not scheduled.
func (g *Gate) Release(jobID string) {
	g.mu.Lock()
	defer g.mu.Unlock()
	delete(g.admitted, jobID)
}

type nodeFree struct {
	labels      labels.Set
	allocatable usage
	cpu         resource.Quantity
	memory      resource.Quantity
}

type snapshot struct {
	free          []nodeFree
	pendingCPU    resource.Quantity
	pendingMemory resource.Quantity
}

// snapshot computes free capacity per eligible node, and the requests of
// unbound job pods and admissions that will land on one. g.mu must be held.
func (g *Gate) snapshot(nodes []*corev1.Node) snapshot {
	var snap snapshot
	for _, node := range nodes {
		if !schedulable(node) {
			continue
		}
		n := nodeFree{
			labels: labels.Set(node.Labels),
			allocatable: usage{
				cpu:    node.Status.Allocatable.Cpu().DeepCopy(),
				memory: node.Status.Allocatable.Memory().DeepCopy(),
			},
		}
		n.cpu = n.allocatable.cpu.DeepCopy()
		n.memory = n.allocatable.memory.DeepCopy()
		if u, ok := g.requested[node.Name]; ok {
			n.cpu.Sub(u.cpu)
			n.memory.Sub(u.memory)
		}
		snap.free = append(snap.free, n)
	}

	for _, pod := range g.unbound {
		cpu, memory := podRequests(pod)
		if snap.couldHost(pod, cpu, memory) {
			snap.pendingCPU.Add(cpu)
			snap.pendingMemory.Add(memory)
		}
	}

	now := g.now()
	for id, at := range g.admitted {
		if g.observed[id] > 0 || now.Sub(at) > g.admissionTTL {
			delete(g.admitted, id)
			continue
		}
		snap.pendingCPU.Add(g.cpu)
		snap.pendingMemory.Add(g.memory)
	}
	return snap
}

// couldHost reports whether an unbound pod can ever land on an eligible
// node: its node selector matches one, and its requests fit that node's
// allocatable. Any other unbound pod competes for nodes the gate doesn't
// count, or can never be scheduled, so counting it would hold capacity
// forever. Required node affinity and taints are not evaluated.
func (s snapshot) couldHost(pod *corev1.Pod, cpu, memory resource.Quantity) bool {
	selector := labels.SelectorFromSet(pod.Spec.NodeSelector)
	for _, n := range s.free {
		if selector.Matches(n.labels) && n.allocatable.cpu.Cmp(cpu) >= 0 && n.allocatable.memory.Cmp(memory) >= 0 {
			return true
		}
	}
	return false
}

// fits requires one node with room for the pod, and room across all nodes
// once every pending job pod and admission is placed.
func (s snapshot) fits(cpu, memory resource.Quantity) bool {
	oneNode := false
	totalCPU, totalMemory := resource.Quantity{}, resource.Quantity{}
	for _, n := range s.free {
		if n.cpu.Cmp(cpu) >= 0 && n.memory.Cmp(memory) >= 0 {
			oneNode = true
		}
		if n.cpu.Sign() > 0 {
			totalCPU.Add(n.cpu)
		}
		if n.memory.Sign() > 0 {
			totalMemory.Add(n.memory)
		}
	}
	totalCPU.Sub(s.pendingCPU)
	totalMemory.Sub(s.pendingMemory)
	return oneNode && totalCPU.Cmp(cpu) >= 0 && totalMemory.Cmp(memory) >= 0
}

func (s snapshot) totalFreeCPU() resource.Quantity {
	var total resource.Quantity
	for _, n := range s.free {
		if n.cpu.Sign() > 0 {
			total.Add(n.cpu)
		}
	}
	total.Sub(s.pendingCPU)
	return total
}

func schedulable(node *corev1.Node) bool {
	if node.Spec.Unschedulable {
		return false
	}
	for _, c := range node.Status.Conditions {
		if c.Type == corev1.NodeReady {
			return c.Status == corev1.ConditionTrue
		}
	}
	return false
}

func terminal(pod *corev1.Pod) bool {
	return pod.Status.Phase == corev1.PodSucceeded || pod.Status.Phase == corev1.PodFailed
}

// podRequests returns what the scheduler reserves for pod: the larger of its
// long-running containers (including restartable init containers, the native
// sidecars) and any one ordinary init container plus the sidecars started
// before it, plus pod overhead.
func podRequests(pod *corev1.Pod) (cpu, memory resource.Quantity) {
	var initCPU, initMemory resource.Quantity
	for _, c := range pod.Spec.InitContainers {
		if c.RestartPolicy != nil && *c.RestartPolicy == corev1.ContainerRestartPolicyAlways {
			cpu.Add(*c.Resources.Requests.Cpu())
			memory.Add(*c.Resources.Requests.Memory())
			continue
		}
		q := c.Resources.Requests.Cpu().DeepCopy()
		q.Add(cpu)
		if q.Cmp(initCPU) > 0 {
			initCPU = q
		}
		q = c.Resources.Requests.Memory().DeepCopy()
		q.Add(memory)
		if q.Cmp(initMemory) > 0 {
			initMemory = q
		}
	}
	for _, c := range pod.Spec.Containers {
		cpu.Add(*c.Resources.Requests.Cpu())
		memory.Add(*c.Resources.Requests.Memory())
	}
	if initCPU.Cmp(cpu) > 0 {
		cpu = initCPU
	}
	if initMemory.Cmp(memory) > 0 {
		memory = initMemory
	}
	cpu.Add(*pod.Spec.Overhead.Cpu())
	memory.Add(*pod.Spec.Overhead.Memory())
	return cpu, memory
}

// Package capacity admits a job only while the cluster can schedule its pod
// right now. Controllers that share nodes then fill them without per-queue
// caps, and every job a controller declines stays unreserved in Buildkite for
// another stack to run.
package capacity

import (
	"context"
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

// recheckInterval wakes waiting workers even without informer events, so an
// expired admission frees its capacity.
const recheckInterval = 10 * time.Second

// Gate decides whether one more job pod fits on a node. It counts pods from
// every namespace and controller, pods not yet bound to a node, and jobs this
// controller admitted whose pods the informer has not reported yet.
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
	now          func() time.Time

	nodes corelisters.NodeLister
	pods  corelisters.PodLister

	mu       sync.Mutex
	admitted map[string]time.Time
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
		now:          time.Now,
		admitted:     make(map[string]time.Time),
	}, nil
}

// Register watches nodes and pods cluster-wide through factory, which must
// have no namespace or label scope. onChange runs when capacity may have been
// freed: a pod finished or was deleted, a node changed, or the recheck
// interval passed. It must not call back into the Gate synchronously while
// holding locks the Gate's callers hold.
func (g *Gate) Register(ctx context.Context, factory informers.SharedInformerFactory, onChange func()) error {
	nodeInformer := factory.Core().V1().Nodes()
	podInformer := factory.Core().V1().Pods()
	g.nodes = nodeInformer.Lister()
	g.pods = podInformer.Lister()

	nodeReg, err := nodeInformer.Informer().AddEventHandler(cache.ResourceEventHandlerFuncs{
		AddFunc:    func(any) { onChange() },
		UpdateFunc: func(any, any) { onChange() },
		DeleteFunc: func(any) { onChange() },
	})
	if err != nil {
		return err
	}
	podReg, err := podInformer.Informer().AddEventHandler(cache.ResourceEventHandlerFuncs{
		UpdateFunc: func(prev, curr any) {
			if !terminal(prev) && terminal(curr) {
				onChange()
			}
		},
		DeleteFunc: func(any) { onChange() },
	})
	if err != nil {
		return err
	}

	go factory.Start(ctx.Done())
	if !cache.WaitForCacheSync(ctx.Done(), nodeReg.HasSynced, podReg.HasSynced) {
		return fmt.Errorf("failed to sync capacity gate informers")
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

// TryAdmit reports whether a pod for jobID fits now, and if so counts it
// against capacity until its pod appears, Release is called, or the
// admission expires.
func (g *Gate) TryAdmit(jobID string) bool {
	g.mu.Lock()
	defer g.mu.Unlock()

	nodes, err := g.nodes.List(g.nodeSelector)
	if err != nil {
		g.logger.Warn("capacity gate could not list nodes, declining job", "error", err)
		return false
	}
	pods, err := g.pods.List(labels.Everything())
	if err != nil {
		g.logger.Warn("capacity gate could not list pods, declining job", "error", err)
		return false
	}

	snap := g.snapshot(nodes, pods)
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
	cpu    resource.Quantity
	memory resource.Quantity
}

type snapshot struct {
	free          []nodeFree
	pendingCPU    resource.Quantity
	pendingMemory resource.Quantity
}

// snapshot computes free capacity per eligible node, and the requests of
// pods and admissions not yet bound to any node. g.mu must be held.
func (g *Gate) snapshot(nodes []*corev1.Node, pods []*corev1.Pod) snapshot {
	eligible := make(map[string]int, len(nodes))
	var snap snapshot
	for _, node := range nodes {
		if !schedulable(node) {
			continue
		}
		eligible[node.Name] = len(snap.free)
		snap.free = append(snap.free, nodeFree{
			cpu:    node.Status.Allocatable.Cpu().DeepCopy(),
			memory: node.Status.Allocatable.Memory().DeepCopy(),
		})
	}

	withPod := make(map[string]struct{})
	for _, pod := range pods {
		if id := pod.Labels[config.UUIDLabel]; id != "" {
			withPod[id] = struct{}{}
		}
		if terminal(pod) {
			continue
		}
		cpu, memory := podRequests(pod)
		if pod.Spec.NodeName == "" {
			snap.pendingCPU.Add(cpu)
			snap.pendingMemory.Add(memory)
			continue
		}
		if i, ok := eligible[pod.Spec.NodeName]; ok {
			snap.free[i].cpu.Sub(cpu)
			snap.free[i].memory.Sub(memory)
		}
	}

	now := g.now()
	for id, at := range g.admitted {
		_, observed := withPod[id]
		if observed || now.Sub(at) > g.admissionTTL {
			delete(g.admitted, id)
			continue
		}
		snap.pendingCPU.Add(g.cpu)
		snap.pendingMemory.Add(g.memory)
	}
	return snap
}

// fits requires one node with room for the pod, and room across all nodes
// once every unbound pod and admission is placed.
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

func terminal(obj any) bool {
	pod, ok := obj.(*corev1.Pod)
	if !ok {
		return false
	}
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

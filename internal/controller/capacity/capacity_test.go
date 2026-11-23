package capacity

import (
	"errors"
	"fmt"
	"log/slog"
	"testing"
	"time"

	"github.com/buildkite/agent-stack-k8s/v2/internal/controller/config"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/informers"
	"k8s.io/client-go/kubernetes/fake"
	k8stesting "k8s.io/client-go/testing"
)

func node(name, cpu, memory string, ready bool) *corev1.Node {
	status := corev1.ConditionFalse
	if ready {
		status = corev1.ConditionTrue
	}
	return &corev1.Node{
		ObjectMeta: metav1.ObjectMeta{Name: name, Labels: map[string]string{"pool": "ci"}},
		Status: corev1.NodeStatus{
			Allocatable: corev1.ResourceList{
				corev1.ResourceCPU:    resource.MustParse(cpu),
				corev1.ResourceMemory: resource.MustParse(memory),
			},
			Conditions: []corev1.NodeCondition{{Type: corev1.NodeReady, Status: status}},
		},
	}
}

func requests(cpu, memory string) corev1.ResourceRequirements {
	return corev1.ResourceRequirements{Requests: corev1.ResourceList{
		corev1.ResourceCPU:    resource.MustParse(cpu),
		corev1.ResourceMemory: resource.MustParse(memory),
	}}
}

func pod(name, nodeName, jobID string, phase corev1.PodPhase, containers ...corev1.ResourceRequirements) *corev1.Pod {
	p := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: "buildkite"},
		Spec:       corev1.PodSpec{NodeName: nodeName},
		Status:     corev1.PodStatus{Phase: phase},
	}
	if jobID != "" {
		p.Labels = map[string]string{config.UUIDLabel: jobID}
	}
	for _, r := range containers {
		p.Spec.Containers = append(p.Spec.Containers, corev1.Container{Resources: r})
	}
	return p
}

func withNodeSelector(p *corev1.Pod, selector map[string]string) *corev1.Pod {
	p.Spec.NodeSelector = selector
	return p
}

// newTestGate returns an unregistered Gate for 3 CPU / 4Gi pods.
func newTestGate(t testing.TB) *Gate {
	t.Helper()
	g, err := New(slog.Default(), &config.CapacityGateConfig{CPU: "3", Memory: "4Gi", NodeSelector: map[string]string{"pool": "ci"}})
	if err != nil {
		t.Fatal(err)
	}
	return g
}

// newGateWithClient returns a registered Gate over a fake cluster holding
// objs, and the cluster's client for changing it afterwards.
func newGateWithClient(t testing.TB, objs ...runtime.Object) (*Gate, *fake.Clientset) {
	t.Helper()
	g := newTestGate(t)
	client := fake.NewClientset(objs...)
	factory := informers.NewSharedInformerFactory(client, 0)
	if err := g.Register(t.Context(), factory, func() {}); err != nil {
		t.Fatal(err)
	}
	return g, client
}

func newGate(t testing.TB, objs ...runtime.Object) *Gate {
	t.Helper()
	g, _ := newGateWithClient(t, objs...)
	return g
}

// freeCPU is TryAdmit's free CPU figure, without admitting anything.
func freeCPU(t *testing.T, g *Gate) string {
	t.Helper()
	nodes, err := g.nodes.List(g.nodeSelector)
	if err != nil {
		t.Fatal(err)
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	free := g.snapshot(nodes).totalFreeCPU()
	return free.String()
}

// waitForFreeCPU fails t unless the informer events bring free CPU to want.
func waitForFreeCPU(t *testing.T, g *Gate, want string) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for {
		got := freeCPU(t, g)
		if got == want {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("free CPU = %s, want %s", got, want)
		}
		time.Sleep(time.Millisecond)
	}
}

func TestTryAdmit(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name string
		objs []runtime.Object
		want bool
	}{
		{
			name: "room on a node",
			objs: []runtime.Object{node("n1", "8", "16Gi", true), pod("p1", "n1", "", corev1.PodRunning, requests("4", "4Gi"))},
			want: true,
		},
		{
			name: "cpu exhausted",
			objs: []runtime.Object{node("n1", "8", "16Gi", true), pod("p1", "n1", "", corev1.PodRunning, requests("5500m", "4Gi"))},
			want: false,
		},
		{
			name: "memory exhausted",
			objs: []runtime.Object{node("n1", "8", "16Gi", true), pod("p1", "n1", "", corev1.PodRunning, requests("1", "13Gi"))},
			want: false,
		},
		{
			name: "finished pods free their requests",
			objs: []runtime.Object{node("n1", "8", "16Gi", true), pod("p1", "n1", "", corev1.PodSucceeded, requests("8", "16Gi"))},
			want: true,
		},
		{
			name: "job pods awaiting a node count against capacity",
			objs: []runtime.Object{node("n1", "8", "16Gi", true), pod("p1", "", "other", corev1.PodPending, requests("6", "4Gi"))},
			want: false,
		},
		{
			name: "pods awaiting a node that are not job pods are ignored",
			objs: []runtime.Object{node("n1", "8", "16Gi", true), pod("p1", "", "", corev1.PodPending, requests("6", "4Gi"))},
			want: true,
		},
		{
			name: "job pods awaiting a node outside the selector are ignored",
			objs: []runtime.Object{node("n1", "8", "16Gi", true), withNodeSelector(pod("p1", "", "other", corev1.PodPending, requests("6", "4Gi")), map[string]string{"pool": "gpu"})},
			want: true,
		},
		{
			name: "job pods too large for any node are ignored",
			objs: []runtime.Object{node("n1", "8", "16Gi", true), pod("p1", "", "other", corev1.PodPending, requests("9", "4Gi"))},
			want: true,
		},
		{
			name: "free space split across nodes is not one node",
			objs: []runtime.Object{
				node("n1", "4", "16Gi", true), pod("p1", "n1", "", corev1.PodRunning, requests("2", "1Gi")),
				node("n2", "4", "16Gi", true), pod("p2", "n2", "", corev1.PodRunning, requests("2", "1Gi")),
			},
			want: false,
		},
		{
			name: "not-ready nodes are ignored",
			objs: []runtime.Object{node("n1", "8", "16Gi", false)},
			want: false,
		},
		{
			name: "nodes outside the selector are ignored",
			objs: []runtime.Object{func() *corev1.Node { n := node("n1", "8", "16Gi", true); n.Labels = nil; return n }()},
			want: false,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			if got := newGate(t, test.objs...).TryAdmit("job"); got != test.want {
				t.Errorf("TryAdmit() = %v, want %v", got, test.want)
			}
		})
	}
}

func TestTryAdmitCountsAdmissionsUntilTheirPodAppears(t *testing.T) {
	t.Parallel()
	n := node("n1", "7", "16Gi", true)
	g := newGate(t, n)
	if !g.TryAdmit("a") || !g.TryAdmit("b") {
		t.Fatal("two 3 CPU jobs should fit in 7 CPU")
	}
	if g.TryAdmit("c") {
		t.Fatal("a third job was admitted while two admissions still had no pod")
	}

	g.Release("b")
	if !g.TryAdmit("c") {
		t.Error("releasing an admission did not free its capacity")
	}
}

func TestTryAdmitDropsAdmissionsOnceTheirPodIsCounted(t *testing.T) {
	t.Parallel()
	g := newGate(t, node("n1", "7", "16Gi", true), pod("pa", "n1", "a", corev1.PodRunning, requests("3", "4Gi")))
	g.admitted["a"] = g.now()
	if !g.TryAdmit("b") {
		t.Fatal("job a was counted twice: once by its pod and once by its admission")
	}
	if _, ok := g.admitted["a"]; ok {
		t.Error("admission for a job whose pod exists was kept")
	}
}

func TestTryAdmitExpiresStaleAdmissions(t *testing.T) {
	t.Parallel()
	g := newGate(t, node("n1", "4", "16Gi", true))
	start := time.Date(2026, 11, 14, 12, 0, 0, 0, time.UTC)
	g.now = func() time.Time { return start }
	if !g.TryAdmit("a") {
		t.Fatal("first job should fit")
	}
	if g.TryAdmit("b") {
		t.Fatal("second job admitted while the first admission was live")
	}
	g.now = func() time.Time { return start.Add(DefaultAdmissionTTL + time.Second) }
	if !g.TryAdmit("b") {
		t.Error("an expired admission still held capacity")
	}
}

// A pod that can never land on an eligible node must not hold the gate's
// capacity. Reported on buildkite/agent-stack-k8s#1015.
func TestTryAdmitIgnoresStuckPodsOutsideTheGate(t *testing.T) {
	t.Parallel()
	stuck := withNodeSelector(pod("stuck", "", "", corev1.PodPending, requests("16", "1Gi")), map[string]string{"gpu": "true"})
	stuck.Namespace = "other-team"
	g := newGate(t, node("n1", "16", "64Gi", true), stuck)
	admitted := 0
	for _, id := range []string{"j1", "j2", "j3", "j4", "j5", "j6"} {
		if g.TryAdmit(id) {
			admitted++
		}
	}
	if admitted != 5 {
		t.Errorf("an empty 16 CPU node admitted %d 3 CPU jobs, want 5", admitted)
	}
}

func TestRequestsFollowPodEvents(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	running := pod("p1", "n1", "a", corev1.PodRunning, requests("4", "4Gi"))
	g, client := newGateWithClient(t, node("n1", "8", "16Gi", true), running)
	pods := client.CoreV1().Pods("buildkite")
	waitForFreeCPU(t, g, "4")

	pending := pod("p2", "", "b", corev1.PodPending, requests("2", "1Gi"))
	if _, err := pods.Create(ctx, pending, metav1.CreateOptions{}); err != nil {
		t.Fatal(err)
	}
	waitForFreeCPU(t, g, "2")

	pending.Spec.NodeName = "n1"
	pending.Status.Phase = corev1.PodRunning
	if _, err := pods.Update(ctx, pending, metav1.UpdateOptions{}); err != nil {
		t.Fatal(err)
	}
	waitForFreeCPU(t, g, "2")

	running.Status.Phase = corev1.PodSucceeded
	if _, err := pods.Update(ctx, running, metav1.UpdateOptions{}); err != nil {
		t.Fatal(err)
	}
	waitForFreeCPU(t, g, "6")

	if err := pods.Delete(ctx, "p2", metav1.DeleteOptions{}); err != nil {
		t.Fatal(err)
	}
	waitForFreeCPU(t, g, "8")
}

func TestUpdatePodReportsFreedCapacity(t *testing.T) {
	t.Parallel()
	bound := pod("p", "n1", "a", corev1.PodRunning, requests("2", "1Gi"))
	unbound := pod("p", "", "a", corev1.PodPending, requests("2", "1Gi"))
	finished := pod("p", "n1", "a", corev1.PodSucceeded, requests("2", "1Gi"))
	unboundFailed := pod("p", "", "a", corev1.PodFailed, requests("2", "1Gi"))
	tests := []struct {
		name       string
		prev, curr *corev1.Pod
		want       bool
	}{
		{"status update on a bound pod", bound, bound.DeepCopy(), false},
		{"pod binds to a node", unbound, bound, false},
		{"bound pod finishes", bound, finished, true},
		{"bound pod is deleted", bound, nil, true},
		{"unbound job pod fails", unbound, unboundFailed, true},
		{"unbound job pod is deleted", unbound, nil, true},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			g := newTestGate(t)
			g.updatePod(nil, test.prev)
			if got := g.updatePod(test.prev, test.curr); got != test.want {
				t.Errorf("updatePod() freed = %v, want %v", got, test.want)
			}
		})
	}
}

func TestRegisterFailsWithoutClusterWideAccess(t *testing.T) {
	t.Parallel()
	client := fake.NewClientset()
	client.PrependReactor("list", "pods", func(k8stesting.Action) (bool, runtime.Object, error) {
		return true, nil, apierrors.NewForbidden(corev1.Resource("pods"), "", errors.New("RBAC"))
	})
	g := newTestGate(t)
	g.syncTimeout = 100 * time.Millisecond
	err := g.Register(t.Context(), informers.NewSharedInformerFactory(client, 0), func() {})
	if !errors.Is(err, ErrSyncTimeout) {
		t.Errorf("Register() error = %v, want %v", err, ErrSyncTimeout)
	}
}

// BenchmarkTryAdmit measures one admission check on a 100-node cluster.
// The cost should not grow with the number of pods.
func BenchmarkTryAdmit(b *testing.B) {
	for _, podCount := range []int{1_000, 10_000, 50_000} {
		b.Run(fmt.Sprintf("%d pods", podCount), func(b *testing.B) {
			objs := make([]runtime.Object, 0, 100+podCount)
			for i := range 100 {
				objs = append(objs, node(fmt.Sprintf("n%d", i), "1000", "4000Gi", true))
			}
			for i := range podCount {
				objs = append(objs, pod(fmt.Sprintf("p%d", i), fmt.Sprintf("n%d", i%100), "", corev1.PodRunning, requests("10m", "10Mi")))
			}
			g := newGate(b, objs...)
			b.ResetTimer()
			for b.Loop() {
				g.TryAdmit("job")
				g.Release("job")
			}
		})
	}
}

func TestPodRequests(t *testing.T) {
	t.Parallel()
	always := corev1.ContainerRestartPolicyAlways
	p := pod("p", "n1", "", corev1.PodRunning, requests("2", "4Gi"), requests("1", "1Gi"))
	p.Spec.InitContainers = []corev1.Container{
		{Resources: requests("500m", "3Gi"), RestartPolicy: &always},
		{Resources: requests("4", "1Gi")},
	}
	p.Spec.Overhead = corev1.ResourceList{corev1.ResourceCPU: resource.MustParse("100m")}

	cpu, memory := podRequests(p)
	// Long-running: 2 + 1 + 0.5 sidecar = 3.5 CPU. The 4 CPU init container
	// runs beside the 0.5 CPU sidecar started before it, which wins at 4.5.
	if want := resource.MustParse("4600m"); cpu.Cmp(want) != 0 {
		t.Errorf("cpu = %s, want %s", cpu.String(), want.String())
	}
	if want := resource.MustParse("8Gi"); memory.Cmp(want) != 0 {
		t.Errorf("memory = %s, want %s", memory.String(), want.String())
	}
}

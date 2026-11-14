package capacity

import (
	"log/slog"
	"testing"
	"time"

	"github.com/buildkite/agent-stack-k8s/v2/internal/controller/config"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/informers"
	"k8s.io/client-go/kubernetes/fake"
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

// newGate returns a Gate for 3 CPU / 4Gi pods over a fake cluster holding objs.
func newGate(t *testing.T, objs ...runtime.Object) *Gate {
	t.Helper()
	g, err := New(slog.Default(), &config.CapacityGateConfig{CPU: "3", Memory: "4Gi", NodeSelector: map[string]string{"pool": "ci"}})
	if err != nil {
		t.Fatal(err)
	}
	factory := informers.NewSharedInformerFactory(fake.NewClientset(objs...), 0)
	if err := g.Register(t.Context(), factory, func() {}); err != nil {
		t.Fatal(err)
	}
	return g
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
			name: "pods awaiting a node count against capacity",
			objs: []runtime.Object{node("n1", "8", "16Gi", true), pod("p1", "", "", corev1.PodPending, requests("6", "4Gi"))},
			want: false,
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

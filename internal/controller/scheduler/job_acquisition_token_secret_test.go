package scheduler

import (
	"errors"
	"log/slog"
	"strings"
	"testing"

	"github.com/buildkite/agent-stack-k8s/v2/api"
	"github.com/buildkite/agent-stack-k8s/v2/internal/controller/config"
	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/kubernetes/fake"
	k8stesting "k8s.io/client-go/testing"
	"sigs.k8s.io/yaml"
)

func TestCreateJobWithJobAcquisitionTokenSecret(t *testing.T) {
	k8sClient := fake.NewSimpleClientset()
	setCreatedJobUID(k8sClient)
	worker := newJobAcquisitionTokenWorker(k8sClient)
	kjob := buildJobWithJobAcquisitionToken(t, worker, "jat-secret")

	if err := worker.createJob(t.Context(), kjob, "jat-secret"); err != nil {
		t.Fatalf("createJob() error = %v", err)
	}

	secretName := kjob.Annotations[config.JobAcquisitionTokenSecretAnnotation]
	secret, err := k8sClient.CoreV1().Secrets("test-namespace").Get(t.Context(), secretName, metav1.GetOptions{})
	if err != nil {
		t.Fatalf("Get secret: %v", err)
	}
	if secret.Immutable == nil || !*secret.Immutable {
		t.Errorf("secret.Immutable = %v, want true", secret.Immutable)
	}
	if got := string(secret.Data[agentTokenKey]); got != "jat-secret" {
		t.Errorf("secret token = %q, want %q", got, "jat-secret")
	}
	if got := secret.Labels[config.UUIDLabel]; got != "job-uuid" {
		t.Errorf("secret job UUID label = %q, want %q", got, "job-uuid")
	}
	if got := secret.Labels[config.ControllerIDLabel]; got != "controller-id" {
		t.Errorf("secret controller ID label = %q, want %q", got, "controller-id")
	}
	if len(secret.OwnerReferences) != 1 || secret.OwnerReferences[0].Name != kjob.Name || secret.OwnerReferences[0].UID != "created-job-uid" {
		t.Errorf("secret owner references = %#v, want created Job", secret.OwnerReferences)
	}

	jobYAML, err := yaml.Marshal(kjob)
	if err != nil {
		t.Fatalf("yaml.Marshal(kjob) error = %v", err)
	}
	if strings.Contains(string(jobYAML), "jat-secret") {
		t.Errorf("Job contains literal JAT:\n%s", jobYAML)
	}
}

func TestCreateJobDoesNotCreateSecretWhenJobCreationFails(t *testing.T) {
	k8sClient := fake.NewSimpleClientset()
	k8sClient.PrependReactor("create", "jobs", func(k8stesting.Action) (bool, runtime.Object, error) {
		return true, nil, errors.New("job create failed")
	})
	worker := newJobAcquisitionTokenWorker(k8sClient)
	kjob := buildJobWithJobAcquisitionToken(t, worker, "jat-secret")

	if err := worker.createJob(t.Context(), kjob, "jat-secret"); err == nil {
		t.Fatal("createJob() error = nil, want error")
	}
	secrets, err := k8sClient.CoreV1().Secrets("test-namespace").List(t.Context(), metav1.ListOptions{})
	if err != nil {
		t.Fatalf("List secrets: %v", err)
	}
	if len(secrets.Items) != 0 {
		t.Errorf("len(secrets.Items) = %d, want 0", len(secrets.Items))
	}
}

func TestCreateJobCleansUpJobWhenSecretCreationFails(t *testing.T) {
	k8sClient := fake.NewSimpleClientset()
	k8sClient.PrependReactor("create", "secrets", func(k8stesting.Action) (bool, runtime.Object, error) {
		return true, nil, errors.New("secret create failed")
	})
	worker := newJobAcquisitionTokenWorker(k8sClient)
	kjob := buildJobWithJobAcquisitionToken(t, worker, "jat-secret")

	if err := worker.createJob(t.Context(), kjob, "jat-secret"); err == nil {
		t.Fatal("createJob() error = nil, want error")
	}
	jobs, err := k8sClient.BatchV1().Jobs("test-namespace").List(t.Context(), metav1.ListOptions{})
	if err != nil {
		t.Fatalf("List jobs: %v", err)
	}
	if len(jobs.Items) != 0 {
		t.Errorf("len(jobs.Items) = %d, want 0", len(jobs.Items))
	}
	secrets, err := k8sClient.CoreV1().Secrets("test-namespace").List(t.Context(), metav1.ListOptions{})
	if err != nil {
		t.Fatalf("List secrets: %v", err)
	}
	if len(secrets.Items) != 0 {
		t.Errorf("len(secrets.Items) = %d, want 0", len(secrets.Items))
	}
}

func TestCreateJobRetryDoesNotReplaceExistingJobSecret(t *testing.T) {
	k8sClient := fake.NewSimpleClientset()
	setCreatedJobUID(k8sClient)
	worker := newJobAcquisitionTokenWorker(k8sClient)
	firstJob := buildJobWithJobAcquisitionToken(t, worker, "first-jat")
	if err := worker.createJob(t.Context(), firstJob, "first-jat"); err != nil {
		t.Fatalf("first createJob() error = %v", err)
	}

	retryJob := buildJobWithJobAcquisitionToken(t, worker, "retry-jat")
	if err := worker.createJob(t.Context(), retryJob, "retry-jat"); err == nil {
		t.Fatal("retry createJob() error = nil, want duplicate error")
	}
	secrets, err := k8sClient.CoreV1().Secrets("test-namespace").List(t.Context(), metav1.ListOptions{})
	if err != nil {
		t.Fatalf("List secrets: %v", err)
	}
	if len(secrets.Items) != 1 {
		t.Fatalf("len(secrets.Items) = %d, want 1", len(secrets.Items))
	}
	if got := secrets.Items[0].Name; got != firstJob.Annotations[config.JobAcquisitionTokenSecretAnnotation] {
		t.Errorf("remaining secret = %q, want %q", got, firstJob.Annotations[config.JobAcquisitionTokenSecretAnnotation])
	}
	if got := string(secrets.Items[0].Data[agentTokenKey]); got != "first-jat" {
		t.Errorf("remaining token = %q, want %q", got, "first-jat")
	}
}

func TestCreateJobWithoutJobAcquisitionTokensUsesNoPerJobSecret(t *testing.T) {
	k8sClient := fake.NewSimpleClientset()
	worker := New(slog.Default(), k8sClient, nil, Config{Image: "buildkite/agent:latest", Namespace: "test-namespace"})
	kjob, err := worker.Build(&corev1.PodSpec{}, false, buildInputs{uuid: "job-uuid", envMap: map[string]string{}})
	if err != nil {
		t.Fatalf("Build() error = %v", err)
	}
	if err := worker.createJob(t.Context(), kjob, ""); err != nil {
		t.Fatalf("createJob() error = %v", err)
	}
	secrets, err := k8sClient.CoreV1().Secrets("test-namespace").List(t.Context(), metav1.ListOptions{})
	if err != nil {
		t.Fatalf("List secrets: %v", err)
	}
	if len(secrets.Items) != 0 {
		t.Errorf("len(secrets.Items) = %d, want 0", len(secrets.Items))
	}
}

func newJobAcquisitionTokenWorker(k8sClient *fake.Clientset) *worker {
	return New(slog.Default(), k8sClient, nil, Config{
		Image:                      "buildkite/agent:latest",
		Namespace:                  "test-namespace",
		ID:                         "controller-id",
		EnableJobAcquisitionTokens: true,
	})
}

func buildJobWithJobAcquisitionToken(t *testing.T, worker *worker, token api.JobAcquisitionToken) *batchv1.Job {
	t.Helper()
	inputs, err := worker.ParseJob(
		&api.AgentJob{ID: "job-uuid", Command: "true"},
		&api.AgentScheduledJob{ID: "job-uuid", JobAcquisitionToken: token},
	)
	if err != nil {
		t.Fatalf("ParseJob() error = %v", err)
	}
	kjob, err := worker.Build(&corev1.PodSpec{}, false, inputs)
	if err != nil {
		t.Fatalf("Build() error = %v", err)
	}
	return kjob
}

func setCreatedJobUID(k8sClient *fake.Clientset) {
	k8sClient.PrependReactor("create", "jobs", func(action k8stesting.Action) (bool, runtime.Object, error) {
		action.(k8stesting.CreateAction).GetObject().(*batchv1.Job).UID = types.UID("created-job-uid")
		return false, nil, nil
	})
}

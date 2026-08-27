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
	kerrors "k8s.io/apimachinery/pkg/api/errors"
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

func TestCreateJobRetryRecoversJobCreatedBeforeResponseWasLost(t *testing.T) {
	k8sClient := fake.NewSimpleClientset()
	createCalls := 0
	k8sClient.PrependReactor("create", "jobs", func(action k8stesting.Action) (bool, runtime.Object, error) {
		createCalls++
		if createCalls != 1 {
			return false, nil, nil
		}
		job := action.(k8stesting.CreateAction).GetObject().(*batchv1.Job).DeepCopy()
		job.Namespace = action.GetNamespace()
		job.UID = "accepted-job-uid"
		if err := k8sClient.Tracker().Create(batchv1.SchemeGroupVersion.WithResource("jobs"), job, job.Namespace); err != nil {
			t.Fatalf("Track accepted job: %v", err)
		}
		return true, nil, errors.New("job create response lost")
	})
	getCalls := 0
	k8sClient.PrependReactor("get", "jobs", func(k8stesting.Action) (bool, runtime.Object, error) {
		getCalls++
		if getCalls == 1 {
			return true, nil, errors.New("job lookup temporarily failed")
		}
		return false, nil, nil
	})
	worker := newJobAcquisitionTokenWorker(k8sClient)
	firstJob := buildJobWithJobAcquisitionToken(t, worker, "first-jat")
	firstSecretName := firstJob.Annotations[config.JobAcquisitionTokenSecretAnnotation]

	if err := worker.createJob(t.Context(), firstJob, "first-jat"); err == nil {
		t.Fatal("first createJob() error = nil, want ambiguous creation error")
	}

	retryJob := buildJobWithJobAcquisitionToken(t, worker, "retry-jat")
	if err := worker.createJob(t.Context(), retryJob, "retry-jat"); err != nil {
		t.Fatalf("retry createJob() error = %v", err)
	}
	acceptedJob, err := k8sClient.BatchV1().Jobs("test-namespace").Get(t.Context(), firstJob.Name, metav1.GetOptions{})
	if err != nil {
		t.Fatalf("Get accepted job: %v", err)
	}
	if got := acceptedJob.Annotations[config.JobAcquisitionTokenSecretAnnotation]; got != firstSecretName {
		t.Errorf("accepted Job secret annotation = %q, want %q", got, firstSecretName)
	}
	secret, err := k8sClient.CoreV1().Secrets("test-namespace").Get(t.Context(), firstSecretName, metav1.GetOptions{})
	if err != nil {
		t.Fatalf("Get reconciled secret: %v", err)
	}
	if got := string(secret.Data[agentTokenKey]); got != "retry-jat" {
		t.Errorf("reconciled secret token = %q, want retry-jat", got)
	}
	if len(secret.OwnerReferences) != 1 || secret.OwnerReferences[0].UID != acceptedJob.UID {
		t.Errorf("reconciled secret owner references = %#v, want accepted Job UID %q", secret.OwnerReferences, acceptedJob.UID)
	}
	if _, err := k8sClient.CoreV1().Secrets("test-namespace").Get(t.Context(), retryJob.Annotations[config.JobAcquisitionTokenSecretAnnotation], metav1.GetOptions{}); err == nil {
		t.Fatal("retry's newly generated Secret unexpectedly exists")
	}
}

func TestCreateJobLeavesAcceptedJobForRetryWhenSecretCreationFails(t *testing.T) {
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
	if len(jobs.Items) != 1 {
		t.Errorf("len(jobs.Items) = %d, want 1", len(jobs.Items))
	}
	secrets, err := k8sClient.CoreV1().Secrets("test-namespace").List(t.Context(), metav1.ListOptions{})
	if err != nil {
		t.Fatalf("List secrets: %v", err)
	}
	if len(secrets.Items) != 0 {
		t.Errorf("len(secrets.Items) = %d, want 0", len(secrets.Items))
	}
}

func TestCreateJobRetryReusesExistingSecretOwnedByAcceptedJob(t *testing.T) {
	k8sClient := fake.NewSimpleClientset()
	setCreatedJobUID(k8sClient)
	worker := newJobAcquisitionTokenWorker(k8sClient)
	firstJob := buildJobWithJobAcquisitionToken(t, worker, "first-jat")
	if err := worker.createJob(t.Context(), firstJob, "first-jat"); err != nil {
		t.Fatalf("first createJob() error = %v", err)
	}

	retryJob := buildJobWithJobAcquisitionToken(t, worker, "retry-jat")
	if err := worker.createJob(t.Context(), retryJob, "retry-jat"); err != nil {
		t.Fatalf("retry createJob() error = %v", err)
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

func TestCreateJobReconcilesSecretCreatedBeforeResponseWasLost(t *testing.T) {
	k8sClient := fake.NewSimpleClientset()
	setCreatedJobUID(k8sClient)
	k8sClient.PrependReactor("create", "secrets", func(action k8stesting.Action) (bool, runtime.Object, error) {
		secret := action.(k8stesting.CreateAction).GetObject().(*corev1.Secret).DeepCopy()
		secret.Namespace = action.GetNamespace()
		if err := k8sClient.Tracker().Create(corev1.SchemeGroupVersion.WithResource("secrets"), secret, secret.Namespace); err != nil {
			t.Fatalf("Track accepted secret: %v", err)
		}
		return true, nil, errors.New("secret create response lost")
	})
	worker := newJobAcquisitionTokenWorker(k8sClient)
	kjob := buildJobWithJobAcquisitionToken(t, worker, "jat-secret")

	if err := worker.createJob(t.Context(), kjob, "jat-secret"); err != nil {
		t.Fatalf("createJob() error = %v", err)
	}
}

func TestCreateJobDoesNotReconcileJobWithDifferentIdentity(t *testing.T) {
	tests := []struct {
		name  string
		label string
		value string
	}{
		{name: "Buildkite workload", label: config.UUIDLabel, value: "different-job-uuid"},
		{name: "controller", label: config.ControllerIDLabel, value: "different-controller"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			k8sClient := fake.NewSimpleClientset()
			worker := newJobAcquisitionTokenWorker(k8sClient)
			preexistingJob := buildJobWithJobAcquisitionToken(t, worker, "hostile-jat")
			preexistingJob.Namespace = "test-namespace"
			preexistingJob.UID = "preexisting-job-uid"
			preexistingJob.Labels[tt.label] = tt.value
			if _, err := k8sClient.BatchV1().Jobs("test-namespace").Create(t.Context(), preexistingJob, metav1.CreateOptions{}); err != nil {
				t.Fatalf("Create pre-existing job: %v", err)
			}
			retryJob := buildJobWithJobAcquisitionToken(t, worker, "retry-jat")

			if err := worker.createJob(t.Context(), retryJob, "retry-jat"); err == nil {
				t.Fatal("createJob() error = nil, want conflicting Job error")
			}
			if _, err := k8sClient.CoreV1().Secrets("test-namespace").Get(t.Context(), preexistingJob.Annotations[config.JobAcquisitionTokenSecretAnnotation], metav1.GetOptions{}); !kerrors.IsNotFound(err) {
				t.Fatalf("Get hostile Job secret error = %v, want NotFound", err)
			}
		})
	}
}

func TestCreateJobDoesNotReuseSecretOwnedByDifferentJob(t *testing.T) {
	k8sClient := fake.NewSimpleClientset()
	setCreatedJobUID(k8sClient)
	k8sClient.PrependReactor("create", "secrets", func(action k8stesting.Action) (bool, runtime.Object, error) {
		desired := action.(k8stesting.CreateAction).GetObject().(*corev1.Secret)
		foreign := desired.DeepCopy()
		foreign.Namespace = action.GetNamespace()
		foreign.Data[agentTokenKey] = []byte("foreign-jat")
		foreign.OwnerReferences[0].UID = "different-job-uid"
		if err := k8sClient.Tracker().Create(corev1.SchemeGroupVersion.WithResource("secrets"), foreign, foreign.Namespace); err != nil {
			t.Fatalf("Track foreign secret: %v", err)
		}
		return true, nil, kerrors.NewAlreadyExists(corev1.Resource("secrets"), desired.Name)
	})
	worker := newJobAcquisitionTokenWorker(k8sClient)
	kjob := buildJobWithJobAcquisitionToken(t, worker, "jat-secret")

	if err := worker.createJob(t.Context(), kjob, "jat-secret"); err == nil {
		t.Fatal("createJob() error = nil, want conflicting Secret error")
	}
	secretName := kjob.Annotations[config.JobAcquisitionTokenSecretAnnotation]
	secret, err := k8sClient.CoreV1().Secrets("test-namespace").Get(t.Context(), secretName, metav1.GetOptions{})
	if err != nil {
		t.Fatalf("Get foreign secret: %v", err)
	}
	if got := string(secret.Data[agentTokenKey]); got != "foreign-jat" {
		t.Errorf("foreign secret token = %q, want foreign-jat", got)
	}
	if got := secret.OwnerReferences[0].UID; got != "different-job-uid" {
		t.Errorf("foreign secret owner UID = %q, want different-job-uid", got)
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

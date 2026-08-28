package scheduler

import (
	"context"
	"errors"
	"log/slog"
	"strings"
	"testing"
	"time"

	"github.com/buildkite/agent-stack-k8s/v2/api"
	"github.com/buildkite/agent-stack-k8s/v2/internal/controller/config"
	"github.com/buildkite/agent-stack-k8s/v2/internal/controller/limiter"
	"github.com/buildkite/agent-stack-k8s/v2/internal/controller/model"
	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	kerrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/wait"
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

func TestCreateJobDoesNotCreateSecretWhenJobCreationHasDeterministicConflict(t *testing.T) {
	k8sClient := fake.NewSimpleClientset()
	k8sClient.PrependReactor("create", "jobs", func(k8stesting.Action) (bool, runtime.Object, error) {
		return true, nil, kerrors.NewConflict(batchv1.Resource("jobs"), "job-uuid", errors.New("conflict"))
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
	for _, action := range k8sClient.Actions() {
		if action.GetVerb() == "get" || action.GetVerb() == "delete" {
			t.Errorf("unexpected %s action after deterministic conflict: %v", action.GetVerb(), action)
		}
	}
}

type reportingHandler struct {
	handler model.JobHandler
	result  chan error
}

func (h *reportingHandler) Handle(ctx context.Context, job *api.AgentScheduledJob) error {
	err := h.handler.Handle(ctx, job)
	h.result <- err
	return err
}

func TestHandleAlreadyExistingJobReportsDuplicateWithoutTouchingExistingResources(t *testing.T) {
	k8sClient := fake.NewSimpleClientset()
	worker := newJobAcquisitionTokenWorker(k8sClient)
	existingJob := buildJobWithJobAcquisitionToken(t, worker, "original-jat")
	existingJob.Namespace = "test-namespace"
	existingJob.UID = "existing-job-uid"
	if _, err := k8sClient.BatchV1().Jobs("test-namespace").Create(t.Context(), existingJob, metav1.CreateOptions{}); err != nil {
		t.Fatalf("Create existing Job: %v", err)
	}
	immutable := true
	existingSecret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:            existingJob.Annotations[config.JobAcquisitionTokenSecretAnnotation],
			Namespace:       existingJob.Namespace,
			UID:             "existing-secret-uid",
			Labels:          existingJob.Labels,
			OwnerReferences: []metav1.OwnerReference{*metav1.NewControllerRef(existingJob, batchv1.SchemeGroupVersion.WithKind("Job"))},
		},
		Immutable: &immutable,
		Data:      map[string][]byte{agentTokenKey: []byte("original-jat")},
	}
	if _, err := k8sClient.CoreV1().Secrets("test-namespace").Create(t.Context(), existingSecret, metav1.CreateOptions{}); err != nil {
		t.Fatalf("Create existing Secret: %v", err)
	}

	server := api.NewFakeAgentServer()
	defer server.Close()
	server.AgentJobs = map[string]*api.AgentJob{"job-uuid": {ID: "job-uuid", Command: "true"}}
	agentClient, err := api.NewAgentClient(t.Context(), api.AgentClientOpts{
		Token: "fake-token", Endpoint: server.URL(), StackID: "test-stack", Logger: slog.Default(),
	})
	if err != nil {
		t.Fatalf("NewAgentClient() error = %v", err)
	}
	worker = New(slog.Default(), k8sClient, agentClient, worker.cfg)
	reporter := &reportingHandler{handler: worker, result: make(chan error, 1)}
	ctx, cancel := context.WithCancel(t.Context())
	lim := limiter.New(ctx, slog.Default(), reporter, 1, 1, 10)
	defer func() {
		cancel()
		lim.Wait()
	}()
	actionCount := len(k8sClient.Actions())
	if err := lim.HandleMany(ctx, []*api.AgentScheduledJob{{ID: "job-uuid", JobAcquisitionToken: "new-jat", QueriedAt: time.Now()}}); err != nil {
		t.Fatalf("HandleMany() error = %v", err)
	}
	select {
	case err := <-reporter.result:
		if !errors.Is(err, model.ErrDuplicateJob) {
			t.Fatalf("Handle() error = %v, want ErrDuplicateJob", err)
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for handler result")
	}
	handlerActions := append([]k8stesting.Action(nil), k8sClient.Actions()[actionCount:]...)

	job, err := k8sClient.BatchV1().Jobs("test-namespace").Get(t.Context(), existingJob.Name, metav1.GetOptions{})
	if err != nil {
		t.Fatalf("Get existing Job: %v", err)
	}
	secret, err := k8sClient.CoreV1().Secrets("test-namespace").Get(t.Context(), existingSecret.Name, metav1.GetOptions{})
	if err != nil {
		t.Fatalf("Get existing Secret: %v", err)
	}
	if job.UID != existingJob.UID || secret.UID != existingSecret.UID || string(secret.Data[agentTokenKey]) != "original-jat" {
		t.Fatalf("existing resources changed: Job UID %q, Secret UID %q, token %q", job.UID, secret.UID, secret.Data[agentTokenKey])
	}
	for _, action := range handlerActions {
		if action.GetResource().Resource == "secrets" && (action.GetVerb() == "create" || action.GetVerb() == "delete") {
			t.Errorf("unexpected Secret %s after AlreadyExists", action.GetVerb())
		}
		if action.GetResource().Resource == "jobs" && (action.GetVerb() == "get" || action.GetVerb() == "delete") {
			t.Errorf("unexpected Job %s after AlreadyExists", action.GetVerb())
		}
	}
}

func TestCreateJobDoesNotRetryPermanentJobCreationError(t *testing.T) {
	k8sClient := fake.NewSimpleClientset()
	createCalls := 0
	k8sClient.PrependReactor("create", "jobs", func(k8stesting.Action) (bool, runtime.Object, error) {
		createCalls++
		return true, nil, kerrors.NewForbidden(batchv1.Resource("jobs"), "job-uuid", errors.New("forbidden"))
	})
	worker := newJobAcquisitionTokenWorker(k8sClient)
	kjob := buildJobWithJobAcquisitionToken(t, worker, "jat-secret")

	if err := worker.createJob(t.Context(), kjob, "jat-secret"); !kerrors.IsForbidden(err) {
		t.Fatalf("createJob() error = %v, want Forbidden", err)
	}
	if createCalls != 1 {
		t.Errorf("Job create calls = %d, want 1", createCalls)
	}
	for _, action := range k8sClient.Actions() {
		if action.GetVerb() == "get" || action.GetVerb() == "delete" {
			t.Errorf("unexpected %s action after permanent error: %v", action.GetVerb(), action)
		}
	}
}

func TestHandleReconcilesJobCreatedBeforeResponseWasLost(t *testing.T) {
	k8sClient := fake.NewSimpleClientset()
	ctx, cancel := context.WithCancel(t.Context())
	k8sClient.PrependReactor("create", "jobs", func(action k8stesting.Action) (bool, runtime.Object, error) {
		job := action.(k8stesting.CreateAction).GetObject().(*batchv1.Job).DeepCopy()
		job.Namespace = action.GetNamespace()
		job.UID = "accepted-job-uid"
		if err := k8sClient.Tracker().Create(batchv1.SchemeGroupVersion.WithResource("jobs"), job, job.Namespace); err != nil {
			t.Fatalf("Track accepted job: %v", err)
		}
		cancel()
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
	server := api.NewFakeAgentServer()
	defer server.Close()
	server.AgentJobs = map[string]*api.AgentJob{"job-uuid": {ID: "job-uuid", Command: "true"}}
	agentClient, err := api.NewAgentClient(t.Context(), api.AgentClientOpts{
		Token: "fake-token", Endpoint: server.URL(), StackID: "test-stack", Logger: slog.Default(),
	})
	if err != nil {
		t.Fatalf("NewAgentClient() error = %v", err)
	}
	worker := New(slog.Default(), k8sClient, agentClient, Config{
		Image: "buildkite/agent:latest", Namespace: "test-namespace", ID: "controller-id", EnableJobAcquisitionTokens: true,
	})
	if err := worker.Handle(ctx, &api.AgentScheduledJob{ID: "job-uuid", JobAcquisitionToken: "jat-secret", QueriedAt: time.Now()}); err != nil {
		t.Fatalf("Handle() error = %v", err)
	}

	var secret *corev1.Secret
	err = wait.PollUntilContextTimeout(t.Context(), 10*time.Millisecond, time.Second, true, func(ctx context.Context) (bool, error) {
		secrets, err := k8sClient.CoreV1().Secrets("test-namespace").List(ctx, metav1.ListOptions{})
		if err != nil || len(secrets.Items) == 0 {
			return false, err
		}
		secret = &secrets.Items[0]
		return true, nil
	})
	if err != nil {
		t.Fatalf("waiting for reconciled secret: %v", err)
	}
	acceptedJob, err := k8sClient.BatchV1().Jobs("test-namespace").Get(t.Context(), "job-uuid", metav1.GetOptions{})
	if err != nil {
		t.Fatalf("Get accepted job: %v", err)
	}
	if len(secret.OwnerReferences) != 1 || secret.OwnerReferences[0].UID != acceptedJob.UID {
		t.Errorf("reconciled secret owner references = %#v, want accepted Job UID %q", secret.OwnerReferences, acceptedJob.UID)
	}
	if got := string(secret.Data[agentTokenKey]); got != "jat-secret" {
		t.Errorf("reconciled secret token = %q, want jat-secret", got)
	}
}

func TestCreateJobRecoversVerifiedJobForCleanupAfterLookupRetriesExhausted(t *testing.T) {
	k8sClient := fake.NewSimpleClientset()
	k8sClient.PrependReactor("create", "jobs", func(action k8stesting.Action) (bool, runtime.Object, error) {
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
		if getCalls <= resourceReconciliationBackoff.Steps {
			return true, nil, kerrors.NewServerTimeout(batchv1.Resource("jobs"), "get", 0)
		}
		return false, nil, nil
	})
	worker := newJobAcquisitionTokenWorker(k8sClient)
	kjob := buildJobWithJobAcquisitionToken(t, worker, "jat-secret")

	if err := worker.createJob(t.Context(), kjob, "jat-secret"); err == nil {
		t.Fatal("createJob() error = nil, want reconciliation error")
	}
	if getCalls <= resourceReconciliationBackoff.Steps {
		t.Fatalf("Job get calls = %d, want bounded recovery after %d normal attempts", getCalls, resourceReconciliationBackoff.Steps)
	}
	if _, err := k8sClient.BatchV1().Jobs("test-namespace").Get(t.Context(), kjob.Name, metav1.GetOptions{}); !kerrors.IsNotFound(err) {
		t.Fatalf("Get cleaned Job error = %v, want NotFound", err)
	}
	for _, action := range k8sClient.Actions() {
		deleteAction, ok := action.(k8stesting.DeleteAction)
		if !ok || action.GetResource().Resource != "jobs" {
			continue
		}
		preconditions := deleteAction.GetDeleteOptions().Preconditions
		if preconditions == nil || preconditions.UID == nil || *preconditions.UID != "accepted-job-uid" {
			t.Errorf("Job delete UID precondition = %v, want accepted-job-uid", preconditions)
		}
	}
}

func TestCreateJobDoesNotDeleteUnverifiedJobWhenRecoveryExhausted(t *testing.T) {
	k8sClient := fake.NewSimpleClientset()
	k8sClient.PrependReactor("create", "jobs", func(action k8stesting.Action) (bool, runtime.Object, error) {
		job := action.(k8stesting.CreateAction).GetObject().(*batchv1.Job).DeepCopy()
		job.Namespace = action.GetNamespace()
		job.UID = "accepted-job-uid"
		if err := k8sClient.Tracker().Create(batchv1.SchemeGroupVersion.WithResource("jobs"), job, job.Namespace); err != nil {
			t.Fatalf("Track accepted job: %v", err)
		}
		return true, nil, errors.New("job create response lost")
	})
	k8sClient.PrependReactor("get", "jobs", func(k8stesting.Action) (bool, runtime.Object, error) {
		return true, nil, kerrors.NewServerTimeout(batchv1.Resource("jobs"), "get", 0)
	})
	worker := newJobAcquisitionTokenWorker(k8sClient)
	kjob := buildJobWithJobAcquisitionToken(t, worker, "jat-secret")

	if err := worker.createJob(t.Context(), kjob, "jat-secret"); err == nil || !strings.Contains(err.Error(), "failed to recover job for cleanup") {
		t.Fatalf("createJob() error = %v, want exhausted recovery error", err)
	}
	if _, err := k8sClient.Tracker().Get(batchv1.SchemeGroupVersion.WithResource("jobs"), "test-namespace", kjob.Name); err != nil {
		t.Fatalf("accepted unverified Job was removed: %v", err)
	}
	for _, action := range k8sClient.Actions() {
		if action.GetVerb() == "delete" {
			t.Errorf("unexpected unverified %s delete", action.GetResource().Resource)
		}
	}
}

func TestCreateJobExhaustedSecretReconciliationCleansVerifiedJobAndOwnedSecret(t *testing.T) {
	k8sClient := fake.NewSimpleClientset()
	setCreatedJobUID(k8sClient)
	createCalls := 0
	k8sClient.PrependReactor("create", "secrets", func(action k8stesting.Action) (bool, runtime.Object, error) {
		createCalls++
		if createCalls != 1 {
			return false, nil, nil
		}
		secret := action.(k8stesting.CreateAction).GetObject().(*corev1.Secret).DeepCopy()
		secret.Namespace = action.GetNamespace()
		secret.UID = "accepted-secret-uid"
		if err := k8sClient.Tracker().Create(corev1.SchemeGroupVersion.WithResource("secrets"), secret, secret.Namespace); err != nil {
			t.Fatalf("Track accepted secret: %v", err)
		}
		return true, nil, errors.New("secret create response lost")
	})
	getCalls := 0
	k8sClient.PrependReactor("get", "secrets", func(k8stesting.Action) (bool, runtime.Object, error) {
		getCalls++
		if getCalls <= resourceReconciliationBackoff.Steps {
			return true, nil, kerrors.NewServerTimeout(corev1.Resource("secrets"), "get", 0)
		}
		return false, nil, nil
	})
	worker := newJobAcquisitionTokenWorker(k8sClient)
	kjob := buildJobWithJobAcquisitionToken(t, worker, "jat-secret")

	if err := worker.createJob(t.Context(), kjob, "jat-secret"); err == nil {
		t.Fatal("createJob() error = nil, want error")
	}
	if _, err := k8sClient.BatchV1().Jobs("test-namespace").Get(t.Context(), kjob.Name, metav1.GetOptions{}); !kerrors.IsNotFound(err) {
		t.Fatalf("Get cleaned Job error = %v, want NotFound", err)
	}
	secretName := kjob.Annotations[config.JobAcquisitionTokenSecretAnnotation]
	if _, err := k8sClient.CoreV1().Secrets("test-namespace").Get(t.Context(), secretName, metav1.GetOptions{}); !kerrors.IsNotFound(err) {
		t.Fatalf("Get cleaned Secret error = %v, want NotFound", err)
	}
	deleteCounts := map[string]int{}
	for _, action := range k8sClient.Actions() {
		deleteAction, ok := action.(k8stesting.DeleteAction)
		if !ok {
			continue
		}
		deleteCounts[action.GetResource().Resource]++
		preconditions := deleteAction.GetDeleteOptions().Preconditions
		if action.GetResource().Resource == "jobs" && (preconditions == nil || preconditions.UID == nil || *preconditions.UID != "created-job-uid") {
			t.Errorf("Job delete UID precondition = %v, want created-job-uid", preconditions)
		}
		if action.GetResource().Resource == "secrets" && (preconditions == nil || preconditions.UID == nil || *preconditions.UID != "accepted-secret-uid") {
			t.Errorf("Secret delete UID precondition = %v, want accepted-secret-uid", preconditions)
		}
	}
	if deleteCounts["jobs"] != 1 || deleteCounts["secrets"] != 1 {
		t.Errorf("delete counts = %v, want one Job and one Secret", deleteCounts)
	}
}

func TestCreateJobReconcilesTransientSecretCreateAndGetAmbiguity(t *testing.T) {
	k8sClient := fake.NewSimpleClientset()
	setCreatedJobUID(k8sClient)
	createCalls := 0
	k8sClient.PrependReactor("create", "secrets", func(action k8stesting.Action) (bool, runtime.Object, error) {
		createCalls++
		if createCalls != 1 {
			return false, nil, nil
		}
		secret := action.(k8stesting.CreateAction).GetObject().(*corev1.Secret).DeepCopy()
		secret.Namespace = action.GetNamespace()
		if err := k8sClient.Tracker().Create(corev1.SchemeGroupVersion.WithResource("secrets"), secret, secret.Namespace); err != nil {
			t.Fatalf("Track accepted secret: %v", err)
		}
		return true, nil, errors.New("secret create response lost")
	})
	getCalls := 0
	k8sClient.PrependReactor("get", "secrets", func(k8stesting.Action) (bool, runtime.Object, error) {
		getCalls++
		if getCalls == 1 {
			return true, nil, kerrors.NewServerTimeout(corev1.Resource("secrets"), "get", 0)
		}
		return false, nil, nil
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
			k8sClient.PrependReactor("create", "jobs", func(k8stesting.Action) (bool, runtime.Object, error) {
				if err := k8sClient.Tracker().Create(batchv1.SchemeGroupVersion.WithResource("jobs"), preexistingJob, preexistingJob.Namespace); err != nil {
					t.Fatalf("Track conflicting job: %v", err)
				}
				return true, nil, errors.New("job create response lost")
			})
			retryJob := buildJobWithJobAcquisitionToken(t, worker, "retry-jat")

			if err := worker.createJob(t.Context(), retryJob, "retry-jat"); err == nil {
				t.Fatal("createJob() error = nil, want conflicting Job error")
			}
			if _, err := k8sClient.BatchV1().Jobs("test-namespace").Get(t.Context(), preexistingJob.Name, metav1.GetOptions{}); err != nil {
				t.Fatalf("Get pre-existing Job after conflict: %v", err)
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
	if _, err := k8sClient.BatchV1().Jobs("test-namespace").Get(t.Context(), kjob.Name, metav1.GetOptions{}); !kerrors.IsNotFound(err) {
		t.Fatalf("Get cleaned Job error = %v, want NotFound", err)
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

package controller

import (
	"testing"

	"github.com/buildkite/agent-stack-k8s/v2/internal/controller/config"
	batchv1 "k8s.io/api/batch/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func TestInformerTransformPreservesJobAcquisitionTokenSecretAnnotation(t *testing.T) {
	kjob := &batchv1.Job{ObjectMeta: metav1.ObjectMeta{Annotations: map[string]string{
		config.JobAcquisitionTokenSecretAnnotation: "job-token",
		"unneeded": "annotation",
	}}}

	transformed, err := informerTransform(kjob)
	if err != nil {
		t.Fatalf("informerTransform() error = %v", err)
	}
	annotations := transformed.(*batchv1.Job).Annotations
	if got := annotations[config.JobAcquisitionTokenSecretAnnotation]; got != "job-token" {
		t.Errorf("job acquisition token secret annotation = %q, want %q", got, "job-token")
	}
	if _, ok := annotations["unneeded"]; ok {
		t.Error("unneeded annotation was preserved")
	}
}

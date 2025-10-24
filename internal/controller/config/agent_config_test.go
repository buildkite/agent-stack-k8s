package config

import (
	"sort"
	"testing"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/utils/ptr"
)

func TestApplyToAgentStartDoesNotDuplicateHooksPath(t *testing.T) {
	existingDefault := corev1.EnvVar{Name: "BUILDKITE_HOOKS_PATH", Value: "/workspace/hooks"}

	tests := []struct {
		name      string
		initial   []corev1.EnvVar
		hooksPath string
		wantEnv   []corev1.EnvVar
	}{
		{
			name:      "existingMatchingValue",
			initial:   []corev1.EnvVar{existingDefault},
			hooksPath: "/workspace/hooks",
			wantEnv:   []corev1.EnvVar{existingDefault},
		},
		{
			name: "existingDifferentValue",
			initial: []corev1.EnvVar{{
				Name:  "BUILDKITE_HOOKS_PATH",
				Value: "/other",
			}},
			hooksPath: "/workspace/hooks",
			wantEnv:   []corev1.EnvVar{existingDefault},
		},
		{
			name:      "noExistingValue",
			initial:   nil,
			hooksPath: "/workspace/hooks",
			wantEnv:   []corev1.EnvVar{existingDefault},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			ctr := &corev1.Container{Env: append([]corev1.EnvVar(nil), tc.initial...)}
			agentCfg := &AgentConfig{
				HooksVolume: &corev1.Volume{Name: "hooks"},
				HooksPath:   ptr.To(tc.hooksPath),
			}

			agentCfg.ApplyToAgentStart(ctr)

			// The helper may append other env vars (built by AgentConfig), so compare
			// the subset matching our expectation.
			var got []corev1.EnvVar
			for _, env := range ctr.Env {
				if env.Name == "BUILDKITE_HOOKS_PATH" {
					got = append(got, env)
				}
			}

			if len(got) != len(tc.wantEnv) {
				t.Fatalf("expected %d hooks env values, got %d: %v", len(tc.wantEnv), len(got), got)
			}

			sort.SliceStable(got, func(i, j int) bool { return got[i].Value < got[j].Value })
			sort.SliceStable(tc.wantEnv, func(i, j int) bool { return tc.wantEnv[i].Value < tc.wantEnv[j].Value })

			for i := range tc.wantEnv {
				if got[i] != tc.wantEnv[i] {
					t.Fatalf("env mismatch at %d: got %v want %v", i, got[i], tc.wantEnv[i])
				}
			}
		})
	}
}

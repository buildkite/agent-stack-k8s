package controller_test

import (
	"testing"

	"github.com/buildkite/agent-stack-k8s/v2/internal/controller/config"
	"github.com/mitchellh/mapstructure"
	"github.com/spf13/viper"
	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"
)

func TestParseConfig(t *testing.T) {
	viper.SetConfigFile("fixtures/config.yaml")

	expected := config.Config{
		AgentTokenSecret: "agent-token",
		BuildkiteToken:   "graphql-token",
		Image:            "ghcr.io/buildkite/agent-stack-k8s/agent:latest",
		Org:              "my-org",
		Tags:             []string{"queue=kubernetes"},
		PodSpecPatch: &corev1.PodSpec{
			Containers: []corev1.Container{
				{
					Name: "checkout",
					EnvFrom: []corev1.EnvFromSource{
						{
							SecretRef: &corev1.SecretEnvSource{
								LocalObjectReference: corev1.LocalObjectReference{
									Name: "git-checkout",
								},
							},
						},
					},
				},
			},
		},
	}

	var actual config.Config
	require.NoError(t, viper.ReadInConfig())
	require.NoError(t, viper.Unmarshal(&actual, func(c *mapstructure.DecoderConfig) {
		c.TagName = "json"
	}))
	require.Equal(t, actual, expected)
}

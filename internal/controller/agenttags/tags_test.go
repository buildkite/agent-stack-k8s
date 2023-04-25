package agenttags_test

import (
	"errors"
	"fmt"
	"testing"

	"github.com/buildkite/agent-stack-k8s/v2/internal/controller/agenttags"
	"github.com/stretchr/testify/assert"
)

func TestToMap(t *testing.T) {
	t.Parallel()

	for i, test := range []struct {
		agentTags    []string
		expectedMap  map[string]string
		expectedErrs []error
	}{
		{
			agentTags:   []string{},
			expectedMap: map[string]string{},
		},
		{
			agentTags: []string{"queue=kubernetes"},
			expectedMap: map[string]string{
				"queue": "kubernetes",
			},
		},
		{
			agentTags: []string{"queue=kubernetes", "arch=arm64"},
			expectedMap: map[string]string{
				"queue": "kubernetes",
				"arch":  "arm64",
			},
		},
		{
			agentTags: []string{"arch=arm64", "queue=kubernetes"},
			expectedMap: map[string]string{
				"queue": "kubernetes",
				"arch":  "arm64",
			},
		},
		{
			agentTags: []string{"queue=kubernetes=2"},
			expectedMap: map[string]string{
				"queue": "kubernetes=2",
			},
		},
		{
			agentTags:    []string{"kubernetes"},
			expectedMap:  map[string]string{},
			expectedErrs: []error{errors.New(`invalid agent tag: "kubernetes"`)},
		},
		{
			agentTags: []string{"kubernetes", "arch=arm64"},
			expectedMap: map[string]string{
				"arch": "arm64",
			},
			expectedErrs: []error{errors.New(`invalid agent tag: "kubernetes"`)},
		},
	} {
		test := test
		t.Run(fmt.Sprintf("%d", i), func(t *testing.T) {
			t.Parallel()
			m, errs := agenttags.ToMap(test.agentTags)
			if test.expectedErrs != nil {
				assert.Equal(t, test.expectedErrs, errs)
			}
			assert.Equal(t, test.expectedMap, m)
		})
	}

}

func TestToLabels(t *testing.T) {
	t.Parallel()

	const invalidLabelErrMsg = "a valid label must be an empty string or consist of alphanumeric characters, '-', '_' or '.', and must start and end with an alphanumeric character (e.g. 'MyValue',  or 'my_value',  or '12345', regex used for validation is '(([A-Za-z0-9][-A-Za-z0-9_.]*)?[A-Za-z0-9])?')"

	for i, test := range []struct {
		agentTags      []string
		expectedLabels map[string]string
		expectedErrs   []error
	}{
		{
			agentTags:      []string{},
			expectedLabels: map[string]string{},
		},
		{
			agentTags: []string{"queue=kubernetes"},
			expectedLabels: map[string]string{
				"buildkite.com/queue": "kubernetes",
			},
		},
		{
			agentTags: []string{"queue=kubernetes", "arch=arm64"},
			expectedLabels: map[string]string{
				"buildkite.com/queue": "kubernetes",
				"buildkite.com/arch":  "arm64",
			},
		},
		{
			agentTags: []string{"arch=arm64", "queue=kubernetes"},
			expectedLabels: map[string]string{
				"buildkite.com/queue": "kubernetes",
				"buildkite.com/arch":  "arm64",
			},
		},
		{
			agentTags:      []string{"queue=kubernetes=2"},
			expectedLabels: map[string]string{},
			expectedErrs:   []error{errors.New(invalidLabelErrMsg)},
		},
		{
			agentTags: []string{"queue="},
			expectedLabels: map[string]string{
				"buildkite.com/queue": "",
			},
		},
	} {
		test := test
		t.Run(fmt.Sprintf("%d", i), func(t *testing.T) {
			t.Parallel()
			labels, errs := agenttags.ToLabels(test.agentTags)
			if test.expectedErrs != nil {
				assert.Equal(t, test.expectedErrs, errs)
			}
			assert.Equal(t, test.expectedLabels, labels)
		})
	}
}

package agenttags_test

import (
	"errors"
	"fmt"
	"maps"
	"testing"

	"github.com/buildkite/agent-stack-k8s/v2/internal/controller/agenttags"
	"github.com/stretchr/testify/assert"
)

func TestMapFromTags(t *testing.T) {
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
			m, errs := agenttags.TagMapFromTags(test.agentTags)
			if test.expectedErrs != nil {
				assert.Equal(t, test.expectedErrs, errs)
			}
			assert.Equal(t, test.expectedMap, m)
		})
	}
}

func TestLabelsFromTags(t *testing.T) {
	t.Parallel()

	const invalidLabelErrMsg = "a valid label must be an empty string or consist of alphanumeric characters, '-', '_' or '.', and must start and end with an alphanumeric character (e.g. 'MyValue',  or 'my_value',  or '12345', regex used for validation is '(([A-Za-z0-9][-A-Za-z0-9_.]*)?[A-Za-z0-9])?')"

	for _, test := range []struct {
		name           string
		agentTags      []string
		expectedLabels map[string]string
		expectedErrs   []error
	}{
		{
			name:           "empty tags",
			agentTags:      []string{},
			expectedLabels: map[string]string{},
		},
		{
			name:      "valid queue",
			agentTags: []string{"queue=kubernetes"},
			expectedLabels: map[string]string{
				"tag.buildkite.com/queue": "kubernetes",
			},
		},
		{
			name:      "valid queue and arch",
			agentTags: []string{"queue=kubernetes", "arch=arm64"},
			expectedLabels: map[string]string{
				"tag.buildkite.com/queue": "kubernetes",
				"tag.buildkite.com/arch":  "arm64",
			},
		},
		{
			name:      "valid queue and arch (swapped order)",
			agentTags: []string{"arch=arm64", "queue=kubernetes"},
			expectedLabels: map[string]string{
				"tag.buildkite.com/queue": "kubernetes",
				"tag.buildkite.com/arch":  "arm64",
			},
		},
		{
			name:           "k8s rejects value",
			agentTags:      []string{"queue=kubernetes=2"},
			expectedLabels: map[string]string{},
			expectedErrs:   []error{errors.New(invalidLabelErrMsg)},
		},
		{
			name:      "empty value",
			agentTags: []string{"queue="},
			expectedLabels: map[string]string{
				"tag.buildkite.com/queue": "",
			},
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			labels, errs := agenttags.LabelsFromTags(test.agentTags)
			if test.expectedErrs != nil {
				assert.Equal(t, test.expectedErrs, errs)
			}
			assert.Equal(t, test.expectedLabels, labels)
		})
	}
}

func TestJobTagsMatchAgentTags(t *testing.T) {
	t.Parallel()

	for i, test := range []struct {
		jobTags        map[string]string
		agentTags      map[string]string
		expectedResult bool
	}{
		{
			jobTags:        map[string]string{},
			agentTags:      map[string]string{},
			expectedResult: true,
		},
		{
			jobTags:        map[string]string{"a": "x"},
			agentTags:      map[string]string{},
			expectedResult: false,
		},
		{
			jobTags:        map[string]string{},
			agentTags:      map[string]string{"a": "x"},
			expectedResult: true,
		},
		{
			jobTags:        map[string]string{"a": "x"},
			agentTags:      map[string]string{"a": "x"},
			expectedResult: true,
		},
		{
			jobTags:        map[string]string{"a": "x"},
			agentTags:      map[string]string{"b": "y"},
			expectedResult: false,
		},
		{
			jobTags:        map[string]string{"a": "x"},
			agentTags:      map[string]string{"a": "x", "b": "y"},
			expectedResult: true,
		},
		{
			jobTags:        map[string]string{"a": "x", "b": "y"},
			agentTags:      map[string]string{"a": "x"},
			expectedResult: false,
		},
		{
			jobTags:        map[string]string{"a": "x"},
			agentTags:      map[string]string{"a": "y"},
			expectedResult: false,
		},
		{
			jobTags:        map[string]string{"a": "*"},
			agentTags:      map[string]string{"a": "x"},
			expectedResult: true,
		},
		{
			jobTags:        map[string]string{"a": "x", "b": "*"},
			agentTags:      map[string]string{"a": "x"},
			expectedResult: false,
		},
		{
			jobTags:        map[string]string{"a": "x", "b": "*"},
			agentTags:      map[string]string{"a": "x", "b": "y"},
			expectedResult: true,
		},
	} {
		test := test

		t.Run(fmt.Sprintf("%d", i), func(t *testing.T) {
			t.Parallel()
			actualResult := agenttags.JobTagsMatchAgentTags(maps.All(test.jobTags), test.agentTags)
			assert.Equal(
				t,
				test.expectedResult,
				actualResult,
				"expected jobTags %+v to match agentTags %+v",
				test.jobTags,
				test.agentTags,
			)
		})
	}
}

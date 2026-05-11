package agenttags

import (
	"errors"
	"fmt"
	"testing"

	"github.com/google/go-cmp/cmp"
	"github.com/google/go-cmp/cmp/cmpopts"
)

// This isn't great, but is needed in one case where we aren't the source of the
// error.
var equateErrorMessages = cmp.Transformer("equateErrorMessages", func(err error) string {
	if err == nil {
		return "<nil>"
	}
	return err.Error()
})

func TestMapFromTags(t *testing.T) {
	t.Parallel()

	for i, test := range []struct {
		agentTags []string
		wantMap   map[string]string
		wantErrs  []error
	}{
		{
			agentTags: []string{},
			wantMap:   map[string]string{},
		},
		{
			agentTags: []string{"queue=kubernetes"},
			wantMap: map[string]string{
				"queue": "kubernetes",
			},
		},
		{
			agentTags: []string{"queue=kubernetes", "arch=arm64"},
			wantMap: map[string]string{
				"queue": "kubernetes",
				"arch":  "arm64",
			},
		},
		{
			agentTags: []string{"arch=arm64", "queue=kubernetes"},
			wantMap: map[string]string{
				"queue": "kubernetes",
				"arch":  "arm64",
			},
		},
		{
			agentTags: []string{"queue=kubernetes=2"},
			wantMap: map[string]string{
				"queue": "kubernetes=2",
			},
		},
		{
			agentTags: []string{"kubernetes"},
			wantMap:   map[string]string{},
			wantErrs:  []error{invalidTagError{"kubernetes"}},
		},
		{
			agentTags: []string{"kubernetes", "arch=arm64"},
			wantMap: map[string]string{
				"arch": "arm64",
			},
			wantErrs: []error{invalidTagError{"kubernetes"}},
		},
	} {
		t.Run(fmt.Sprintf("%d", i), func(t *testing.T) {
			t.Parallel()
			m, errs := TagMapFromTags(test.agentTags)
			if diff := cmp.Diff(errs, test.wantErrs, cmpopts.EquateErrors()); diff != "" {
				t.Errorf("TagMapFromTags(%+v) errors diff (-got +want):\n%s", test.agentTags, diff)
			}
			if diff := cmp.Diff(m, test.wantMap); diff != "" {
				t.Errorf("TagMapFromTags(%+v) map diff (-got +want):\n%s", test.agentTags, diff)
			}
		})
	}

}

func TestLabelsFromTags(t *testing.T) {
	t.Parallel()

	const invalidLabelErrMsg = "a valid label must be an empty string or consist of alphanumeric characters, '-', '_' or '.', and must start and end with an alphanumeric character (e.g. 'MyValue',  or 'my_value',  or '12345', regex used for validation is '(([A-Za-z0-9][-A-Za-z0-9_.]*)?[A-Za-z0-9])?')"

	for _, test := range []struct {
		name       string
		agentTags  []string
		wantLabels map[string]string
		wantErrs   []error
	}{
		{
			name:       "empty tags",
			agentTags:  []string{},
			wantLabels: map[string]string{},
		},
		{
			name:      "valid queue",
			agentTags: []string{"queue=kubernetes"},
			wantLabels: map[string]string{
				"tag.buildkite.com/queue": "kubernetes",
			},
		},
		{
			name:      "valid queue and arch",
			agentTags: []string{"queue=kubernetes", "arch=arm64"},
			wantLabels: map[string]string{
				"tag.buildkite.com/queue": "kubernetes",
				"tag.buildkite.com/arch":  "arm64",
			},
		},
		{
			name:      "valid queue and arch (swapped order)",
			agentTags: []string{"arch=arm64", "queue=kubernetes"},
			wantLabels: map[string]string{
				"tag.buildkite.com/queue": "kubernetes",
				"tag.buildkite.com/arch":  "arm64",
			},
		},
		{
			name:       "k8s rejects value",
			agentTags:  []string{"queue=kubernetes=2"},
			wantLabels: map[string]string{},
			wantErrs:   []error{errors.New(invalidLabelErrMsg)},
		},
		{
			name:      "empty value",
			agentTags: []string{"queue="},
			wantLabels: map[string]string{
				"tag.buildkite.com/queue": "",
			},
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			labels, errs := LabelsFromTags(test.agentTags)
			if diff := cmp.Diff(errs, test.wantErrs, equateErrorMessages); diff != "" {
				t.Errorf("LabelsFromTags(%+v) errors diff (-got +want):\n%s", test.agentTags, diff)
			}
			if diff := cmp.Diff(labels, test.wantLabels); diff != "" {
				t.Errorf("LabelsFromTags(%+v) labels diff (-got +want):\n%s", test.agentTags, diff)
			}
		})
	}
}

func TestMatchJobTags(t *testing.T) {
	t.Parallel()

	for i, test := range []struct {
		jobTags   map[string]string
		agentTags map[string]string
		want      bool
	}{
		{
			jobTags:   map[string]string{},
			agentTags: map[string]string{},
			want:      true,
		},
		{
			jobTags:   map[string]string{"a": "x"},
			agentTags: map[string]string{},
			want:      false,
		},
		{
			jobTags:   map[string]string{},
			agentTags: map[string]string{"a": "x"},
			want:      true,
		},
		{
			jobTags:   map[string]string{"a": "x"},
			agentTags: map[string]string{"a": "x"},
			want:      true,
		},
		{
			jobTags:   map[string]string{"a": "x"},
			agentTags: map[string]string{"b": "y"},
			want:      false,
		},
		{
			jobTags:   map[string]string{"a": "x"},
			agentTags: map[string]string{"a": "x", "b": "y"},
			want:      true,
		},
		{
			jobTags:   map[string]string{"a": "x", "b": "y"},
			agentTags: map[string]string{"a": "x"},
			want:      false,
		},
		{
			jobTags:   map[string]string{"a": "x"},
			agentTags: map[string]string{"a": "y"},
			want:      false,
		},
		{
			jobTags:   map[string]string{"a": "*"},
			agentTags: map[string]string{"a": "x"},
			want:      true,
		},
		{
			jobTags:   map[string]string{"a": "x", "b": "*"},
			agentTags: map[string]string{"a": "x"},
			want:      false,
		},
		{
			jobTags:   map[string]string{"a": "x", "b": "*"},
			agentTags: map[string]string{"a": "x", "b": "y"},
			want:      true,
		},
		{
			jobTags:   map[string]string{"tag1": "x", "tag2": "*"},
			agentTags: map[string]string{"tag1": "x", "tag2": "y", "tags3": "z"},
			want:      true,
		},
	} {

		t.Run(fmt.Sprintf("%d", i), func(t *testing.T) {
			t.Parallel()
			if got, want := MatchJobTags(test.agentTags, test.jobTags), test.want; got != want {
				t.Errorf("MatchJobTags(%+v, %+v) = %t, want %t", test.jobTags, test.agentTags, got, want)
			}
		})
	}
}

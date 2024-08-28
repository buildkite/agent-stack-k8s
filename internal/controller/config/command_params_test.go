package config

import (
	"testing"

	"github.com/google/go-cmp/cmp"
)

func TestInterposer(t *testing.T) {
	tests := []struct {
		interposer Interposer
		want       string
	}{
		{
			interposer: InterposerBuildkite,
			want:       "one\ntwo\nthree\nfour\nfive six seven eight nine ten 'eleven twelve'",
		},
		{
			interposer: InterposerVector,
			want:       "one two three four 'five six seven eight' nine ten 'eleven twelve'",
		},
		{
			interposer: InterposerLegacy,
			want:       "one two three four five six seven eight nine ten eleven twelve",
		},
	}

	for _, test := range tests {
		params := &CommandParams{
			Interposer: test.interposer,
		}

		cmd := []string{"one", "two", "three", "four", "five six seven eight"}
		args := []string{"nine", "ten", "eleven twelve"}

		if diff := cmp.Diff(params.Command(cmd, args), test.want); diff != "" {
			t.Errorf("%v.Command(ctr) diff (-got +want):\n%s", params, diff)
		}
	}
}

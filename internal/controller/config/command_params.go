package config

import (
	"fmt"
	"reflect"
	"slices"
	"strings"

	"github.com/kballard/go-shellquote"
	corev1 "k8s.io/api/core/v1"
)

// CommandParams contains parameters that provide additional control over all
// command container(s).
type CommandParams struct {
	Interposer Interposer             `json:"interposer,omitempty"`
	EnvFrom    []corev1.EnvFromSource `json:"envFrom,omitempty"`
}

func (cmd *CommandParams) ApplyTo(ctr *corev1.Container) {
	if cmd == nil || ctr == nil {
		return
	}
	ctr.EnvFrom = append(ctr.EnvFrom, cmd.EnvFrom...)
}

// Command interprets the command and args fields of the container into a
// BUILDKITE_COMMAND value.
func (cmd *CommandParams) Command(command, args []string) string {
	var interp Interposer
	if cmd != nil {
		interp = cmd.Interposer
	}
	if interp == "" {
		interp = InterposerBuildkite
	}
	switch interp {
	case InterposerBuildkite:
		command := strings.Join(command, "\n")
		if len(args) > 0 {
			command += " " + shellquote.Join(args...)
		}
		return command

	case InterposerVector:
		return shellquote.Join(append(command, args...)...)

	case InterposerLegacy:
		return strings.Join(append(command, args...), " ")

	default:
		// "This should never happen" (famous last words)
		panic(fmt.Sprintf("invalid interposer %q", interp))
	}
}

// Interposer values.
const (
	// InterposerBuildkite forms BUILDKITE_COMMAND by joining podSpec/command
	// with newlines, and appends podSpec/args to the last line joined with
	// spaces and additional shell quoting as needed.
	// This is intended to mimic how a pipeline.yaml steps/command works: as a
	// list of one or more commands. But note that:
	// 1. this is not "correct" as far as Kubernetes would interpret a pod spec
	// 2. per the pod spec schema, it must be a list. Unlike pipeline.yaml a
	//    single command string (not within a list) is not accepted.
	//
	// Example:
	//
	//   command:
	//     - echo 'hello world'
	//     - ls -halt
	//     - touch
	//   args:
	//     - example file.txt
	//
	// becomes:
	//
	//   BUILDKITE_COMMAND="echo 'hello world'\nls -halt\ntouch 'example file.txt'"
	InterposerBuildkite Interposer = "buildkite"

	// InterposerVector forms BUILDKITE_COMMAND by joining podSpec/command
	// and podSpec/args with spaces, and adds shell quoting around individual
	// items as needed.
	// This is intended to mach how Kubernetes interprets command and args: as
	// a 'vector' specifying a single command.
	//
	// Example:
	//
	//   command: ['echo']
	//   args: ['hello world']
	//
	// becomes:
	//
	//   BUILDKITE_COMMAND="echo 'hello world'"
	InterposerVector Interposer = "vector"

	// InterposerLegacy forms BUILDKITE_COMMAND by joining podSpec/command
	// and podSpec/args directly with spaces and no shell quoting.
	// This interposer should be avoided, but was the old default, and is
	// provided as an escape hatch for users with pipelines that stop working on
	// upgrade to the new default (CmdInterposerBuildkite).
	//
	// Example:
	//
	//   command: ['echo']
	//   args: ['hello world']
	//
	// becomes:
	//
	//   BUILDKITE_COMMAND="echo hello world"
	//
	// (note the lack of quotes around "hello world" in the output).
	InterposerLegacy Interposer = "legacy"
)

// Accepted values for Interposer.
var AllowedInterposers = []Interposer{
	"", // same as "buildkite"
	InterposerBuildkite,
	InterposerVector,
	InterposerLegacy,
}

// Interposer is a string-flavoured "enum" of command interposers. These
// configure the conversion from podSpec/command and podSpec/args into
// BUILDKITE_COMMAND.
type Interposer string

var interposerType = reflect.TypeOf(InterposerBuildkite)

// StringToInterposer implements a [mapstructure.DecodeHookFunc] for decoding
// a string into CmdInterposer.
func StringToInterposer(from, to reflect.Type, data any) (any, error) {
	if from.Kind() != reflect.String {
		return data, nil
	}
	if to != interposerType {
		return data, nil
	}
	interp := Interposer(data.(string))
	if interp == "" {
		return "", nil
	}
	if !slices.Contains(AllowedInterposers, interp) {
		return data, fmt.Errorf("invalid command interposer %q", interp)
	}
	return interp, nil
}

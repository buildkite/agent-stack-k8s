# Overriding commands

For command containers, it is possible to alter the `command` or `args` using
PodSpecPatch. These will be re-wrapped in the necessary `buildkite-agent`
invocation.

However, PodSpecPatch will not modify the `command` or `args` values
for these containers (provided by the agent-stack-k8s controller), and will
instead return an error:

* `copy-agent`
* `imagecheck-*`
* `agent`
* `checkout`

If modifying the commands of these containers is something you want to do, first
consider other potential solutions:

* To override checkout behaviour, consider writing a `checkout` hook, or
  disabling the checkout container entirely with `checkout: skip: true`.
* To run additional containers without `buildkite-agent` in them, consider using
  a [sidecar](#sidecars).

We are continually investigating ways to make the stack more flexible while
ensuring core functionality.

> [!CAUTION]
> Avoid using PodSpecPatch to override `command` or `args` of the containers
> added by the agent-stack-k8s controller. Such modifications, if not done with
> extreme care and detailed knowledge about how agent-stack-k8s constructs
> podspecs, are very likely to break how the agent within the pod works.
>
> If the replacement command for the checkout container does not invoke
> `buildkite-agent bootstrap`:
>
>  * the container will not connect to the `agent` container, and the agent will
>    not finish the job normally because there was not an expected number of
>    other containers connecting to it
>  * logs from the container will not be visible in Buildkite
>  * hooks will not be executed automatically
>  * plugins will not be checked out or executed automatically
>
> and various other functions provided by `buildkite-agent` may not work.
>
> If the command for the `agent` container is overridden, and the replacement
> command does not invoke `buildkite-agent start`, then the job will not be
> acquired on Buildkite at all.

If you still wish to disable this precaution, and override the raw `command` or
`args` of these stack-provided containers using PodSpecPatch, you may do so with
the `allow-pod-spec-patch-unsafe-command-modification` config option.

package scheduler

import (
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"net/url"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/buildkite/agent-stack-k8s/v2/api"
	"github.com/buildkite/agent-stack-k8s/v2/internal/controller/agenttags"
	"github.com/buildkite/agent-stack-k8s/v2/internal/controller/config"
	"github.com/buildkite/agent-stack-k8s/v2/internal/controller/model"
	"github.com/buildkite/agent-stack-k8s/v2/internal/version"

	"github.com/buildkite/agent/v3/clicommand"

	"github.com/distribution/reference"
	"github.com/jedib0t/go-pretty/v6/table"
	"go.uber.org/zap"
	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	kerrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/strategicpatch"
	"k8s.io/client-go/kubernetes"
	"k8s.io/utils/ptr"
)

const (
	agentTokenKey                 = "BUILDKITE_AGENT_TOKEN"
	AgentContainerName            = "agent"
	CopyAgentContainerName        = "copy-agent"
	ImageCheckContainerNamePrefix = "imagecheck-"
	CheckoutContainerName         = "checkout"
)

var errK8sPluginProhibited = errors.New("the kubernetes plugin is prohibited by this controller, but was configured on this job")

var (
	commandContainerCommand = []string{"/workspace/tini-static"}
	commandContainerArgs    = []string{"--", "/workspace/buildkite-agent", "bootstrap"}
)

type Config struct {
	Namespace                   string
	Image                       string
	AgentTokenSecretName        string
	JobTTL                      time.Duration
	AdditionalRedactedVars      []string
	WorkspaceVolume             *corev1.Volume
	AgentConfig                 *config.AgentConfig
	DefaultCheckoutParams       *config.CheckoutParams
	DefaultCommandParams        *config.CommandParams
	DefaultSidecarParams        *config.SidecarParams
	DefaultMetadata             config.Metadata
	DefaultImagePullPolicy      corev1.PullPolicy
	DefaultImageCheckPullPolicy corev1.PullPolicy
	PodSpecPatch                *corev1.PodSpec
	ProhibitK8sPlugin           bool
}

func New(logger *zap.Logger, client kubernetes.Interface, cfg Config) *worker {
	return &worker{
		cfg:    cfg,
		client: client,
		logger: logger.Named("worker"),
	}
}

type KubernetesPlugin struct {
	PodSpec           *corev1.PodSpec        `json:"podSpec,omitempty"`
	PodSpecPatch      *corev1.PodSpec        `json:"podSpecPatch,omitempty"`
	GitEnvFrom        []corev1.EnvFromSource `json:"gitEnvFrom,omitempty"`
	Sidecars          []corev1.Container     `json:"sidecars,omitempty"`
	Metadata          config.Metadata        `json:"metadata,omitempty"`
	ExtraVolumeMounts []corev1.VolumeMount   `json:"extraVolumeMounts,omitempty"`
	CheckoutParams    *config.CheckoutParams `json:"checkout,omitempty"`
	CommandParams     *config.CommandParams  `json:"commandParams,omitempty"`
	SidecarParams     *config.SidecarParams  `json:"sidecarParams,omitempty"`
}

type worker struct {
	cfg    Config
	client kubernetes.Interface
	logger *zap.Logger
}

func (w *worker) Handle(ctx context.Context, job model.Job) error {
	logger := w.logger.With(zap.String("job-uuid", job.Uuid))
	logger.Info("creating job")

	inputs, err := w.ParseJob(job.CommandJob)
	if err != nil {
		logger.Warn("Job parsing failed, failing job", zap.Error(err))
		return w.failJob(ctx, inputs, fmt.Sprintf("agent-stack-k8s failed to parse the job: %v", err))
	}

	// Default command container using default image.
	podSpec := &corev1.PodSpec{
		Containers: []corev1.Container{
			{
				Image:   w.cfg.Image,
				Command: []string{job.Command},
			},
		},
	}
	// Use the podSpec provided by the plugin, if allowed.
	if inputs.k8sPlugin != nil && inputs.k8sPlugin.PodSpec != nil {
		podSpec = inputs.k8sPlugin.PodSpec
	}

	kjob, err := w.Build(podSpec, false, inputs)
	if err != nil {
		logger.Warn("Job definition error detected, failing job", zap.Error(err))
		return w.failJob(ctx, inputs, fmt.Sprintf("agent-stack-k8s failed to build a podSpec for the job: %v", err))
	}

	jobCreateCallsCounter.Inc()
	if err := w.createJob(ctx, kjob); err != nil {
		jobCreateErrorCounter.WithLabelValues(string(kerrors.ReasonForError(err))).Inc()

		switch {
		case kerrors.IsAlreadyExists(err):
			logger.Warn("Job creation failed because it already exists", zap.Error(err))
			return fmt.Errorf("%w: %w", model.ErrDuplicateJob, err)

		case kerrors.IsInvalid(err):
			logger.Warn("Job invalid, failing job on Buildkite", zap.Error(err))
			return w.failJob(ctx, inputs, fmt.Sprintf("Kubernetes rejected the podSpec built by agent-stack-k8s: %v", err))

		default:
			return err
		}
	}
	jobCreateSuccessCounter.Inc()
	jobEndToEndDurationHistogram.Observe(time.Since(job.QueriedAt).Seconds())
	return nil
}

func (w *worker) createJob(ctx context.Context, kjob *batchv1.Job) error {
	_, err := w.client.BatchV1().Jobs(w.cfg.Namespace).Create(ctx, kjob, metav1.CreateOptions{})
	if err != nil {
		return fmt.Errorf("failed to create job: %w", err)
	}
	return nil
}

// buildInputs contains the relevant components of a CommandJob needed for Build.
type buildInputs struct {
	// Taken from the job directly.
	uuid            string
	command         string
	agentQueryRules []string

	// Involves some parsing of the job env / plugins map
	envMap       map[string]string
	k8sPlugin    *KubernetesPlugin
	otherPlugins []map[string]json.RawMessage
}

func (w *worker) ParseJob(job *api.CommandJob) (buildInputs, error) {
	parsed := buildInputs{
		uuid:            job.Uuid,
		command:         job.Command,
		agentQueryRules: job.AgentQueryRules,
		envMap:          make(map[string]string),
	}

	for _, val := range job.Env {
		parts := strings.SplitN(val, "=", 2)
		parsed.envMap[parts[0]] = parts[1]
	}
	var plugins []map[string]json.RawMessage
	if pluginsJSON, ok := parsed.envMap["BUILDKITE_PLUGINS"]; ok {
		if err := json.Unmarshal([]byte(pluginsJSON), &plugins); err != nil {
			w.logger.Debug("invalid plugin spec", zap.String("json", pluginsJSON))
			return parsed, fmt.Errorf("failed parsing plugins: %w", err)
		}
	}
	w.logger.Info("parsing", zap.Any("plugins", plugins))
	for _, plugin := range plugins {
		if len(plugin) != 1 {
			return parsed, fmt.Errorf("found invalid plugin: %v", plugin)
		}
		val, isK8sPlugin := plugin["github.com/buildkite-plugins/kubernetes-buildkite-plugin"]
		if !isK8sPlugin {
			for k, v := range plugin {
				parsed.otherPlugins = append(parsed.otherPlugins, map[string]json.RawMessage{k: v})
			}
			continue
		}
		// plugin is the k8s plugin. If the plugin is prohibited, fail.
		if w.cfg.ProhibitK8sPlugin {
			return parsed, errK8sPluginProhibited
		}
		if err := json.Unmarshal(val, &parsed.k8sPlugin); err != nil {
			return parsed, fmt.Errorf("failed parsing Kubernetes plugin: %w", err)
		}
	}
	return parsed, nil
}

// Build builds a job. The checkout container will be skipped either by passing
// `true` or if the configuration is configured to skip it.
func (w *worker) Build(podSpec *corev1.PodSpec, skipCheckout bool, inputs buildInputs) (*batchv1.Job, error) {
	// If Build was called with skipCheckout == false, then look at the config
	// and plugin.
	if !skipCheckout {
		// Start with the default, if set
		if co := w.cfg.DefaultCheckoutParams; co != nil && co.Skip != nil {
			skipCheckout = *co.Skip
		}
		// The plugin overrides the default, if set
		if inputs.k8sPlugin != nil {
			if co := inputs.k8sPlugin.CheckoutParams; co != nil && co.Skip != nil {
				skipCheckout = *co.Skip
			}
		}
	}

	kjob := &batchv1.Job{
		ObjectMeta: metav1.ObjectMeta{
			Name:        k8sJobName(inputs.uuid),
			Labels:      make(map[string]string),
			Annotations: make(map[string]string),
		},
	}

	maps.Copy(kjob.Labels, w.cfg.DefaultMetadata.Labels)
	maps.Copy(kjob.Annotations, w.cfg.DefaultMetadata.Annotations)
	if inputs.k8sPlugin != nil {
		maps.Copy(kjob.Labels, inputs.k8sPlugin.Metadata.Labels)
		maps.Copy(kjob.Annotations, inputs.k8sPlugin.Metadata.Annotations)
	}

	kjob.Labels[config.UUIDLabel] = inputs.uuid
	tagLabels, errs := agenttags.LabelsFromTags(inputs.agentQueryRules)
	if len(errs) > 0 {
		w.logger.Warn("converting all tags to labels", zap.Errors("errs", errs))
	}
	maps.Copy(kjob.Labels, tagLabels)

	buildURL := inputs.envMap["BUILDKITE_BUILD_URL"]
	kjob.Annotations[config.BuildURLAnnotation] = buildURL
	jobURL, err := w.jobURL(inputs.uuid, buildURL)
	if err != nil {
		w.logger.Warn("could not parse BuildURL when annotating with JobURL", zap.String("buildURL", buildURL))
	}
	if jobURL != "" {
		kjob.Annotations[config.JobURLAnnotation] = jobURL
	}

	// Prevent k8s cluster autoscaler from terminating the job before it finishes to scale down cluster
	kjob.Annotations["cluster-autoscaler.kubernetes.io/safe-to-evict"] = "false"

	kjob.Spec.Template.Labels = kjob.Labels
	kjob.Spec.Template.Annotations = kjob.Annotations
	kjob.Spec.BackoffLimit = ptr.To[int32](0)
	kjob.Spec.Template.Spec.TerminationGracePeriodSeconds = ptr.To[int64](defaultTermGracePeriodSeconds)

	// Shared among all containers that run buildkite-agent start or bootstrap.
	env := []corev1.EnvVar{
		{
			Name:  "BUILDKITE_BUILD_PATH",
			Value: "/workspace/build",
		},
		{
			Name:  "BUILDKITE_BIN_PATH",
			Value: "/workspace",
		},
		{
			Name: agentTokenKey,
			ValueFrom: &corev1.EnvVarSource{
				SecretKeyRef: &corev1.SecretKeySelector{
					LocalObjectReference: corev1.LocalObjectReference{Name: w.cfg.AgentTokenSecretName},
					Key:                  agentTokenKey,
				},
			},
		},
		{
			Name:  "BUILDKITE_AGENT_ACQUIRE_JOB",
			Value: inputs.uuid,
		},
		{
			// Suppress the ignored env vars message for now.
			// TODO: remove this when k8s exposes more agent config, e.g. PR#391
			Name:  "BUILDKITE_IGNORED_ENV",
			Value: "",
		},
	}
	if len(inputs.otherPlugins) > 0 {
		otherPluginsJSON, err := json.Marshal(inputs.otherPlugins)
		if err != nil {
			return nil, fmt.Errorf("failed to remarshal non-k8s plugins: %w", err)
		}
		env = append(env, corev1.EnvVar{
			Name:  "BUILDKITE_PLUGINS",
			Value: string(otherPluginsJSON),
		})
	}
	// TODO: revisit the next loop, since it sets **all env vars from the job**
	// on the checkout and command containers, bypassing the "protected env
	// vars" agent logic (that supplies many vars from configuration instead).
	for k, v := range inputs.envMap {
		switch k {
		case "BUILDKITE_COMMAND", "BUILDKITE_ARTIFACT_PATHS", "BUILDKITE_PLUGINS": // noop
		default:
			env = append(env, corev1.EnvVar{Name: k, Value: v})
		}
	}

	redactedVars := append([]string(nil), clicommand.RedactedVars.Value.Value()...)
	redactedVars = append(redactedVars, w.cfg.AdditionalRedactedVars...)
	env = append(env, corev1.EnvVar{
		Name:  clicommand.RedactedVars.EnvVar,
		Value: strings.Join(redactedVars, ","),
	})

	// workspaceVolume is shared among most containers, so set it up first.
	workspaceVolume := w.cfg.WorkspaceVolume
	if workspaceVolume == nil {
		// The default workspace volume is an empty dir volume.
		workspaceVolume = &corev1.Volume{
			Name: "workspace",
			VolumeSource: corev1.VolumeSource{
				EmptyDir: &corev1.EmptyDirVolumeSource{},
			},
		}
	}
	podSpec.Volumes = append(podSpec.Volumes, *workspaceVolume)

	// Set up other volumes (hooks, plugins, keys).
	w.cfg.AgentConfig.ApplyVolumesTo(podSpec)

	// Volume mounts shared among most containers: the workspace volume, and
	// any others supplied with ExtraVolumeMounts.
	volumeMounts := []corev1.VolumeMount{{Name: workspaceVolume.Name, MountPath: "/workspace"}}
	if inputs.k8sPlugin != nil {
		volumeMounts = append(volumeMounts, inputs.k8sPlugin.ExtraVolumeMounts...)
	}

	systemContainerCount := 0
	if !skipCheckout {
		systemContainerCount = 1
	}

	ttl := int32(w.cfg.JobTTL.Seconds())
	kjob.Spec.TTLSecondsAfterFinished = &ttl

	// Env vars used for command containers
	containerEnv := append([]corev1.EnvVar{}, env...)
	containerEnv = append(containerEnv, []corev1.EnvVar{
		{
			Name:  "BUILDKITE_KUBERNETES_EXEC",
			Value: "true",
		},
		{
			Name:  "BUILDKITE_BOOTSTRAP_PHASES",
			Value: "plugin,command",
		},
		{
			Name:  "BUILDKITE_AGENT_NAME",
			Value: "buildkite",
		},
		{
			Name:  "BUILDKITE_SHELL",
			Value: "/bin/sh -ec",
		},
		{
			Name:  "BUILDKITE_ARTIFACT_PATHS",
			Value: inputs.envMap["BUILDKITE_ARTIFACT_PATHS"],
		},
		{
			Name:  "BUILDKITE_PLUGINS_PATH",
			Value: "/workspace/plugins",
		},
		{
			Name:  "BUILDKITE_SOCKETS_PATH",
			Value: "/workspace/sockets",
		},
	}...)

	for i, c := range podSpec.Containers {
		// Default to the command from the pipeline step
		command := inputs.command

		// If the container's command is specified, use that
		if len(c.Command) > 0 {
			// The plugin overrides the default, if set
			if p := inputs.k8sPlugin; p != nil && p.CommandParams != nil && p.CommandParams.Interposer != "" {
				command = p.CommandParams.Command(c.Command, c.Args)
			} else {
				command = w.cfg.DefaultCommandParams.Command(c.Command, c.Args)
			}
		}

		// Substitute the container's entrypoint for tini-static + buildkite-agent
		// Note that tini-static (not plain tini) is needed because we don't
		// know what libraries are available in the image.
		c.Command = commandContainerCommand
		c.Args = commandContainerArgs

		if c.ImagePullPolicy == "" {
			c.ImagePullPolicy = w.cfg.DefaultImagePullPolicy
		}

		c.Env = append(c.Env, containerEnv...)
		c.Env = append(c.Env,
			corev1.EnvVar{
				Name:  "BUILDKITE_COMMAND",
				Value: command,
			},
			corev1.EnvVar{
				Name:  "BUILDKITE_CONTAINER_ID",
				Value: strconv.Itoa(i + systemContainerCount),
			},
		)

		w.cfg.AgentConfig.ApplyToCommand(&c)
		w.cfg.DefaultCommandParams.ApplyTo(&c)
		if inputs.k8sPlugin != nil {
			inputs.k8sPlugin.CommandParams.ApplyTo(&c)
		}

		// Supply more required defaults.
		if c.Name == "" {
			c.Name = fmt.Sprintf("container-%d", i)
		}
		if c.WorkingDir == "" {
			c.WorkingDir = "/workspace"
		}

		c.VolumeMounts = append(c.VolumeMounts, volumeMounts...)
		if inputs.k8sPlugin != nil {
			c.EnvFrom = append(c.EnvFrom, inputs.k8sPlugin.GitEnvFrom...)
		}
		podSpec.Containers[i] = c
	}

	containerCount := len(podSpec.Containers) + systemContainerCount

	if len(podSpec.Containers) == 0 {
		// Create a default command container named "container-0".
		c := corev1.Container{
			Name:            "container-0",
			Image:           w.cfg.Image,
			Command:         commandContainerCommand,
			Args:            commandContainerArgs,
			WorkingDir:      "/workspace",
			VolumeMounts:    volumeMounts,
			ImagePullPolicy: w.cfg.DefaultImagePullPolicy,
			Env: append(containerEnv,
				corev1.EnvVar{
					Name:  "BUILDKITE_COMMAND",
					Value: inputs.command,
				},
				corev1.EnvVar{
					Name:  "BUILDKITE_CONTAINER_ID",
					Value: strconv.Itoa(0 + systemContainerCount),
				},
			),
		}
		w.cfg.AgentConfig.ApplyToCommand(&c)
		w.cfg.DefaultCommandParams.ApplyTo(&c)
		if inputs.k8sPlugin != nil {
			inputs.k8sPlugin.CommandParams.ApplyTo(&c)
			c.EnvFrom = append(c.EnvFrom, inputs.k8sPlugin.GitEnvFrom...)
		}
		podSpec.Containers = append(podSpec.Containers, c)
	}

	if inputs.k8sPlugin != nil {
		for i, c := range inputs.k8sPlugin.Sidecars {
			if c.Name == "" {
				c.Name = fmt.Sprintf("%s-%d", "sidecar", i)
			}
			c.VolumeMounts = append(c.VolumeMounts, volumeMounts...)
			w.cfg.DefaultSidecarParams.ApplyTo(&c)
			inputs.k8sPlugin.SidecarParams.ApplyTo(&c)
			c.EnvFrom = append(c.EnvFrom, inputs.k8sPlugin.GitEnvFrom...)
			podSpec.Containers = append(podSpec.Containers, c)
		}
	}

	agentTags := map[string]string{
		"k8s:agent-stack-version": version.Version(),
	}

	tags, errs := agenttags.TagMapFromTags(inputs.agentQueryRules)
	if len(errs) > 0 {
		w.logger.Warn("errors parsing job tags", zap.String("job", inputs.uuid), zap.Errors("errors", errs))
	}
	maps.Copy(agentTags, tags)

	// Agent server container
	// This runs the "upper layer" of the agent that is responsible for talking
	// to Buildkite: acquiring the job, starting the job, uploading log chunks,
	// finishing the job.
	agentContainer := corev1.Container{
		Name:            AgentContainerName,
		Args:            []string{"start"},
		Image:           w.cfg.Image,
		WorkingDir:      "/workspace",
		VolumeMounts:    volumeMounts,
		ImagePullPolicy: corev1.PullIfNotPresent,
		Env: []corev1.EnvVar{
			{
				Name:  "BUILDKITE_KUBERNETES_EXEC",
				Value: "true",
			},
			{
				Name:  "BUILDKITE_CONTAINER_COUNT",
				Value: strconv.Itoa(containerCount),
			},
			{
				Name:  "BUILDKITE_AGENT_TAGS",
				Value: createAgentTagString(agentTags),
			},
			{
				Name: "BUILDKITE_K8S_NODE",
				ValueFrom: &corev1.EnvVarSource{
					FieldRef: &corev1.ObjectFieldSelector{
						FieldPath: "spec.nodeName",
					},
				},
			},
			{
				Name: "BUILDKITE_K8S_NAMESPACE",
				ValueFrom: &corev1.EnvVarSource{
					FieldRef: &corev1.ObjectFieldSelector{
						FieldPath: "metadata.namespace",
					},
				},
			},
			{
				Name: "BUILDKITE_K8S_SERVICE_ACCOUNT",
				ValueFrom: &corev1.EnvVarSource{
					FieldRef: &corev1.ObjectFieldSelector{
						FieldPath: "spec.serviceAccountName",
					},
				},
			},
		},
	}

	w.cfg.AgentConfig.ApplyToAgentStart(&agentContainer)
	agentContainer.Env = append(agentContainer.Env, env...)
	podSpec.Containers = append(podSpec.Containers, agentContainer)

	if !skipCheckout {
		podSpec.Containers = append(podSpec.Containers,
			w.createCheckoutContainer(podSpec, env, volumeMounts, inputs.k8sPlugin),
		)
	}

	podSpec.InitContainers = append(podSpec.InitContainers, w.createWorkspaceSetupContainer(podSpec, workspaceVolume))

	// Only attempt the job once.
	podSpec.RestartPolicy = corev1.RestartPolicyNever

	// Allow podSpec to be overridden by the controller config and the k8s plugin.
	// Patch from the controller config is applied first.
	if w.cfg.PodSpecPatch != nil {
		patched, err := PatchPodSpec(podSpec, w.cfg.PodSpecPatch, w.cfg.DefaultCommandParams, inputs.k8sPlugin)
		if err != nil {
			return nil, fmt.Errorf("failed to apply podSpec patch from agent: %w", err)
		}
		podSpec = patched
		w.logger.Debug("Applied podSpec patch from agent", zap.Any("patched", patched))
	}

	// If present, patch from the k8s plugin is applied second.
	if inputs.k8sPlugin != nil && inputs.k8sPlugin.PodSpecPatch != nil {
		patched, err := PatchPodSpec(podSpec, inputs.k8sPlugin.PodSpecPatch, w.cfg.DefaultCommandParams, inputs.k8sPlugin)
		if err != nil {
			return nil, fmt.Errorf("failed to apply podSpec patch from k8s plugin: %w", err)
		}
		podSpec = patched
		w.logger.Debug("Applied podSpec patch from k8s plugin", zap.Any("patched", patched))
	}

	// Use init containers to check that images can be used or pulled before
	// any other containers run. These are added _after_ podSpecPatch is applied
	// since podSpecPatch may freely modify each container's image ref.
	// This process also checks that image refs are well-formed so we can fail
	// the job early.
	// Init containers run before the agent, so we can acquire and fail the job
	// if an init container stays in ImagePullBackOff for too long.
	//
	// Rank pull policy by preference. If two containers specify different pull
	// policies, the most preferred policy is used for the image check.
	// Always is most preferred, Never is least preferred.
	var pullPolicyPreference = map[corev1.PullPolicy]int{
		corev1.PullNever:        1,
		corev1.PullIfNotPresent: 2,
		corev1.PullAlways:       3,
	}

	// First decide which images to pre-check and what pull policy to use.
	type policyAndRef struct {
		policy corev1.PullPolicy
		ref    reference.Reference
	}
	type imageParseErr struct {
		container string
		image     string
		err       error
	}
	var invalidRefs []imageParseErr

	preflightImageChecks := make(map[string]policyAndRef)
	for _, c := range podSpec.Containers {
		ref, err := reference.Parse(c.Image)
		if err != nil {
			invalidRefs = append(invalidRefs, imageParseErr{container: c.Name, image: c.Image, err: err})
			continue
		}

		pnr, has := preflightImageChecks[c.Image]
		if !has || pnr.policy == "" {
			preflightImageChecks[c.Image] = policyAndRef{
				policy: c.ImagePullPolicy,
				ref:    ref,
			}
			continue
		}

		// Pick the more preferred pull policy of either what is present in
		// preflightImagePulls or what the user set for c.ImagePullPolicy.
		// Most to least preferred: Always, default, IfNotPresent, Never.
		// (If two containers specify different policies, then to check that
		// both will run, using the policy more likely to pull means the other
		// is more likely to have an image if it succeeds, and more likely to
		// fail quickly if the pull fails.
		r := pullPolicyPreference[c.ImagePullPolicy]
		s := pullPolicyPreference[pnr.policy]
		if r > s {
			preflightImageChecks[c.Image] = policyAndRef{
				policy: c.ImagePullPolicy,
				ref:    ref,
			}
		}
	}

	// We don't need to add more init containers to check images when existing
	// init containers will do this for us implicitly.
	cullCheckForExisting := func(c corev1.Container) {
		if _, err := reference.Parse(c.Image); err != nil {
			invalidRefs = append(invalidRefs, imageParseErr{container: c.Name, image: c.Image, err: err})
			return
		}

		pnr, has := preflightImageChecks[c.Image]
		if !has {
			return // not an image we'd add a check for
		}

		r := pullPolicyPreference[c.ImagePullPolicy]
		s := pullPolicyPreference[pnr.policy]
		// If the existing init container has the same or higher preference than
		// the image check container we would add, then no need to add it.
		if r >= s {
			delete(preflightImageChecks, c.Image)
		}
	}

	for _, c := range podSpec.InitContainers { // user-defined init containers
		cullCheckForExisting(c)
	}

	// If any of the image refs were bad, return a single error with all of them
	// in a sorted table.
	if len(invalidRefs) > 0 {
		if len(invalidRefs) == 1 {
			ie := invalidRefs[0]
			return nil, fmt.Errorf("%w %q for container %q", ie.err, ie.image, ie.container)
		}

		slices.SortFunc(invalidRefs, func(a, b imageParseErr) int {
			return cmp.Compare(a.container, b.container)
		})
		tw := table.NewWriter()
		tw.SetStyle(table.StyleColoredDark)
		tw.AppendHeader(table.Row{"CONTAINER", "IMAGE REF", "PARSE ERROR"})
		tw.AppendSeparator()
		for _, ie := range invalidRefs {
			tw.AppendRow(table.Row{
				strconv.Quote(ie.container), // could also be invalid at this stage
				strconv.Quote(ie.image),     // it couldn't be parsed, so quote it
				ie.err,
			})
		}
		return nil, errors.New("invalid image references\n\n" + tw.Render())
	}

	// Create the pre-flight image check containers.
	i := 0
	for image, pnr := range preflightImageChecks {
		name := ImageCheckContainerNamePrefix + strconv.Itoa(i)
		policy := pnr.policy
		if policy == "" {
			policy = w.cfg.DefaultImageCheckPullPolicy
		}
		if policy == "" {
			// If pull policy is unspecified by either the podSpec{,Patch} or
			// controller configuration, use Always, unless there is a
			// digest that has pinned the ref, in which case use IfNotPresent.
			// (No need to pull a pinned image ref if it's present.)
			policy = corev1.PullAlways
			if _, hasDigest := pnr.ref.(reference.Digested); hasDigest {
				policy = corev1.PullIfNotPresent
			}
		}

		w.logger.Debug(
			"adding preflight image check init container",
			zap.String("name", name),
			zap.String("image", image),
			zap.String("policy", string(policy)),
		)
		podSpec.InitContainers = append(podSpec.InitContainers, corev1.Container{
			Name:            name,
			Image:           image,
			ImagePullPolicy: policy,
			// `tini-static --version` is sorta like `true`, but:
			// (a) always exists (we *just* copied it into /workspace), and
			// (b) is statically compiled, so should be compatible with whatever
			//     container image we're in - no assumption of glibc or musl.
			Command: []string{"/workspace/tini-static"},
			Args:    []string{"--version"},
			VolumeMounts: []corev1.VolumeMount{{
				Name:      workspaceVolume.Name,
				MountPath: "/workspace",
			}},
		})
		i++
	}

	kjob.Spec.Template.Spec = *podSpec

	return kjob, nil
}

var ErrNoCommandModification = errors.New("modifying container commands or args via podSpecPatch is not supported")

func PatchPodSpec(original *corev1.PodSpec, patch *corev1.PodSpec, cmdParams *config.CommandParams, k8sPlugin *KubernetesPlugin) (*corev1.PodSpec, error) {

	// Index command containers by name - these should be unique within each podSpec.
	originalContainers := make(map[string]*corev1.Container)
	for i := range original.Containers {
		c := &original.Containers[i]
		originalContainers[c.Name] = c
	}

	// We do special stuff™️ with init-container commands to make them run as
	// buildkite agent things under the hood, so don't let users mess with them
	// via podSpecPatch.
	for i := range patch.InitContainers {
		c := &patch.InitContainers[i]
		if len(c.Command) == 0 && len(c.Args) == 0 {
			// No modification (strategic merge won't set these to empty).
			continue
		}
		oc := originalContainers[c.Name]
		if oc != nil && slices.Equal(c.Command, oc.Command) && slices.Equal(c.Args, oc.Args) {
			// No modification (original and patch are equal).
			continue
		}

		if CopyAgentContainerName == c.Name {
			return nil, fmt.Errorf("for the agent container, %w", ErrNoCommandModification)
		}
	}

	// We do special stuff™️ with container commands to make them run as
	// buildkite agent things under the hood, so don't let users mess with them
	// via podSpecPatch.
	for i := range patch.Containers {
		c := &patch.Containers[i]
		if len(c.Command) == 0 && len(c.Args) == 0 {
			// No modification (strategic merge won't set these to empty).
			continue
		}
		oc := originalContainers[c.Name]
		if oc != nil && slices.Equal(c.Command, oc.Command) && slices.Equal(c.Args, oc.Args) {
			// No modification (original and patch are equal).
			continue
		}

		// Some modification is occurring.
		// What we prevent vs what we fix up depends on the type of container.

		// Agent, checkout: prevent command modification entirely.
		switch c.Name {
		case AgentContainerName:
			return nil, fmt.Errorf("for the agent container, %w", ErrNoCommandModification)

		case CheckoutContainerName:
			return nil, fmt.Errorf("for the checkout container, %w; instead consider configuring a checkout hook or skipping the checkout container entirely", ErrNoCommandModification)

		default:
			// Is it patching a command container or a sidecar?
			if oc == nil || !slices.Equal(oc.Command, commandContainerCommand) || !slices.Equal(oc.Args, commandContainerArgs) {
				// It's either a new container, which for now we'll treat as
				// a sidecar, or a patch on an existing sidecar.
				// Since these are unmonitored by buildkite-agent within the pod
				// the command modification is fine.
				continue
			}

			// It's a command container. Remove the modification and apply the
			// same transformation from container command to BUILDKITE_COMMAND.
			command := cmdParams.Command(c.Command, c.Args)
			if k8sPlugin != nil && k8sPlugin.CommandParams != nil && k8sPlugin.CommandParams.Interposer != "" {
				command = k8sPlugin.CommandParams.Command(c.Command, c.Args)
			}
			c.Command = nil
			c.Args = nil
			c.Env = append(c.Env, corev1.EnvVar{
				Name:  "BUILDKITE_COMMAND",
				Value: command,
			})
		}
	}

	originalJSON, err := json.Marshal(original)
	if err != nil {
		return nil, fmt.Errorf("error converting original to JSON: %w", err)
	}

	patchJSON, err := json.Marshal(patch)
	if err != nil {
		return nil, fmt.Errorf("error converting patch to JSON: %w", err)
	}

	patchedJSON, err := strategicpatch.StrategicMergePatch(originalJSON, patchJSON, corev1.PodSpec{})
	if err != nil {
		return nil, fmt.Errorf("error applying strategic patch: %w", err)
	}

	var patchedSpec corev1.PodSpec
	if err := json.Unmarshal(patchedJSON, &patchedSpec); err != nil {
		return nil, fmt.Errorf("error converting patched JSON to PodSpec: %w", err)
	}

	return &patchedSpec, nil
}

func (w *worker) createWorkspaceSetupContainer(podSpec *corev1.PodSpec, workspaceVolume *corev1.Volume) corev1.Container {
	podUser, podGroup := int64(0), int64(0)
	if podSpec.SecurityContext != nil {
		if podSpec.SecurityContext.RunAsUser != nil {
			podUser = *(podSpec.SecurityContext.RunAsUser)
		}
		if podSpec.SecurityContext.RunAsGroup != nil {
			podGroup = *(podSpec.SecurityContext.RunAsGroup)
		}
	}

	var securityContext *corev1.SecurityContext
	var containerArgs strings.Builder
	// Ensure that the checkout occurs as the user/group specified in the pod's security context.
	// we will create a buildkite-agent user/group in the checkout container as needed and switch
	// to it. The created user/group will have the uid/gid specified in the pod's security context.
	switch {
	case podUser != 0 && podGroup != 0:
		// The init container needs to be run as root to create the user and give it ownership to the workspace directory
		securityContext = &corev1.SecurityContext{
			RunAsUser:    ptr.To[int64](0),
			RunAsGroup:   ptr.To[int64](0),
			RunAsNonRoot: ptr.To(false),
		}

		fmt.Fprintf(&containerArgs, "chown -R %d:%d /workspace\n", podUser, podGroup)

	case podUser != 0 && podGroup == 0:
		//The init container needs to be run as root to create the user and give it ownership to the workspace directory
		securityContext = &corev1.SecurityContext{
			RunAsUser:    ptr.To[int64](0),
			RunAsGroup:   ptr.To[int64](0),
			RunAsNonRoot: ptr.To[bool](false),
		}

		fmt.Fprintf(&containerArgs, "chown -R %d /workspace\n", podUser)

	// If the group is not root, but the user is root, I don't think we NEED to do anything. It's fine
	// for the user and group to be root for the checked out repo, even though the Pod's security
	// context has a non-root group.
	default:
		securityContext = nil
	}
	// Init containers. These run in order before the regular containers.
	// We run some init containers before any specified in the given podSpec.
	//
	// We use an init container to copy buildkite-agent into /workspace.
	// We also use init containers to check that images can be pulled before
	// any other containers run.
	containerArgs.WriteString("\ncp /usr/local/bin/buildkite-agent /sbin/tini-static /workspace\n")
	return corev1.Container{
		// This container copies buildkite-agent and tini-static into
		// /workspace.
		Name:            CopyAgentContainerName,
		Image:           w.cfg.Image,
		ImagePullPolicy: w.cfg.DefaultImagePullPolicy,
		Command:         []string{"ash"},
		Args:            []string{"-cefx", containerArgs.String()},
		SecurityContext: securityContext,
		VolumeMounts: []corev1.VolumeMount{{
			Name:      workspaceVolume.Name,
			MountPath: "/workspace",
		}},
	}
}

func (w *worker) createCheckoutContainer(
	podSpec *corev1.PodSpec,
	env []corev1.EnvVar,
	volumeMounts []corev1.VolumeMount,
	k8sPlugin *KubernetesPlugin,
) corev1.Container {
	checkoutContainer := corev1.Container{
		Name:            CheckoutContainerName,
		Image:           w.cfg.Image,
		WorkingDir:      "/workspace",
		VolumeMounts:    volumeMounts,
		ImagePullPolicy: w.cfg.DefaultImagePullPolicy,
		Env: []corev1.EnvVar{
			{
				Name:  "BUILDKITE_KUBERNETES_EXEC",
				Value: "true",
			},
			{
				Name:  "BUILDKITE_BOOTSTRAP_PHASES",
				Value: "plugin,checkout",
			},
			{
				Name:  "BUILDKITE_AGENT_NAME",
				Value: "buildkite",
			},
			{
				Name:  "BUILDKITE_CONTAINER_ID",
				Value: "0",
			},
			{
				Name:  "BUILDKITE_PLUGINS_PATH",
				Value: "/workspace/plugins",
			},
		},
	}

	w.cfg.AgentConfig.ApplyToCheckout(&checkoutContainer)
	w.cfg.DefaultCheckoutParams.ApplyTo(podSpec, &checkoutContainer)
	if k8sPlugin != nil {
		k8sPlugin.CheckoutParams.ApplyTo(podSpec, &checkoutContainer)
		checkoutContainer.EnvFrom = append(checkoutContainer.EnvFrom, k8sPlugin.GitEnvFrom...)
	}

	checkoutContainer.Env = append(checkoutContainer.Env, env...)

	podUser, podGroup := int64(0), int64(0)
	if podSpec.SecurityContext != nil {
		if podSpec.SecurityContext.RunAsUser != nil {
			podUser = *(podSpec.SecurityContext.RunAsUser)
		}
		if podSpec.SecurityContext.RunAsGroup != nil {
			podGroup = *(podSpec.SecurityContext.RunAsGroup)
		}
	}

	// If configured, set up a volume mount of a secret containing a
	// .git-credentials file. k8sPlugin (if allowed) supersedes the default.
	gitCredsSecret := w.cfg.DefaultCheckoutParams.GitCredsSecret()
	if k8sPlugin != nil {
		gitCredsSecret = k8sPlugin.CheckoutParams.GitCredsSecret()
	}
	gitConfigCmd := "true"
	if gitCredsSecret != nil {
		podSpec.Volumes = append(podSpec.Volumes,
			corev1.Volume{
				Name:         "git-credentials-ro",
				VolumeSource: corev1.VolumeSource{Secret: gitCredsSecret},
			},
			corev1.Volume{
				Name: "git-credentials",
				VolumeSource: corev1.VolumeSource{
					EmptyDir: &corev1.EmptyDirVolumeSource{
						Medium: "Memory",
					},
				},
			},
		)

		checkoutContainer.VolumeMounts = append(checkoutContainer.VolumeMounts,
			corev1.VolumeMount{
				Name:      "git-credentials-ro",
				MountPath: "/buildkite/git-credentials-ro",
			},
			corev1.VolumeMount{
				Name:      "git-credentials",
				MountPath: "/buildkite/git-credentials",
			},
		)

		// Why copy the file between volumes instead of mounting it directly
		// into place?
		// K8s secret mounts are *always* read only, but the 'store' credential
		// helper always tries to write back to the file.
		// If we didn't do this, we'd get some alarming-looking log lines like:
		// "fatal: unable to write credential store: Resource busy"
		// (despite Git being a drama llama, the failure doesn't impact the
		// checkout process in any meaningful way).
		// TODO: replace this nonsense with a better git credential helper
		gitConfigCmd = "cp /buildkite/git-credentials-ro/.git-credentials /buildkite/git-credentials && " +
			"git config --global credential.helper 'store --file /buildkite/git-credentials/.git-credentials'"
	}

	// Ensure that the checkout occurs as the user/group specified in the pod's security context.
	// we will create a buildkite-agent user/group in the checkout container as needed and switch
	// to it. The created user/group will have the uid/gid specified in the pod's security context.
	switch {
	case podUser != 0 && podGroup != 0:
		// The checkout container needs to be run as root to create the user. After that, it switches to the user.
		checkoutContainer.SecurityContext = &corev1.SecurityContext{
			RunAsUser:    ptr.To[int64](0),
			RunAsGroup:   ptr.To[int64](0),
			RunAsNonRoot: ptr.To(false),
		}

		checkoutContainer.Command = []string{"ash", "-c"}
		checkoutContainer.Args = []string{fmt.Sprintf(`set -exufo pipefail
addgroup -g %d buildkite-agent
adduser -D -u %d -G buildkite-agent -h /workspace buildkite-agent
su buildkite-agent -c "%s && buildkite-agent-entrypoint bootstrap"`,
			podGroup,
			podUser,
			gitConfigCmd,
		)}

	case podUser != 0 && podGroup == 0:
		// The checkout container needs to be run as root to create the user. After that, it switches to the user.
		checkoutContainer.SecurityContext = &corev1.SecurityContext{
			RunAsUser:    ptr.To[int64](0),
			RunAsGroup:   ptr.To[int64](0),
			RunAsNonRoot: ptr.To[bool](false),
		}

		checkoutContainer.Command = []string{"ash", "-c"}
		checkoutContainer.Args = []string{fmt.Sprintf(`set -exufo pipefail
adduser -D -u %d -G root -h /workspace buildkite-agent
su buildkite-agent -c "%s && buildkite-agent-entrypoint bootstrap"`,
			podUser,
			gitConfigCmd,
		)}

	// If the group is not root, but the user is root, I don't think we NEED to do anything. It's fine
	// for the user and group to be root for the checked out repo, even though the Pod's security
	// context has a non-root group.
	default:
		checkoutContainer.SecurityContext = nil
		checkoutContainer.Command = []string{"ash", "-c"}
		checkoutContainer.Args = []string{fmt.Sprintf(`set -exufo pipefail
%s
buildkite-agent-entrypoint bootstrap`,
			gitConfigCmd)}
	}

	return checkoutContainer
}

// failJob fails the job in Buildkite.
func (w *worker) failJob(ctx context.Context, inputs buildInputs, message string) error {
	// Need to fetch the agent token ourselves.
	agentToken, err := fetchAgentToken(ctx, w.logger, w.client, w.cfg.Namespace, w.cfg.AgentTokenSecretName)
	if err != nil {
		w.logger.Error("fetching agent token from secret", zap.Error(err))
		return err
	}

	opts := w.cfg.AgentConfig.ControllerOptions()
	if err := acquireAndFail(ctx, w.logger, agentToken, inputs.uuid, inputs.agentQueryRules, message, opts...); err != nil {
		w.logger.Error("failed to acquire and fail the job on Buildkite", zap.Error(err))
		schedulerBuildkiteJobFailErrorsCounter.Inc()
		return err
	}
	schedulerBuildkiteJobFailsCounter.Inc()
	return nil
}

func (w *worker) jobURL(jobUUID string, buildURL string) (string, error) {
	u, err := url.Parse(buildURL)
	if err != nil {
		return "", err
	}
	u.Fragment = jobUUID
	return u.String(), nil
}

func k8sJobName(jobUUID string) string {
	return fmt.Sprintf("buildkite-%s", jobUUID)
}

// Format each agentTag as key=value and join with ,
func createAgentTagString(tags map[string]string) string {
	ts := make([]string, 0, len(tags))
	for k, v := range tags {
		ts = append(ts, k+"="+v)
	}
	return strings.Join(ts, ",")
}

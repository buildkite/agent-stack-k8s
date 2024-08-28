package scheduler

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/buildkite/agent-stack-k8s/v2/api"
	"github.com/buildkite/agent-stack-k8s/v2/internal/controller/agenttags"
	"github.com/buildkite/agent-stack-k8s/v2/internal/controller/config"
	"github.com/buildkite/agent-stack-k8s/v2/internal/version"

	"github.com/buildkite/agent/v3/clicommand"

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
	defaultTermGracePeriodSeconds     = 60
	agentTokenKey                     = "BUILDKITE_AGENT_TOKEN"
	AgentContainerName                = "agent"
	CopyAgentContainerName            = "copy-agent"
	ImagePullCheckContainerNamePrefix = "imagepullcheck-"
	CheckoutContainerName             = "checkout"
)

var errK8sPluginProhibited = errors.New("the kubernetes plugin is prohibited by this controller, but was configured on this job")

type Config struct {
	Namespace              string
	Image                  string
	AgentTokenSecretName   string
	JobTTL                 time.Duration
	AdditionalRedactedVars []string
	DefaultCheckoutParams  *config.CheckoutParams
	DefaultCommandParams   *config.CommandParams
	DefaultSidecarParams   *config.SidecarParams
	PodSpecPatch           *corev1.PodSpec
	ProhibitK8sPlugin      bool
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
	Metadata          Metadata               `json:"metadata,omitempty"`
	ExtraVolumeMounts []corev1.VolumeMount   `json:"extraVolumeMounts,omitempty"`
	CheckoutParams    *config.CheckoutParams `json:"checkout,omitempty"`
	CommandParams     *config.CommandParams  `json:"commandParams,omitempty"`
	SidecarParams     *config.SidecarParams  `json:"sidecarParams,omitempty"`
}

type Metadata struct {
	Annotations map[string]string
	Labels      map[string]string
}

type worker struct {
	cfg    Config
	client kubernetes.Interface
	logger *zap.Logger
}

func (w *worker) Create(ctx context.Context, job *api.CommandJob) error {
	logger := w.logger.With(zap.String("uuid", job.Uuid))
	logger.Info("creating job")

	inputs, err := w.ParseJob(job)
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

	err = w.createJob(ctx, kjob)
	if kerrors.IsInvalid(err) {
		logger.Warn("Job creation failed, failing job", zap.Error(err))
		return w.failJob(ctx, inputs, fmt.Sprintf("Kubernetes rejected the podSpec built by agent-stack-k8s: %v", err))
	}
	return err
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
			Name:        kjobName(inputs.uuid),
			Labels:      make(map[string]string),
			Annotations: make(map[string]string),
		},
	}

	if inputs.k8sPlugin != nil {
		maps.Copy(kjob.Labels, inputs.k8sPlugin.Metadata.Labels)
		maps.Copy(kjob.Annotations, inputs.k8sPlugin.Metadata.Annotations)
	}

	kjob.Labels[config.UUIDLabel] = inputs.uuid
	w.labelWithAgentTags(kjob.Labels, inputs.agentQueryRules)
	kjob.Annotations[config.BuildURLAnnotation] = inputs.envMap["BUILDKITE_BUILD_URL"]
	w.annotateWithJobURL(kjob.Annotations, inputs.uuid, inputs.envMap)

	// Prevent k8s cluster autoscaler from terminating the job before it finishes to scale down cluster
	kjob.Annotations["cluster-autoscaler.kubernetes.io/safe-to-evict"] = "false"

	kjob.Spec.Template.Labels = kjob.Labels
	kjob.Spec.Template.Annotations = kjob.Annotations
	kjob.Spec.BackoffLimit = ptr.To[int32](0)
	kjob.Spec.Template.Spec.TerminationGracePeriodSeconds = ptr.To[int64](defaultTermGracePeriodSeconds)

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

	volumeMounts := []corev1.VolumeMount{{Name: "workspace", MountPath: "/workspace"}}
	if inputs.k8sPlugin != nil {
		volumeMounts = append(volumeMounts, inputs.k8sPlugin.ExtraVolumeMounts...)
	}

	systemContainerCount := 0
	if !skipCheckout {
		systemContainerCount = 1
	}

	ttl := int32(w.cfg.JobTTL.Seconds())
	kjob.Spec.TTLSecondsAfterFinished = &ttl

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

		// Substitute the container's entrypoint for buildkite-agent
		c.Command = []string{"/workspace/buildkite-agent"}
		c.Args = []string{"bootstrap"}

		c.ImagePullPolicy = corev1.PullAlways
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
			Command:         []string{"/workspace/buildkite-agent"},
			Args:            []string{"bootstrap"},
			WorkingDir:      "/workspace",
			VolumeMounts:    volumeMounts,
			ImagePullPolicy: corev1.PullAlways,
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

	agentTags := []agentTag{
		{
			Name:  "k8s:agent-stack-version",
			Value: version.Version(),
		},
	}

	if tags, err := agentTagsFromJob(inputs.agentQueryRules); err != nil {
		w.logger.Warn("error parsing job tags", zap.String("job", inputs.uuid))
	} else {
		agentTags = append(agentTags, tags...)
	}

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
		ImagePullPolicy: corev1.PullAlways,
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
	agentContainer.Env = append(agentContainer.Env, env...)
	podSpec.Containers = append(podSpec.Containers, agentContainer)

	if !skipCheckout {
		podSpec.Containers = append(podSpec.Containers,
			w.createCheckoutContainer(podSpec, env, volumeMounts, inputs.k8sPlugin),
		)
	}

	// Init containers. These run in order before the regular containers.
	// We run some init containers before any specified in the given podSpec.
	//
	// We use an init container to copy buildkite-agent into /workspace.
	// We also use init containers to check that images can be pulled before
	// any other containers run.
	//
	// Why not let Kubernetes worry about pulling images as needed? Well...
	// If Kubernetes can't pull an image, the container stays in Waiting with
	// ImagePullBackOff. But Kubernetes also tries to start containers ASAP.
	// This behaviour is fine for when you are using Kubernetes to run services,
	// such as a web server or database, because you are DevOps and are dealing
	// with Kubernetes more directly.
	// Since the agent, command, checkout etc are in separate containers, we can
	// be in the awkward situation of having started a BK job with an agent
	// running happily in the agent server container, but any of the other pod
	// containers can still be waiting on an image that can't be pulled.
	//
	// Over here in the agent-stack-k8s controller, we can detect
	// ImagePullBackOff using the k8s API (see imagePullBackOffWatcher.go) but
	// our options for pulling the plug on a job that's already started are
	// limited, because we can't steal responsibility for the job from the
	// already-running agent.
	//
	// We can:
	//  * kill the agent container (agent lost, which looks weird)
	//  * use GraphQL to cancel the job, rely on the agent to count the
	//    containers that connected to it through the socket, and spit out an
	//    error in the log that is easy to miss. (This is what we used to do.)
	// Both those options suck.
	//
	// So instead, we pull each required image in its own init container and
	// set the entrypoint to the equivalent of "/bin/true".
	// If the image pull fails, we can use agentcore to fail the job directly.
	// This early detection approach is also useful in a CI/CD context since the
	// user is more likely to be playing with pipeline configurations.
	//
	// The main downside to pre-pulling images with init containers is that
	// init containers do not run in parallel, so Kubernetes might well decide
	// not to pull them in parallel. Also there's no agent running to report
	// that we're currently waiting for the image pull. (In the BK UI, the job
	// will sit in "waiting for agent" for a bit.)
	//
	// TODO: investigate agent modifications to accept handover of a started
	// job (i.e. make the controller acquire the job, log some k8s progress,
	// then hand over the job token to the agent in the pod.)
	initContainers := []corev1.Container{
		{
			// This container copies buildkite-agent into /workspace.
			Name:            CopyAgentContainerName,
			Image:           w.cfg.Image,
			ImagePullPolicy: corev1.PullAlways,
			Command:         []string{"cp"},
			Args: []string{
				"/usr/local/bin/buildkite-agent",
				"/workspace",
			},
			VolumeMounts: []corev1.VolumeMount{{
				Name:      "workspace",
				MountPath: "/workspace",
			}},
		},
	}

	// Pre-pull these images. (Note that even when specifying PullAlways,
	// layers can still be cached on the node.)
	preflightImagePulls := map[string]struct{}{}
	for _, c := range podSpec.Containers {
		preflightImagePulls[c.Image] = struct{}{}
	}
	for _, c := range podSpec.EphemeralContainers {
		preflightImagePulls[c.Image] = struct{}{}
	}
	// w.cfg.Image is the first init container, so we don't need to add another
	// container specifically to check it can pull. Same goes for user-supplied
	// init containers.
	delete(preflightImagePulls, w.cfg.Image)
	for _, c := range podSpec.InitContainers {
		delete(preflightImagePulls, c.Image)
	}

	i := 0
	for image := range preflightImagePulls {
		name := ImagePullCheckContainerNamePrefix + strconv.Itoa(i)
		w.logger.Info("creating preflight image pull init container", zap.String("name", name), zap.String("image", image))
		initContainers = append(initContainers, corev1.Container{
			Name:            name,
			Image:           image,
			ImagePullPolicy: corev1.PullAlways,
			// We run `buildkite-agent --version` to exit the container
			// immediately with success.
			// Why not /bin/true or sh -c 'exit 0'? Because `true` and `sh`
			// might not be present in the image (e.g. built from scratch).
			// Why not, say, copy Busybox (from the agent image, based on alpine)
			// into /workspace as `true`?
			// Because Alpine Busybox is dynamically linked against musl, and
			// musl might not be present.
			Command: []string{"/workspace/buildkite-agent"},
			Args:    []string{"--version"},
			VolumeMounts: []corev1.VolumeMount{{
				Name:      "workspace",
				MountPath: "/workspace",
			}},
		})
		i++
	}

	podSpec.InitContainers = append(initContainers, podSpec.InitContainers...)

	podSpec.Volumes = append(podSpec.Volumes, corev1.Volume{
		Name: "workspace",
		VolumeSource: corev1.VolumeSource{
			EmptyDir: &corev1.EmptyDirVolumeSource{},
		},
	})
	podSpec.RestartPolicy = corev1.RestartPolicyNever

	// Allow podSpec to be overridden by the agent configuration and the k8s plugin

	// Patch from the agent is applied first
	if w.cfg.PodSpecPatch != nil {
		w.logger.Info("applying podSpec patch from agent")
		patched, err := PatchPodSpec(podSpec, w.cfg.PodSpecPatch)
		if err != nil {
			return nil, fmt.Errorf("failed to apply podSpec patch from agent: %w", err)
		}
		podSpec = patched
	}

	if inputs.k8sPlugin != nil && inputs.k8sPlugin.PodSpecPatch != nil {
		w.logger.Info("applying podSpec patch from k8s plugin")
		patched, err := PatchPodSpec(podSpec, inputs.k8sPlugin.PodSpecPatch)
		if err != nil {
			return nil, fmt.Errorf("failed to apply podSpec patch from k8s plugin: %w", err)
		}
		podSpec = patched
	}

	kjob.Spec.Template.Spec = *podSpec

	return kjob, nil
}

var ErrNoCommandModification = errors.New("modifying container commands or args via podSpecPatch is not supported. Specify the command in the job's command field instead")

func PatchPodSpec(original *corev1.PodSpec, patch *corev1.PodSpec) (*corev1.PodSpec, error) {
	// We do special stuff™️ with container commands to make them run as buildkite agent things under the hood, so don't
	// let users mess with them via podSpecPatch.
	for _, c := range patch.Containers {
		if len(c.Command) != 0 || len(c.Args) != 0 {
			return nil, ErrNoCommandModification
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
		ImagePullPolicy: corev1.PullAlways,
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

	w.cfg.DefaultCheckoutParams.ApplyTo(&checkoutContainer)
	if k8sPlugin != nil {
		k8sPlugin.CheckoutParams.ApplyTo(&checkoutContainer)
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
	return failJob(ctx, w.logger, agentToken, inputs.uuid, inputs.agentQueryRules, message)
}

func (w *worker) labelWithAgentTags(dstLabels map[string]string, agentQueryRules []string) {
	ls, errs := agenttags.ToLabels(agentQueryRules)
	if len(errs) != 0 {
		w.logger.Warn("converting all tags to labels", zap.Errors("errs", errs))
	}

	for k, v := range ls {
		dstLabels[k] = v
	}
}

func (w *worker) annotateWithJobURL(dstAnnotations map[string]string, jobUUID string, envMap map[string]string) {
	buildURL := envMap["BUILDKITE_BUILD_URL"]
	u, err := url.Parse(buildURL)
	if err != nil {
		w.logger.Warn(
			"could not parse BuildURL when annotating with JobURL",
			zap.String("buildURL", buildURL),
		)
		return
	}
	u.Fragment = jobUUID
	dstAnnotations[config.JobURLAnnotation] = u.String()
}

func kjobName(jobUUID string) string {
	return fmt.Sprintf("buildkite-%s", jobUUID)
}

type agentTag struct {
	Name  string
	Value string
}

func agentTagsFromJob(agentQueryRules []string) ([]agentTag, error) {
	agentTags := make([]agentTag, 0, len(agentQueryRules))
	for _, tag := range agentQueryRules {
		k, v, found := strings.Cut(tag, "=")
		if !found {
			return nil, fmt.Errorf("could not parse tag: %q", tag)
		}
		agentTags = append(agentTags, agentTag{Name: k, Value: v})
	}

	return agentTags, nil
}

func createAgentTagString(tags []agentTag) string {
	var sb strings.Builder
	for i, t := range tags {
		sb.WriteString(t.Name)
		sb.WriteString("=")
		sb.WriteString(t.Value)
		if i < len(tags)-1 {
			sb.WriteString(",")
		}
	}
	return sb.String()
}

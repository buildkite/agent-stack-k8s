package scheduler

import (
	"cmp"
	"strconv"

	"github.com/buildkite/agent-stack-k8s/v2/internal/controller/config"
	"github.com/distribution/reference"
	"go.uber.org/zap"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
)

// Rank pull policy by preference. If two containers specify different pull
// policies for the same image, the most preferred policy is used for the
// image check.
// Always is most preferred, Never is less preferred.
var pullPolicyPreference = map[corev1.PullPolicy]int{
	corev1.PullNever:        1, // least preferred
	corev1.PullIfNotPresent: 2,
	corev1.PullAlways:       3, // most preferred
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

type preflightImageCheckMap map[string]policyAndRef

func selectImagesToCheck(w *worker, podSpec *corev1.PodSpec) (preflightImageCheckMap, []imageParseErr) {
	var invalidRefs []imageParseErr

	preflightImageChecks := make(map[string]policyAndRef)
	for i := range podSpec.Containers {
		c := &podSpec.Containers[i]

		// Parse the image ref for error reporting and policy purposes.
		ref, err := reference.Parse(c.Image)
		if err != nil {
			invalidRefs = append(invalidRefs, imageParseErr{container: c.Name, image: c.Image, err: err})
			continue
		}

		// "nominal policy" = "the image pull policy we would use if there
		// was only one container".
		// Actually we have two containers: an image check init container
		// and an app container. The nominal policy is what mainly applies
		// to image check containers.
		//
		// The nominal policy is computed as follows:
		//  - If the container already has a pull policy, use that.
		//  - Otherwise, use the DefaultImageCheckPullPolicy, if configured.
		//  - Otherwise, use the DefaultImagePullPolicy, if configured.
		//  - Otherwise, use Always or IfNotPresent depending on whether or not
		//    the image ref is pinned by a digest.
		nominalPolicy := cmp.Or(
			c.ImagePullPolicy,
			w.cfg.DefaultImageCheckPullPolicy,
			w.cfg.DefaultImagePullPolicy,
			defaultPullPolicyForImage(ref),
		)

		// The actual policy for this app container, accounting for defaults
		// and pre-pulling by the image check container.
		//
		// 	- If the container already has a pull policy, use that.
		//  - Otherwise, use the DefaultImagePullPolicy, if configured.
		//  - Otherwise, use Always or IfNotPresent depending on whether or not
		//    the image ref is pinned by a digest.
		//
		// DefaultImageCheckPullPolicy doesn't apply here, because this isn't
		// an image check container - it's an app container!
		//
		// If the nominal policy is Always, then we'll create an imagecheck
		// init container with the Always policy. But then there's no need to
		// pull it again later, so we downgrade the app container's policy from
		// Always to IfNotPresent.
		c.ImagePullPolicy = cmp.Or(
			c.ImagePullPolicy,
			w.cfg.DefaultImagePullPolicy,
			defaultPullPolicyForImage(ref),
		)
		if nominalPolicy == corev1.PullAlways && c.ImagePullPolicy == corev1.PullAlways {
			c.ImagePullPolicy = corev1.PullIfNotPresent
		}

		pnr, has := preflightImageChecks[c.Image]
		if !has {
			w.logger.Debug("setting initial pull policy for preflight image check",
				zap.String("container", c.Name),
				zap.String("image", c.Image),
				zap.String("new-policy", string(nominalPolicy)),
			)
			preflightImageChecks[c.Image] = policyAndRef{
				policy: nominalPolicy,
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
		r := pullPolicyPreference[nominalPolicy]
		s := pullPolicyPreference[pnr.policy]
		if r > s {
			w.logger.Debug("updating pull policy for preflight image check",
				zap.String("container", c.Name),
				zap.String("image", c.Image),
				zap.String("old-policy", string(pnr.policy)),
				zap.String("new-policy", string(nominalPolicy)),
			)
			preflightImageChecks[c.Image] = policyAndRef{
				policy: nominalPolicy,
				ref:    ref,
			}
		}
	}

	return preflightImageChecks, invalidRefs
}

// cullCheckForExisting removes image-check containers with images that are
// already present among existing init containers.
// Such image-check containers don't need to be added, since the image pull
// failure checking happens across all init containers. Adding another init
// container specifically for checking image pull would be redundant.
// However, if a stronger pull policy is specified for a non-init container
// than for an existing init container, then an image check may still be
// needed.
func cullCheckForExisting(
	w *worker,
	preflightImageChecks preflightImageCheckMap,
	invalidRefs []imageParseErr,
	c corev1.Container,
) (preflightImageCheckMap, []imageParseErr) {
	if _, err := reference.Parse(c.Image); err != nil {
		invalidRefs = append(invalidRefs, imageParseErr{container: c.Name, image: c.Image, err: err})
		return preflightImageChecks, invalidRefs
	}

	pnr, has := preflightImageChecks[c.Image]
	if !has {
		return preflightImageChecks, invalidRefs // not an image we'd add a check for
	}

	r := pullPolicyPreference[c.ImagePullPolicy]
	s := pullPolicyPreference[pnr.policy]
	// If the existing init container has the same or higher preference than
	// the image check container we would add, then no need to add it.
	if r >= s {
		w.logger.Debug("removing preflight image check because of lower policy preference",
			zap.String("container", c.Name),
			zap.String("image", c.Image),
			zap.String("old-policy", string(pnr.policy)),
		)
		delete(preflightImageChecks, c.Image)
	}

	return preflightImageChecks, invalidRefs // not an image we'd add a check for
}

func makeImageCheckContainers(
	w *worker,
	preflightImageChecks preflightImageCheckMap,
	workspaceVolumeName string,
) []corev1.Container {
	// Use default resource limits for pre-flight image check containers, if not provided
	imageCheckContainerCPULimit, err := resource.ParseQuantity(w.cfg.ImageCheckContainerCPULimit)
	if err != nil {
		imageCheckContainerCPULimit, _ = resource.ParseQuantity(config.DefaultImageCheckContainerCPULimit)
	}
	imageCheckContainerMemoryLimit, err := resource.ParseQuantity(w.cfg.ImageCheckContainerMemoryLimit)
	if err != nil {
		imageCheckContainerMemoryLimit, _ = resource.ParseQuantity(config.DefaultImageCheckContainerMemoryLimit)
	}

	i := 0
	containers := make([]corev1.Container, 0, len(preflightImageChecks))
	for image, pnr := range preflightImageChecks {
		name := ImageCheckContainerNamePrefix + strconv.Itoa(i)

		policy := cmp.Or(pnr.policy, w.cfg.DefaultImageCheckPullPolicy, defaultPullPolicyForImage(pnr.ref))

		w.logger.Debug(
			"adding preflight image check init container",
			zap.String("name", name),
			zap.String("image", image),
			zap.String("policy", string(policy)),
		)
		containers = append(containers, corev1.Container{
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
				Name:      workspaceVolumeName,
				MountPath: "/workspace",
			}},
			// Apply container resource requests to imagecheck containers
			Resources: corev1.ResourceRequirements{
				Requests: corev1.ResourceList{
					corev1.ResourceCPU:    resource.MustParse("100m"),
					corev1.ResourceMemory: resource.MustParse("64Mi"),
				},
				Limits: corev1.ResourceList{
					corev1.ResourceCPU:    imageCheckContainerCPULimit,
					corev1.ResourceMemory: imageCheckContainerMemoryLimit,
				},
			},
		})
		i++
	}

	return containers
}

package config

// Metadata contains k8s job metadata to apply when creating pods. It can be
// set as a default within the config, or per step using the kubernetes plugin.
type Metadata struct {
	Annotations map[string]string
	Labels      map[string]string
}

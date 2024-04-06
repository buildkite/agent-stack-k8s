{{/* Generate basic labels *}}
{{- define "agent-stack-k8s.mandatoryLabels" }}
app: {{ .Release.Name }}
{{- end }}

{{- define "agent-stack-k8s.labels" }}
{{- toYaml (mustMerge (fromYaml (include "agent-stack-k8s.mandatoryLabels" .)) .Values.labels) }}
{{- end }}

{{/* Generate basic secrets metadata */}}
{{- define "agent-stack-k8s.mandatorySecretsMetadata" }}
name: {{ .Release.Name }}-secrets
namespace: {{ .Release.Namespace }}
{{- end }}

{{- define "agent-stack-k8s.secretsMetadata" }}
{{- toYaml (mustMerge (fromYaml (include "agent-stack-k8s.mandatorySecretsMetadata" .)) .Values.secretsMetadata) }}
{{- end }}

{{/* Generate basic serviceaccount metadata */}}
{{- define "agent-stack-k8s.mandatoryServiceAccountMetadata" }}
name: {{ .Release.Name }}-controller
namespace: {{ .Release.Namespace }}
{{- end }}

{{- define "agent-stack-k8s.serviceAccountMetadata" }}
{{- toYaml (mustMerge (fromYaml (include "agent-stack-k8s.mandatoryServiceAccountMetadata" .)) .Values.serviceAccountMetadata) }}
{{- end }}

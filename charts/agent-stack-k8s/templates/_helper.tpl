{{/* Generate basic labels */}}
{{- define "agent-stack-k8s.mandatoryLabels" }}
app: {{ .Release.Name }}
{{- end }}

{{- define "agent-stack-k8s.labels" }}
{{- toYaml (mustMerge (fromYaml (include "agent-stack-k8s.mandatoryLabels" .)) .Values.labels) }}
{{- end }}

{{/* Generate basic secrets metadata */}}
{{- define "agent-stack-k8s.mandatorySecretsmetadata" }}
name: {{ .Release.Name }}-secrets
namespace: {{ .Release.Namespace }}
{{- end }}

{{- define "agent-stack-k8s.secretsmetadata" }}
{{- toYaml (mustMerge (fromYaml (include "agent-stack-k8s.mandatorySecretsmetadata" .)) .Values.secretsmetadata) }}
{{- end }}

{{/* Generate basic serviceaccount metadata */}}
{{- define "agent-stack-k8s.mandatoryServiceaccountmetadata" }}
name: {{ .Release.Name }}-controller
namespace: {{ .Release.Namespace }}
{{- end }}

{{- define "agent-stack-k8s.serviceaccountmetadata" }}
{{- toYaml (mustMerge (fromYaml (include "agent-stack-k8s.mandatoryServiceaccountmetadata" .)) .Values.serviceaccountmetadata) }}
{{- end }}

{{- define "agent-stack-k8s.name" -}}
{{- default .Chart.Name .Values.nameOverride | trunc 63 | trimSuffix "-" -}}
{{- end -}}

{{- define "agent-stack-k8s.fullname" -}}
{{- if .Values.fullnameOverride -}}
{{- .Values.fullnameOverride | trunc 63 | trimSuffix "-" -}}
{{- else -}}
{{- $name := default .Chart.Name .Values.nameOverride -}}
{{- if contains $name .Release.Name -}}
{{- .Release.Name | trunc 63 | trimSuffix "-" -}}
{{- else -}}
{{- printf "%s-%s" .Release.Name $name | trunc 63 | trimSuffix "-" -}}
{{- end -}}
{{- end -}}
{{- end -}}

{{/* Generate basic labels */}}
{{- define "agent-stack-k8s.mandatoryLabels" -}}
app: {{ include "agent-stack-k8s.fullname" . }}
{{- end }}

{{- define "agent-stack-k8s.labels" }}
{{- toYaml (mustMerge (fromYaml (include "agent-stack-k8s.mandatoryLabels" .)) .Values.labels) }}
{{- end }}

{{/* Generate basic secrets metadata */}}
{{- define "agent-stack-k8s.mandatorySecretsMetadata" }}
name: {{ include "agent-stack-k8s.fullname" . }}-secrets
namespace: {{ .Release.Namespace }}
{{- end }}

{{- define "agent-stack-k8s.secretsMetadata" }}
{{- toYaml (mustMerge (fromYaml (include "agent-stack-k8s.mandatorySecretsMetadata" .)) .Values.secretsMetadata) }}
{{- end }}

{{/* Generate basic serviceaccount metadata */}}
{{- define "agent-stack-k8s.mandatoryServiceAccountMetadata" }}
name: {{ include "agent-stack-k8s.fullname" . }}-controller
namespace: {{ .Release.Namespace }}
{{- end }}

{{- define "agent-stack-k8s.serviceAccountMetadata" }}
{{- toYaml (mustMerge (fromYaml (include "agent-stack-k8s.mandatoryServiceAccountMetadata" .)) .Values.serviceAccountMetadata) }}
{{- end }}

{{/* Generate basic labels */}}
{{- define "agent-stack-k8s.mandatoryLabels" }}
app: {{ .Release.Name }}
{{- end }}

{{- define "agent-stack-k8s.labels" }}
{{- toYaml (mustMerge (fromYaml (include "agent-stack-k8s.mandatoryLabels" .)) .Values.labels) }}
{{- end }}

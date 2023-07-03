{{/* Generate basic labels */}}
{{- define "agent-stack-k8s.labels" }}
  labels:  
    {{- toYaml $.Values.labels }}
    app: {{ .Release.Name }}
{{- end }}

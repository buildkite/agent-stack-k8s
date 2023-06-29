{{/* Generate basic labels */}}
{{- define "mychart.labels" }}
  labels:
    app: {{ .Release.Name }}
    {{- toYaml $.Values.labels | nindent 8 }}
{{- end }}
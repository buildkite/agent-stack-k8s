apiVersion: v1
kind: ConfigMap
metadata:
  name: {{ .Release.Name }}-config
  namespace: {{ .Release.Namespace }}
data:
  config.yaml: |
    agent-token-secret: {{ if .Values.agentStackSecret }}{{ .Values.agentStackSecret }}{{ else }}{{ .Release.Name }}-secrets{{ end }}
    namespace: {{ .Release.Namespace }}
    {{- .Values.config | toYaml | nindent 4 }}
  pre-schedule: |
    {{- .Values.pre-schedule | nindent 4 }}

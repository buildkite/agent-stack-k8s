apiVersion: v1
kind: ConfigMap
metadata:
  name: {{ include "agent-stack-k8s.fullname" . }}-config
  namespace: {{ .Release.Namespace }}
data:
  config.yaml: |
    agent-token-secret: {{ if .Values.agentStackSecret }}{{ .Values.agentStackSecret }}{{ else }}{{ include "agent-stack-k8s.fullname" . }}-secrets{{ end }}
    namespace: {{ .Release.Namespace }}
    {{- .Values.config | toYaml | nindent 4 }}

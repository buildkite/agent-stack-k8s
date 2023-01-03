apiVersion: v1
kind: ConfigMap
metadata:
  name: {{ .Release.Name }}-config
  namespace: {{ .Release.Namespace }}
data:
  config.yaml: |
    agent-token-secret: {{ .Release.Name }}-secrets
    namespace: {{ .Release.Namespace }}
    {{- .Values.config | toYaml | nindent 4 }}

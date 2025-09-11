{{ if .Values.monitoring.podMonitor.deploy }}
apiVersion: monitoring.coreos.com/v1
kind: PodMonitor
metadata:
  name: {{ include "agent-stack-k8s.fullname" . }}-podmonitor
  namespace: {{ with .Values.monitoring.podMonitor.namespace }}{{ . }}{{ else }}{{ .Release.Namespace }}{{ end }}
  labels:
    app: {{ include "agent-stack-k8s.fullname" . }}
spec:
  jobLabel: app
  namespaceSelector:
    matchNames:
      - {{ .Release.Namespace }}
  selector:
    matchLabels:
      app: {{ include "agent-stack-k8s.fullname" . }}
  podMetricsEndpoints:
    - port: metrics 
      {{ with .Values.monitoring.podMonitor.scrapeInterval }}interval: {{ . }}{{ end }}
{{end}}
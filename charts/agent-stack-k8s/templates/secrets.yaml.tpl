{{- if not .Values.agentStackSecret -}}
# A sample agent token and graphql token secret
apiVersion: v1
kind: Secret
metadata:
  {{- include "agent-stack-k8s.secretsMetadata" . | nindent 2 }}
data:
  BUILDKITE_AGENT_TOKEN: {{ required "agentToken must be set" .Values.agentToken | b64enc | quote }}
  {{ with .Values.graphqlToken }}BUILDKITE_TOKEN: {{ . | b64enc | quote }}{{ end }}
{{- end -}}

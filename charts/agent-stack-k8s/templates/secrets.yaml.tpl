{{- if not .Values.agentStackSecret -}}
# A sample agent token and graphql token secret
apiVersion: v1
kind: Secret
metadata:
  name: {{ .Release.Name }}-secrets
  namespace: {{ .Release.Namespace }}
  annotations:
    strategy.spinnaker.io/versioned: ""
data:
  BUILDKITE_AGENT_TOKEN: {{ required "agentToken must be set" .Values.agentToken | b64enc | quote }}
  BUILDKITE_TOKEN: {{ required "graphqlToken must be set" .Values.graphqlToken | b64enc | quote }}
{{- end -}}
apiVersion: apps/v1
kind: Deployment
metadata:
  name: {{ include "agent-stack-k8s.fullname" . }}
  namespace: {{ .Release.Namespace }}
spec:
  selector:
    matchLabels:
      {{- include "agent-stack-k8s.mandatoryLabels" . | nindent 8 }}
  template:
    metadata:
      labels:
        {{- include "agent-stack-k8s.labels" . | nindent 8 }}
      annotations:
        checksum/config: {{ include (print $.Template.BasePath "/config.yaml.tpl") . | sha256sum }}
        checksum/secrets: {{ include (print $.Template.BasePath "/secrets.yaml.tpl") . | sha256sum }}
    spec:
      serviceAccountName: {{ include "agent-stack-k8s.fullname" . }}-controller
      nodeSelector:
        {{- toYaml $.Values.nodeSelector | nindent 8 }}
      tolerations:
        {{- toYaml $.Values.tolerations | nindent 8 }}
      containers:
      - name: controller
        terminationMessagePolicy: FallbackToLogsOnError
        image: {{ .Values.image }}
        env:
        - name: CONFIG
          value: /etc/config.yaml
        envFrom:
          - secretRef:
              name: {{ if .Values.agentStackSecret }}{{ .Values.agentStackSecret }}{{ else }}{{ include "agent-stack-k8s.fullname" . }}-secrets{{ end }}
        volumeMounts:
          - name: config
            mountPath: /etc/config.yaml
            subPath: config.yaml
        resources:
          {{- toYaml .Values.resources | nindent 10 }}
        {{ with index .Values.config "prometheus-port" -}}
        ports:
          - name: metrics
            containerPort: {{.}}
        {{ end -}}
        securityContext:
          allowPrivilegeEscalation: false
          readOnlyRootFilesystem: true
          runAsNonRoot: true
          capabilities:
            drop:
            - ALL
          seccompProfile:
            type: RuntimeDefault
      volumes:
        - name: config
          configMap:
            name: {{ include "agent-stack-k8s.fullname" . }}-config

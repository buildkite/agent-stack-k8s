apiVersion: apps/v1
kind: Deployment
metadata:
  name: {{ .Release.Name }}
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
      serviceAccountName: {{ .Release.Name }}-controller
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
          value: /etc/agent-stack-k8s/config.yaml
        envFrom:
          - secretRef:
              name: {{ if .Values.agentStackSecret }}{{ .Values.agentStackSecret }}{{ else }}{{ .Release.Name }}-secrets{{ end }}
        volumeMounts:
          - name: tmp-volume
            mountPath: /tmp
          - name: config-volume
            mountPath: /etc/agent-stack-k8s/config.yaml
            subPath: config.yaml
          - name: config-volume
            mountPath: /etc/agent-stack-k8s/pre-schedule
            subPath: pre-schedule
        resources:
          {{- toYaml .Values.resources | nindent 10 }}
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
        - name: tmp-volume
          emptyDir:
            medium: Memory
            sizeLimit: 100Mi
        - name: config-volume
          configMap:
            name: {{ .Release.Name }}-config
            items:
              - key: config.yaml
                path: config.yaml
              - key: pre-schedule
                path: pre-schedule
                mode: 0755
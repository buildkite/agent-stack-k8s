apiVersion: apps/v1
kind: Deployment
metadata:
  name: {{ .Release.Name }}
  namespace: {{ .Release.Namespace }}
spec:
  selector:
    matchLabels:
      app: {{ .Release.Name }}
  template:
    metadata:
      labels:
        app: {{ .Release.Name }}
      annotations:
        checksum/config: {{ include (print $.Template.BasePath "/config.yaml.tpl") . | sha256sum }}
        checksum/secrets: {{ include (print $.Template.BasePath "/secrets.yaml.tpl") . | sha256sum }}
    spec:
      serviceAccountName: {{ .Release.Name }}
      containers:
      - name: controller
        terminationMessagePolicy: FallbackToLogsOnError
        image: {{ .Values.image }}
        env:
        - name: CONFIG
          value: /etc/config.yaml
        envFrom:
          - secretRef:
              name: {{ .Release.Name }}-secrets
        volumeMounts:
          - name: config
            mountPath: /etc/config.yaml
            subPath: config.yaml
        resources:
          requests:
            cpu: 100m
            memory: 100Mi

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
            name: {{ .Release.Name }}-config

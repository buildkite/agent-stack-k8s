apiVersion: rbac.authorization.k8s.io/v1
kind: Role
metadata:
  {{- include "agent-stack-k8s.serviceAccountMetadata" . | nindent 2 }}
rules:
  - apiGroups:
      - batch
    resources:
      - jobs
    verbs:
      - get
      - list
      - watch
      - create
      - update
      - delete
  - apiGroups:
      - ""
    resources:
      - pods
    verbs:
      - get
      - list
      - watch
      - delete
  - apiGroups:
      - ""
    resources:
      - secrets
    verbs:
      - get
      - create
      - delete
  - apiGroups:
      - ""
    resources:
      - podtemplates
    verbs:
      - get
  - apiGroups:
      - ""
    resources:
      - events
    verbs:
      - list
---
apiVersion: rbac.authorization.k8s.io/v1
kind: RoleBinding
metadata:
  name: {{ include "agent-stack-k8s.fullname" . }}
  namespace: {{ .Release.Namespace }}
roleRef:
  apiGroup: rbac.authorization.k8s.io
  kind: Role
  name: {{ include "agent-stack-k8s.fullname" . }}-controller
subjects:
  - kind: ServiceAccount
    name: {{ include "agent-stack-k8s.fullname" . }}-controller
    namespace: {{ .Release.Namespace }}
---
apiVersion: v1
kind: ServiceAccount
metadata:
  {{- include "agent-stack-k8s.serviceAccountMetadata" . | nindent 2 }}
{{- if index .Values.config "capacity-gate" }}
---
# capacity-gate counts pod requests against node allocatable across the whole cluster.
apiVersion: rbac.authorization.k8s.io/v1
kind: ClusterRole
metadata:
  name: {{ include "agent-stack-k8s.fullname" . }}-{{ .Release.Namespace }}-capacity-gate
rules:
  - apiGroups:
      - ""
    resources:
      - pods
      - nodes
    verbs:
      - get
      - list
      - watch
---
apiVersion: rbac.authorization.k8s.io/v1
kind: ClusterRoleBinding
metadata:
  name: {{ include "agent-stack-k8s.fullname" . }}-{{ .Release.Namespace }}-capacity-gate
roleRef:
  apiGroup: rbac.authorization.k8s.io
  kind: ClusterRole
  name: {{ include "agent-stack-k8s.fullname" . }}-{{ .Release.Namespace }}-capacity-gate
subjects:
  - kind: ServiceAccount
    name: {{ include "agent-stack-k8s.fullname" . }}-controller
    namespace: {{ .Release.Namespace }}
{{- end }}

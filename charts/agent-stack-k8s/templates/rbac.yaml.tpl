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
  - apiGroups:
      - ""
    resources:
      - pods
    verbs:
      - get
      - list
      - watch
  - apiGroups:
      - ""
    resources:
      - secrets
    verbs:
      - get
  - apiGroups:
      - ""
    resources:
      - pods/eviction
    verbs:
      - create
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

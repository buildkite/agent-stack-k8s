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
---
apiVersion: rbac.authorization.k8s.io/v1
kind: RoleBinding
metadata:
  name: {{ .Release.Name }}
  namespace: {{ .Release.Namespace }}
roleRef:
  apiGroup: rbac.authorization.k8s.io
  kind: Role
  name: {{ .Release.Name }}-controller
subjects:
  - kind: ServiceAccount
    name: {{ .Release.Name }}-controller
    namespace: {{ .Release.Namespace }}
---
apiVersion: v1
kind: ServiceAccount
metadata:
  {{- include "agent-stack-k8s.serviceAccountMetadata" . | nindent 2 }}

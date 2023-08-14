apiVersion: rbac.authorization.k8s.io/v1
kind: Role
metadata:
  {{- include "agent-stack-k8s.serviceaccountmetadata" . | nindent 2 }}
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
roleRef:
  apiGroup: rbac.authorization.k8s.io
  kind: Role
  name: {{ .Release.Name }}-controller
subjects:
  - kind: ServiceAccount
    {{- include "agent-stack-k8s.serviceaccountmetadata" . | nindent 4 }}
---
apiVersion: v1
kind: ServiceAccount
metadata:
  {{- include "agent-stack-k8s.serviceaccountmetadata" . | nindent 2 }}

{{- define "tunlease.name" -}}tunlease-gateway{{- end }}
{{- define "tunlease.fullname" -}}{{ .Release.Name }}-gateway{{- end }}
{{- define "tunlease.labels" -}}
app.kubernetes.io/name: {{ include "tunlease.name" . }}
app.kubernetes.io/instance: {{ .Release.Name }}
app.kubernetes.io/managed-by: {{ .Release.Service }}
{{- end }}

{{- define "muxcore-module.name" -}}
{{- default .Chart.Name .Values.nameOverride | trunc 63 | trimSuffix "-" }}
{{- end }}

{{- define "muxcore-module.selectorLabels" -}}
app.kubernetes.io/name: {{ include "muxcore-module.name" . }}
app.kubernetes.io/instance: {{ .Release.Name }}
{{- end }}

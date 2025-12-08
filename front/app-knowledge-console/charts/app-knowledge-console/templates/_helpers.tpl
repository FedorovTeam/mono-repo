{{- define "app-knowledge-console.labels" -}}
helm.sh/chart: {{ include "app-knowledge-console.chart" . }}
app.kubernetes.io/name: {{ include "app-knowledge-console.name" . }}
app.kubernetes.io/instance: {{ .Release.Name }}
app.kubernetes.io/version: {{ .Chart.AppVersion }}
app.kubernetes.io/managed-by: {{ .Release.Service }}
{{- end }}

{{- define "app-knowledge-console.selectorLabels" -}}
app.kubernetes.io/name: {{ include "app-knowledge-console.name" . }}
app.kubernetes.io/instance: {{ .Release.Name }}
{{- end }}

{{- define "app-knowledge-console.name" -}}
{{- default .Chart.Name .Values.fullnameOverride | trunc 63 | trimSuffix "-" -}}
{{- end }}

{{- define "app-knowledge-console.chart" -}}
{{- printf "%s-%s" .Chart.Name .Chart.Version | replace "+" "_" | trunc 63 | trimSuffix "-" -}}
{{- end }}

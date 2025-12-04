{{- define "template-service.name" -}}
{{- default .Chart.Name .Values.nameOverride -}}
{{- end -}}

{{- define "template-service.fullname" -}}
{{- if .Values.fullnameOverride -}}
{{- .Values.fullnameOverride -}}
{{- else -}}
{{- $name := default .Chart.Name .Values.nameOverride -}}
{{- printf "%s-%s" .Release.Name $name | trunc 63 | trimSuffix "-" -}}
{{- end -}}
{{- end -}}

{{- define "template-service.labels" -}}
helm.sh/chart: {{ include "template-service.chart" . }}
{{ include "template-service.selectorLabels" . }}
{{- if .Chart.AppVersion }}
app.kubernetes.io/version: {{ .Chart.AppVersion | quote }}
{{- end }}
app.kubernetes.io/managed-by: {{ .Release.Service }}
{{- end -}}

{{- define "template-service.selectorLabels" -}}
app.kubernetes.io/name: {{ include "template-service.name" . }}
app.kubernetes.io/instance: {{ .Release.Name }}
{{- end -}}

{{- define "template-service.chart" -}}
{{- printf "%s-%s" .Chart.Name .Chart.Version -}}
{{- end -}}

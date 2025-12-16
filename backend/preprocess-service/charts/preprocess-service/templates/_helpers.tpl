{{- define "preprocess-service.name" -}}
{{- default .Chart.Name .Values.nameOverride -}}
{{- end -}}

{{- define "preprocess-service.fullname" -}}
{{- if .Values.fullnameOverride -}}
{{- .Values.fullnameOverride -}}
{{- else -}}
{{- $name := default .Chart.Name .Values.nameOverride -}}
{{- printf "%s-%s" .Release.Name $name | trunc 63 | trimSuffix "-" -}}
{{- end -}}
{{- end -}}

{{- define "preprocess-service.labels" -}}
helm.sh/chart: {{ include "preprocess-service.chart" . }}
{{ include "preprocess-service.selectorLabels" . }}
{{- if .Chart.AppVersion }}
app.kubernetes.io/version: {{ .Chart.AppVersion | quote }}
{{- end }}
app.kubernetes.io/managed-by: {{ .Release.Service }}
{{- end -}}

{{- define "preprocess-service.selectorLabels" -}}
app.kubernetes.io/name: {{ include "preprocess-service.name" . }}
app.kubernetes.io/instance: {{ .Release.Name }}
{{- end -}}

{{- define "preprocess-service.chart" -}}
{{- printf "%s-%s" .Chart.Name .Chart.Version -}}
{{- end -}}

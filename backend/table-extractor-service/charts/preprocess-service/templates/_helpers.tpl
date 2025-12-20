{{/* backend/table-extractor-service/charts/table-extractor-service/templates/_helpers.tpl */}}
{{- define "table-extractor-service.name" -}}
{{- default .Chart.Name .Values.nameOverride -}}
{{- end -}}

{{- define "table-extractor-service.fullname" -}}
{{- if .Values.fullnameOverride -}}
{{- .Values.fullnameOverride -}}
{{- else -}}
{{- $name := default .Chart.Name .Values.nameOverride -}}
{{- printf "%s-%s" .Release.Name $name | trunc 63 | trimSuffix "-" -}}
{{- end -}}
{{- end -}}

{{- define "table-extractor-service.labels" -}}
helm.sh/chart: {{ include "table-extractor-service.chart" . }}
{{ include "table-extractor-service.selectorLabels" . }}
{{- if .Chart.AppVersion }}
app.kubernetes.io/version: {{ .Chart.AppVersion | quote }}
{{- end }}
app.kubernetes.io/managed-by: {{ .Release.Service }}
{{- end -}}

{{- define "table-extractor-service.selectorLabels" -}}
app.kubernetes.io/name: {{ include "table-extractor-service.name" . }}
app.kubernetes.io/instance: {{ .Release.Name }}
{{- end -}}

{{- define "table-extractor-service.chart" -}}
{{- printf "%s-%s" .Chart.Name .Chart.Version -}}
{{- end -}}

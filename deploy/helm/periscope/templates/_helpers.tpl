{{/*
Expand the name of the chart.
*/}}
{{- define "periscope.name" -}}
{{- default .Chart.Name .Values.nameOverride | trunc 63 | trimSuffix "-" -}}
{{- end -}}

{{/*
Create a default fully qualified app name.
*/}}
{{- define "periscope.fullname" -}}
{{- if .Values.fullnameOverride -}}
{{- .Values.fullnameOverride | trunc 63 | trimSuffix "-" -}}
{{- else -}}
{{- $name := default .Chart.Name .Values.nameOverride -}}
{{- if contains $name .Release.Name -}}
{{- .Release.Name | trunc 63 | trimSuffix "-" -}}
{{- else -}}
{{- printf "%s-%s" .Release.Name $name | trunc 63 | trimSuffix "-" -}}
{{- end -}}
{{- end -}}
{{- end -}}

{{/*
Chart label.
*/}}
{{- define "periscope.chart" -}}
{{- printf "%s-%s" .Chart.Name .Chart.Version | replace "+" "_" | trunc 63 | trimSuffix "-" -}}
{{- end -}}

{{/*
Common labels.
*/}}
{{- define "periscope.labels" -}}
helm.sh/chart: {{ include "periscope.chart" . }}
{{ include "periscope.selectorLabels" . }}
{{- if .Chart.AppVersion }}
app.kubernetes.io/version: {{ .Chart.AppVersion | quote }}
{{- end }}
app.kubernetes.io/managed-by: {{ .Release.Service }}
{{- end -}}

{{/*
Selector labels.
*/}}
{{- define "periscope.selectorLabels" -}}
app.kubernetes.io/name: {{ include "periscope.name" . }}
app.kubernetes.io/instance: {{ .Release.Name }}
{{- end -}}

{{/*
ServiceAccount name.
*/}}
{{- define "periscope.serviceAccountName" -}}
{{- if .Values.serviceAccount.create -}}
{{- default (include "periscope.fullname" .) .Values.serviceAccount.name -}}
{{- else -}}
{{- default "default" .Values.serviceAccount.name -}}
{{- end -}}
{{- end -}}

{{/*
Name of the K8s Secret holding OIDC_CLIENT_SECRET (modes existing,
plain, external). Empty when mode=native.
*/}}
{{- define "periscope.secretName" -}}
{{- if eq .Values.secrets.mode "existing" -}}
{{- .Values.secrets.existing.name -}}
{{- else if eq .Values.secrets.mode "plain" -}}
{{- printf "%s-oidc" (include "periscope.fullname" .) -}}
{{- else if eq .Values.secrets.mode "external" -}}
{{- printf "%s-oidc" (include "periscope.fullname" .) -}}
{{- end -}}
{{- end -}}

{{/*
Env-var key the Deployment reads OIDC_CLIENT_SECRET from. Defaults to
OIDC_CLIENT_SECRET; only `existing` mode lets the operator override.
*/}}
{{- define "periscope.secretKey" -}}
{{- if eq .Values.secrets.mode "existing" -}}
{{- default "OIDC_CLIENT_SECRET" .Values.secrets.existing.key -}}
{{- else -}}
OIDC_CLIENT_SECRET
{{- end -}}
{{- end -}}

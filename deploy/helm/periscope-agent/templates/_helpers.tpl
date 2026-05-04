{{/*
Standard helm naming/label helpers, scoped to periscope-agent.
*/}}

{{- define "periscope-agent.name" -}}
{{- default .Chart.Name .Values.nameOverride | trunc 63 | trimSuffix "-" }}
{{- end }}

{{- define "periscope-agent.fullname" -}}
{{- if .Values.fullnameOverride }}
{{- .Values.fullnameOverride | trunc 63 | trimSuffix "-" }}
{{- else }}
{{- $name := default .Chart.Name .Values.nameOverride }}
{{- if contains $name .Release.Name }}
{{- .Release.Name | trunc 63 | trimSuffix "-" }}
{{- else }}
{{- printf "%s-%s" .Release.Name $name | trunc 63 | trimSuffix "-" }}
{{- end }}
{{- end }}
{{- end }}

{{- define "periscope-agent.labels" -}}
app.kubernetes.io/name: {{ include "periscope-agent.name" . }}
app.kubernetes.io/instance: {{ .Release.Name }}
app.kubernetes.io/version: {{ .Chart.AppVersion | quote }}
app.kubernetes.io/managed-by: {{ .Release.Service }}
helm.sh/chart: {{ printf "%s-%s" .Chart.Name .Chart.Version | replace "+" "_" }}
app.kubernetes.io/component: agent
{{- end }}

{{- define "periscope-agent.selectorLabels" -}}
app.kubernetes.io/name: {{ include "periscope-agent.name" . }}
app.kubernetes.io/instance: {{ .Release.Name }}
{{- end }}

{{- define "periscope-agent.serviceAccountName" -}}
{{- if .Values.serviceAccount.create }}
{{- default (include "periscope-agent.fullname" .) .Values.serviceAccount.name }}
{{- else }}
{{- default "default" .Values.serviceAccount.name }}
{{- end }}
{{- end }}

{{/*
Bootstrap-token Secret name. Held separately from the persisted
state Secret because the bootstrap token is single-use — once the
agent registers, the bootstrap-token Secret can be deleted.
*/}}
{{- define "periscope-agent.bootstrapSecretName" -}}
{{- printf "%s-bootstrap" (include "periscope-agent.fullname" .) | trunc 63 | trimSuffix "-" }}
{{- end }}

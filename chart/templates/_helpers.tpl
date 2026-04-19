{{/*
Expand the name of the chart.
*/}}
{{- define "autoscaler-cloudscale.name" -}}
{{- default .Chart.Name .Values.nameOverride | trunc 63 | trimSuffix "-" }}
{{- end }}

{{/*
Create a default fully qualified app name.
*/}}
{{- define "autoscaler-cloudscale.fullname" -}}
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

{{/*
Create chart name and version as used by the chart label.
*/}}
{{- define "autoscaler-cloudscale.chart" -}}
{{- printf "%s-%s" .Chart.Name .Chart.Version | replace "+" "_" | trunc 63 | trimSuffix "-" }}
{{- end }}

{{/*
Common labels
*/}}
{{- define "autoscaler-cloudscale.labels" -}}
helm.sh/chart: {{ include "autoscaler-cloudscale.chart" . }}
{{ include "autoscaler-cloudscale.selectorLabels" . }}
{{- if .Chart.AppVersion }}
app.kubernetes.io/version: {{ .Chart.AppVersion | quote }}
{{- end }}
app.kubernetes.io/managed-by: {{ .Release.Service }}
{{- end }}

{{/*
Selector labels
*/}}
{{- define "autoscaler-cloudscale.selectorLabels" -}}
app.kubernetes.io/name: {{ include "autoscaler-cloudscale.name" . }}
app.kubernetes.io/instance: {{ .Release.Name }}
{{- end }}

{{/*
Create the name of the service account to use
*/}}
{{- define "autoscaler-cloudscale.serviceAccountName" -}}
{{- if .Values.serviceAccount.create }}
{{- default (include "autoscaler-cloudscale.fullname" .) .Values.serviceAccount.name }}
{{- else }}
{{- default "default" .Values.serviceAccount.name }}
{{- end }}
{{- end }}

{{/*
TLS secret name
*/}}
{{- define "autoscaler-cloudscale.tlsSecretName" -}}
{{ include "autoscaler-cloudscale.fullname" . }}-tls
{{- end }}

{{/*
CA secret name
*/}}
{{- define "autoscaler-cloudscale.caSecretName" -}}
{{ include "autoscaler-cloudscale.fullname" . }}-ca
{{- end }}

{{/*
Machineconfig secret name
*/}}
{{- define "autoscaler-cloudscale.machineconfigSecretName" -}}
{{- if .Values.machineconfig.existingSecret }}
{{- .Values.machineconfig.existingSecret }}
{{- else }}
{{- include "autoscaler-cloudscale.fullname" . }}-machineconfig
{{- end }}
{{- end }}

{{/*
cloudscale API secret name
*/}}
{{- define "autoscaler-cloudscale.apiSecretName" -}}
{{- if .Values.cloudscaleAPI.existingSecret }}
{{- .Values.cloudscaleAPI.existingSecret }}
{{- else }}
{{- include "autoscaler-cloudscale.fullname" . }}-api
{{- end }}
{{- end }}

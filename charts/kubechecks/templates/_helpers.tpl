{{/*
Expand the name of the chart.
*/}}
{{- define "kubechecks.name" -}}
{{- default .Chart.Name .Values.nameOverride | trunc 63 | trimSuffix "-" }}
{{- end }}

{{/*
Create a default fully qualified app name.
We truncate at 63 chars because some Kubernetes name fields are limited to this (by the DNS naming spec).
If release name contains chart name it will be used as a full name.
*/}}
{{- define "kubechecks.fullname" -}}
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
{{- define "kubechecks.chart" -}}
{{- printf "%s-%s" .Chart.Name .Chart.Version | replace "+" "_" | trunc 63 | trimSuffix "-" }}
{{- end }}

{{/*
Common labels
*/}}
{{- define "kubechecks.labels" -}}
{{ include "kubechecks.selectorLabels" . }}
app.kubernetes.io/version: {{ .Values.deployment.image.tag }}
app.kubernetes.io/managed-by: {{ .Release.Service }}
{{- with .Values.commonLabels }}
{{ . | toYaml }}
{{- end }}
{{- end }}

{{/*
{{- end }}

{{/*
Selector labels
*/}}
{{- define "kubechecks.selectorLabels" -}}
app.kubernetes.io/name: {{ include "kubechecks.name" . }}
app.kubernetes.io/instance: {{ .Release.Name }}
{{- end }}

{{/*
Create the name of the service account to use
*/}}
{{- define "kubechecks.serviceAccountName" -}}
{{- tpl .Values.serviceAccount.name . }}
{{- end }}

{{/*
Create the name of the secret file to use
*/}}
{{- define "kubechecks.secretsName" -}}
{{- tpl .Values.secrets.name . }}
{{- end -}}

{{/*
KUBECHECKS_SHUTDOWN_TIMEOUT derived from the grace period, unless the user sets
it themselves. Rendered only when the user doesn't, so the pod spec doesn't
carry the variable twice.
*/}}
{{- define "kubechecks.shutdownTimeoutEnv" -}}
{{- $grace := int (.Values.deployment.terminationGracePeriodSeconds | default 30) -}}
{{- $userSet := or (hasKey .Values.configMap.env "KUBECHECKS_SHUTDOWN_TIMEOUT") (hasKey .Values.secrets.env "KUBECHECKS_SHUTDOWN_TIMEOUT") -}}
{{- range .Values.deployment.env }}
{{- if eq .name "KUBECHECKS_SHUTDOWN_TIMEOUT" }}{{ $userSet = true }}{{ end }}
{{- end }}
{{- if not $userSet }}
- name: KUBECHECKS_SHUTDOWN_TIMEOUT
  value: {{ printf "%ds" (max 1 (sub $grace 5)) | quote }}
{{- end }}
{{- end }}

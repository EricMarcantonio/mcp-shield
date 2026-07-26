{{/*
Expand the name of the chart.
*/}}
{{- define "mcp-shield.name" -}}
{{- default .Chart.Name .Values.nameOverride | trunc 63 | trimSuffix "-" }}
{{- end }}

{{/*
Create a default fully qualified app name, honoring fullnameOverride /
nameOverride the way every standard Helm starter chart does, so this chart
behaves the way anyone who has installed a Bitnami/community chart expects.
*/}}
{{- define "mcp-shield.fullname" -}}
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

{{- define "mcp-shield.chart" -}}
{{- printf "%s-%s" .Chart.Name .Chart.Version | replace "+" "_" | trunc 63 | trimSuffix "-" }}
{{- end }}

{{/*
Common labels applied to every object this chart renders.
*/}}
{{- define "mcp-shield.labels" -}}
helm.sh/chart: {{ include "mcp-shield.chart" . }}
{{ include "mcp-shield.selectorLabels" . }}
app.kubernetes.io/managed-by: {{ .Release.Service }}
{{- end }}

{{/*
Selector labels. Kept separate from mcp-shield.labels because these are the
only ones that may ever appear on a Service/NetworkPolicy podSelector or a
Deployment's spec.selector -- both are immutable-on-update in Kubernetes, so
nothing volatile (chart version, managed-by) can live here.
*/}}
{{- define "mcp-shield.selectorLabels" -}}
app.kubernetes.io/name: {{ include "mcp-shield.name" . }}
app.kubernetes.io/instance: {{ .Release.Name }}
{{- end }}

{{/*
ServiceAccount name to use.
*/}}
{{- define "mcp-shield.serviceAccountName" -}}
{{- if .Values.serviceAccount.create }}
{{- default (include "mcp-shield.fullname" .) .Values.serviceAccount.name }}
{{- else }}
{{- default "default" .Values.serviceAccount.name }}
{{- end }}
{{- end }}

{{/*
Image reference, honoring Chart.AppVersion (kept in step with the release
tag) as the default tag so operators don't have to set image.tag by hand
for a normal upgrade.
*/}}
{{- define "mcp-shield.image" -}}
{{- $tag := .Values.image.tag | default .Chart.AppVersion }}
{{- printf "%s:%s" .Values.image.repository $tag }}
{{- end }}

{{/*
Name of the Secret/ConfigMap holding servers.json, honoring
config.existingSecret when the operator supplies their own.
*/}}
{{- define "mcp-shield.configObjectName" -}}
{{- .Values.config.existingSecret | default (printf "%s-servers" (include "mcp-shield.fullname" .)) }}
{{- end }}

{{/*
Fail the render outright if replicaCount is anything but 1. mcp-shield's
state is a single SQLite file (see values.yaml persistence.* comments); more
than one live replica writing to it is how the approvals audit trail gets
corrupted, not a supported scaling path. This check exists so that turning
the dial is a deliberate values.yaml edit against this comment, not an
accidental `--set replicaCount=3`.
*/}}
{{- define "mcp-shield.assertSingleReplica" -}}
{{- if ne (int .Values.replicaCount) 1 }}
{{- fail (printf "mcp-shield: replicaCount must be 1 (got %d) -- this chart's SQLite-backed state does not support multiple live replicas; see values.yaml's `replicaCount` comment" (int .Values.replicaCount)) }}
{{- end }}
{{- end }}

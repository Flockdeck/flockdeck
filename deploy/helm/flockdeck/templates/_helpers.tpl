{{/*
The chart's name, and the release's full name, cut to what Kubernetes allows.
*/}}
{{- define "flockdeck.name" -}}
{{- default .Chart.Name .Values.nameOverride | trunc 63 | trimSuffix "-" }}
{{- end }}

{{- define "flockdeck.fullname" -}}
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

{{- define "flockdeck.labels" -}}
helm.sh/chart: {{ printf "%s-%s" .Chart.Name .Chart.Version | replace "+" "_" | trunc 63 | trimSuffix "-" }}
{{ include "flockdeck.selectorLabels" . }}
app.kubernetes.io/version: {{ .Values.image.tag | default .Chart.AppVersion | quote }}
app.kubernetes.io/managed-by: {{ .Release.Service }}
{{- end }}

{{- define "flockdeck.selectorLabels" -}}
app.kubernetes.io/name: {{ include "flockdeck.name" . }}
app.kubernetes.io/instance: {{ .Release.Name }}
{{- end }}

{{- define "flockdeck.serviceAccountName" -}}
{{- if .Values.serviceAccount.create }}
{{- default (include "flockdeck.fullname" .) .Values.serviceAccount.name }}
{{- else }}
{{- default "default" .Values.serviceAccount.name }}
{{- end }}
{{- end }}

{{/*
secretName is the Secret the chart makes for whatever secret values it was
given directly (remote.join, remote.invite, apiKey.value).
*/}}
{{- define "flockdeck.secretName" -}}
{{- include "flockdeck.fullname" . }}
{{- end }}

{{/*
ownSecret says whether the chart makes its own Secret: true when any secret
value is given directly rather than through an existingSecret of your own.
*/}}
{{- define "flockdeck.ownSecret" -}}
{{- $remote := and (or .Values.remote.join .Values.remote.invite) (not .Values.remote.existingSecret) }}
{{- $apiKey := and .Values.apiKey.value (not .Values.apiKey.existingSecret) }}
{{- if or $remote $apiKey }}true{{ end }}
{{- end }}

{{/*
validate refuses, at install, values flockdeck would refuse at start or that
would quietly do nothing -- found at `helm install`/`helm template` rather
than in a pod that crash-loops.
*/}}
{{- define "flockdeck.validate" -}}
{{- if and .Values.remote.join .Values.remote.invite }}
{{- fail "remote.join and remote.invite are alternatives (joining an existing account, or an invitation into a new one) -- set only one" }}
{{- end }}
{{- if and .Values.persistence.workspace.existingClaim (not .Values.persistence.workspace.enabled) }}
{{- fail "persistence.workspace.existingClaim is set but persistence.workspace.enabled is false, so it would never be mounted" }}
{{- end }}
{{- if and .Values.persistence.state.existingClaim (not .Values.persistence.state.enabled) }}
{{- fail "persistence.state.existingClaim is set but persistence.state.enabled is false, so it would never be mounted" }}
{{- end }}
{{- range .Values.allowedHosts }}
{{- if regexMatch "[^A-Za-z0-9._:\\[\\]-]" (toString .) }}
{{- fail (printf "allowedHosts entry %q is not a bare host name or host:port (letters, digits, dot, hyphen and underscore; IPv6 in brackets; no wildcards, schemes, paths, spaces or control characters)" (toString .)) }}
{{- end }}
{{- end }}
{{- end }}

{{/*
workspacePath is where the project is mounted: dir if set, else /workspace,
which is also the image's own WORKDIR and -C default.
*/}}
{{- define "flockdeck.workspacePath" -}}
{{- .Values.dir | default "/workspace" }}
{{- end }}

{{/*
allowedHosts is the value of FLOCKDECK_ALLOWED_HOSTS: the names the server
answers to besides 127.0.0.1. A client reaching the Service sends the Service's
name as its Host header, so the chart lists the four ways the cluster resolves
it (bare names match on any port, which also covers a Service on port 80, where
no port is sent), and whatever allowedHosts adds for an Ingress or gateway.
localhost and 127.0.0.1 are already allowed by flockdeck itself, on any port, so
`kubectl port-forward` needs nothing here.
*/}}
{{- define "flockdeck.allowedHosts" -}}
{{- $name := include "flockdeck.fullname" . }}
{{- $ns := .Release.Namespace }}
{{- $hosts := list $name (printf "%s.%s" $name $ns) (printf "%s.%s.svc" $name $ns) (printf "%s.%s.svc.cluster.local" $name $ns) }}
{{- range .Values.allowedHosts }}
{{- $hosts = append $hosts (toString .) }}
{{- end }}
{{- join "," $hosts }}
{{- end }}

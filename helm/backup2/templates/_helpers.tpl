{{/* Common labels */}}
{{- define "backup2.labels" -}}
helm.sh/chart: {{ printf "%s-%s" .Chart.Name .Chart.Version | replace "+" "_" | trunc 63 | trimSuffix "-" }}
app.kubernetes.io/name: {{ .Chart.Name }}
app.kubernetes.io/instance: {{ .Release.Name }}
app.kubernetes.io/managed-by: {{ .Release.Service }}
app.kubernetes.io/version: {{ .Chart.AppVersion | quote }}
{{- end }}

{{/* Out-of-band Secret holding DB + rclone credentials */}}
{{- define "backup2.secretName" -}}
{{- .Values.existingSecret | default "backup-secrets" -}}
{{- end }}

{{/* Non-secret config ConfigMap */}}
{{- define "backup2.configMapName" -}}backup-config{{- end }}

{{/* CronJob name (shared with the viewer's run-now button) */}}
{{- define "backup2.cronjobName" -}}{{ .Values.cronjobName | default "backup" }}{{- end }}

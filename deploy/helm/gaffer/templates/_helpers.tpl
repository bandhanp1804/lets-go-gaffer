{{- define "gaffer.image" -}}
{{- if .root.Values.image.registry }}{{ .root.Values.image.registry }}/{{ end }}{{ .name }}:{{ .root.Values.image.tag }}
{{- end -}}

{{- define "gaffer.labels" -}}
app.kubernetes.io/part-of: gaffer
app.kubernetes.io/managed-by: {{ .Release.Service }}
{{- end -}}

{{/* Env shared by the API, the worker and the load job. */}}
{{- define "gaffer.env" -}}
envFrom:
  - configMapRef:
      name: gaffer-env
  - secretRef:
      name: gaffer-secrets
env:
  # JSON lines, which the Datadog Agent parses into fields (level, msg, trace ids).
  - name: GAFFER_LOG_FORMAT
    value: json
{{- if not .Values.preview }}
  - name: TEMPORAL_UI
    value: https://{{ .Values.host }}/temporal
{{- end }}
{{- with .Values.telemetry.otlpEndpoint }}
  - name: OTEL_EXPORTER_OTLP_ENDPOINT
    value: {{ . | quote }}
  - name: OTEL_RESOURCE_ATTRIBUTES
    value: {{ printf "deployment.environment=%s,deployment.environment.name=%s,service.version=%s" (include "gaffer.envName" $) (include "gaffer.envName" $) $.Values.image.tag | quote }}
{{- end }}
{{- end -}}

{{/* The environment telemetry is filed under: prod, or the preview's namespace (pr-12). */}}
{{- define "gaffer.envName" -}}
{{- if .Values.preview }}{{ .Release.Namespace }}{{ else }}prod{{ end -}}
{{- end -}}

{{/* A pod's labels: the selector label, and Datadog's unified service tags, which join a pod's
     logs, traces and metrics under one service, environment and version. */}}
{{- define "gaffer.podLabels" -}}
app: {{ .name }}
tags.datadoghq.com/service: {{ .name }}
tags.datadoghq.com/env: {{ include "gaffer.envName" .root }}
tags.datadoghq.com/version: {{ .root.Values.image.tag | quote }}
{{- end -}}

{{/* An HPA on CPU for one deployment. */}}
{{- define "gaffer.hpa" -}}
apiVersion: autoscaling/v2
kind: HorizontalPodAutoscaler
metadata:
  name: {{ .name }}
  labels:
    {{- include "gaffer.labels" .root | nindent 4 }}
spec:
  scaleTargetRef:
    apiVersion: apps/v1
    kind: Deployment
    name: {{ .name }}
  minReplicas: {{ .scaling.min }}
  maxReplicas: {{ .scaling.max }}
  metrics:
    - type: Resource
      resource:
        name: cpu
        target:
          type: Utilization
          averageUtilization: {{ .scaling.cpu }}
{{- end -}}

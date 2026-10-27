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
{{- if not .Values.preview }}
env:
  - name: TEMPORAL_UI
    value: https://{{ .Values.host }}/temporal
{{- end }}
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

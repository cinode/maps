{{- define "osm-machinery.image" }}
image: {{
  printf
    "%s/%s:%s"
    .registry
    .repository
    .tag
  | quote
}}
{{- if .pullPolicy }}
imagePullPolicy: {{ .pullPolicy | quote }}
{{- end }}
{{- end }}
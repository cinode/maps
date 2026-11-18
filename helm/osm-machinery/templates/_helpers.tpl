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

{{/*
Settings of the selected import tuning profile (global.importTuning) as JSON
*/}}
{{- define "osm-machinery.importTuning" }}
{{- $t := $.Values.global.importTuning }}
{{- $p := index $t.profiles $t.profile }}
{{- if not $p }}
{{-   fail (printf "unknown global.importTuning.profile '%s', available profiles: %s" $t.profile (keys $t.profiles | sortAlpha | join ", ")) }}
{{- end }}
{{- $p | toJson }}
{{- end }}

{{/*
Validated type of the generated map: "raster" or "vector"
*/}}
{{- define "osm-machinery.mapType" }}
{{- $mapType := $.Values.mapType | default "raster" }}
{{- if not (has $mapType (list "raster" "vector")) }}
{{-   fail (printf "mapType must be either 'raster' or 'vector', got '%s'" $mapType) }}
{{- end }}
{{- if and (eq $mapType "vector") $.Values.postgres.enabled }}
{{-   fail "mapType 'vector' does not use PostgreSQL, set postgres.enabled=false" }}
{{- end }}
{{- $mapType }}
{{- end }}

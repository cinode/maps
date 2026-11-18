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

{{/*
Regions with vector tiles: enabled and with own OSM extract
*/}}
{{- define "osm-machinery.vectorRegions" }}
{{- $regions := list }}
{{- range $.Values.regions }}
{{-   if and .enabled .pbf (not .skipDownload) }}
{{-     if not .poly }}
{{-       fail (printf "vector map requires polygon (poly) of region '%s'" .name) }}
{{-     end }}
{{-     $regions = append $regions . }}
{{-   end }}
{{- end }}
{{- dict "regions" $regions | toJson }}
{{- end }}

{{/*
Env variable taken from a value or an existing secret, renders nothing
when neither is set. Params: env, cfg, defaultSecret
*/}}
{{- define "osm-machinery.secretEnv" }}
{{- if or .cfg.existingSecret .cfg.value }}
- name: {{ .env | quote }}
  valueFrom:
    secretKeyRef:
      {{- if .cfg.existingSecret }}
      name: {{ .cfg.secretName | required (printf "secretName is required for %s when existingSecret is set to true" .env) | quote }}
      {{- else }}
      name: {{ .cfg.secretName | default .defaultSecret | quote }}
      {{- end }}
      key: {{ .cfg.secretKey | quote }}
{{- end }}
{{- end }}

{{/*
Extra Planetiler arguments of the vector map, quoted for the shell
*/}}
{{- define "osm-machinery.planetilerArgs" }}
{{- range $.Values.vectorMap.planetiler.extraArgs }} {{ . | squote }}{{ end }}
{{- end }}

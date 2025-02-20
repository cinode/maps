{{- /* Create a filtered list of regions to support */}}
{{- $regions := list }}
{{- range $.Values.regions }}
{{-   if and .enabled ( not ( .skipDownload | default false ) ) }}
{{-     $regions = append $regions ( dict
            "enabled" .enabled
            "name"    .name
            "pbf"     .pbf
            "poly"    .poly
        ) }}
{{-   end }}
{{- end }}
{{- $regionsChecksum := toJson $regions | sha256sum }}
#!/bin/sh

set -eu

mkdir -p /data/download/
mkdir -p /data/database/
mkdir -p /data/tiles/
mkdir -p /data/style/

# Cleanup any old download files
[ "$( ls -A /data/download/ )" ] && rm -rf /data/download/*

# Small fix to the update script to use pgdb password
cp $(which openstreetmap-tiles-update-expire.sh ) /tmp/otue.sh
sed -i 's/^TRIM_OPTIONS=.*$/TRIM_OPTIONS="-d $DBNAME --password"/g' /tmp/otue.sh

if [ z"$( cat /data/database/planet-import-complete 2>/dev/null || true )" = z"{{ $regionsChecksum }}" ]; then
    # The data has been imported, check if there's any diff and apply it
    echo "INFO: Synchronizing osm changes"

    touch /var/log/tiles/run.log;       tail -f /var/log/tiles/run.log       >> /proc/1/fd/1 &
    touch /var/log/tiles/osmosis.log;   tail -f /var/log/tiles/osmosis.log   >> /proc/1/fd/1 &
    touch /var/log/tiles/expiry.log;    tail -f /var/log/tiles/expiry.log    >> /proc/1/fd/1 &
    touch /var/log/tiles/osm2pgsql.log; tail -f /var/log/tiles/osm2pgsql.log >> /proc/1/fd/1 &

    sh /tmp/otue.sh

    exit 0
fi

# The data has not yet been imported or configuration of regions has changed,
# start from scratch by importing the latest snapshot of the data

echo "INFO: Doing initial data import"

rm /data/database/planet-import-complete 2>/dev/null || true

[ "$( ls -A /data/database/ )" ] && rm -rf /data/database/*
[ "$( ls -A /data/tiles/    )" ] && rm -rf /data/tiles/*
[ "$( ls -A /data/style/    )" ] && rm -rf /data/style/*

echo "INFO: Creating style data"
cp -r /home/renderer/src/openstreetmap-carto-backup/* /data/style/
cd /data/style/
carto ${NAME_MML:-project.mml} > mapnik.xml

{{- range $index, $region := $regions }}

echo "INFO: Preparing data for region {{ $region.name }}"

{{- $suffix := "" }}
{{- if ne $index 0 }}{{ $suffix = printf "_%d" $index }}{{ end }}

echo "INFO: Download PBF file for region {{ $region.name }}"
wget ${WGET_ARGS:-} {{ $region.pbf | quote }} -O /data/download/region{{ $suffix }}.osm.pbf

echo "INFO: Download POLY file for region {{ $region.name }}"
wget ${WGET_ARGS:-} {{ $region.poly | quote }} -O /data/database/region{{ $suffix }}.poly

echo "INFO: Determining replication timestamp for region {{ $region.name }}"
osmium fileinfo -g header.option.osmosis_replication_timestamp /data/download/region{{ $suffix }}.osm.pbf >> /data/download/replication_timestamps.txt

{{- if ne $index 0 }}

echo "INFO: Merging in new pbf file from region {{ $region.name }}"
osmium merge /data/download/region.osm.pbf /data/download/region{{ $suffix }}.osm.pbf -o /data/download/region-merged.osm.pbf
mv -f /data/download/region-merged.osm.pbf /data/download/region.osm.pbf
rm -f /data/download/region{{ $suffix }}.osm.pbf

echo "INFO: Merging in new poly file from region {{ $region.name }}"
( echo "" && cat /data/database/region{{ $suffix }}.poly ) >> /data/database/region.poly
rm -f /data/database/region{{ $suffix }}.poly

{{- end }}
{{- end }}

echo "INFO: Finished processing regions"

REPLICATION_TIMESTAMP="$(cat /data/download/replication_timestamps.txt | sort | head -n 1)"
echo "INFO: Oldest replication timestamp: $REPLICATION_TIMESTAMP"

# initial setup of osmosis workspace (for consecutive updates)
/tmp/otue.sh $REPLICATION_TIMESTAMP || true

# Import data
osm2pgsql -d gis --create --slim -G --hstore  \
    --tag-transform-script /data/style/${NAME_LUA:-openstreetmap-carto.lua}  \
    --number-processes ${THREADS:-4}  \
    -S /data/style/${NAME_STYLE:-openstreetmap-carto.style}  \
    /data/download/region.osm.pbf  \
    ${OSM2PGSQL_EXTRA_ARGS:-}  \
    ;

rm -rf /data/download/*

# Create indexes
if [ -f "/data/style/${NAME_SQL:-indexes.sql}" ]; then
    psql -d gis -f "/data/style/${NAME_SQL:-indexes.sql}"
fi

# Import external data
python3 /data/style/scripts/get-external-data.py -c /data/style/external-data.yml -D /data/style/data

# Mark the import as done
echo -n "{{ $regionsChecksum }}" > /data/database/planet-import-complete

echo "INFO: Initialization complete!"

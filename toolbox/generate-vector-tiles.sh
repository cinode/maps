#!/bin/bash
# Generates vector tiles from an OSM extract with Planetiler
#
# Usage: generate-vector-tiles.sh <name> <osm-file> <min-zoom> <max-zoom> [planetiler args...]
#
# Tiles are written to $DATA_DIR/tiles/<name> (DATA_DIR defaults to /data),
# previous tiles are replaced only after a successful run. Additional sources
# (water polygons, Natural Earth) are downloaded to $DATA_DIR/sources once.
#
# Planetiler is started with PLANETILER (defaults to the launcher of the
# maps-vector-generator image), java options can be set with JAVA_TOOL_OPTIONS.
set -euo pipefail

if [ $# -lt 4 ]; then
    sed -n '2,/^set /{/^#/s/^# \{0,1\}//p}' "$0" >&2
    exit 1
fi

NAME="$1"
OSM_FILE="$2"
MIN_ZOOM="$3"
MAX_ZOOM="$4"
shift 4

DATA="${DATA_DIR:-/data}"
OUT="$DATA/tiles/$NAME"
TMP="$DATA/tmp/$NAME"
read -r -a PLANETILER <<<"${PLANETILER:-java -cp @/app/jib-classpath-file com.onthegomap.planetiler.Main}"

mkdir -p "$DATA/sources" "$DATA/tiles" "$DATA/tmp"

echo "Generating tiles '$NAME' (z$MIN_ZOOM-z$MAX_ZOOM)"
rm -rf "$OUT.new" "$TMP"
"${PLANETILER[@]}" \
    --osm_path="$OSM_FILE" \
    --download \
    --download_dir="$DATA/sources" \
    --tmpdir="$TMP" \
    --output="$OUT.new?format=files" \
    --tile_compression=none \
    --minzoom="$MIN_ZOOM" \
    --maxzoom="$MAX_ZOOM" \
    "$@"

rm -rf "$OUT" "$TMP"
mv "$OUT.new" "$OUT"
echo "Tiles '$NAME' ready"

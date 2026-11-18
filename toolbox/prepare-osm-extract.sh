#!/bin/bash
# Downloads OSM extracts of regions and combines them into a single extract
#
# Usage: prepare-osm-extract.sh <name> <pbf-url> <poly-url> [<pbf-url> <poly-url>...]
#
# Each region is given by its OSM extract (.osm.pbf) and the polygon of its
# area (.poly), e.g. from https://download.geofabrik.de/. The result is
# written to $DATA_DIR/osm/<name>.osm.pbf and $DATA_DIR/osm/<name>.poly
# (DATA_DIR defaults to /data).
#
# Extracts of neighbouring regions overlap a bit along shared borders, they
# are merged with osmium which keeps a single copy of every OSM object, so
# that tiles along borders contain data of all regions. Polygons are merged
# into a single multi-polygon.
#
# Downloaded files are kept in $DATA_DIR/osm and only fetched again when a
# newer version is published, the merge is skipped if nothing has changed.
set -euo pipefail

if [ $# -lt 3 ] || [ $(( ($# - 1) % 2 )) -ne 0 ]; then
    sed -n '2,/^set /{/^#/s/^# \{0,1\}//p}' "$0" >&2
    exit 1
fi

NAME="$1"
shift

DATA="${DATA_DIR:-/data}"
OSM="$DATA/osm"
OUT_PBF="$OSM/$NAME.osm.pbf"
OUT_POLY="$OSM/$NAME.poly"
STAMP="$OSM/$NAME.inputs"

mkdir -p "$OSM"

fetch() {
    echo "Fetching $1"
    # Download servers (e.g. Geofabrik mirrors) occasionally refuse connections
    # or drop them, retry instead of failing the whole run
    (cd "$OSM" && wget --no-verbose --timestamping \
        --tries=5 --retry-connrefused --waitretry=30 "$1")
}

PBFS=()
POLYS=()
while [ $# -gt 0 ]; do
    fetch "$1"
    fetch "$2"
    PBFS+=("$OSM/$(basename "$1")")
    POLYS+=("$OSM/$(basename "$2")")
    shift 2
done

# Inputs with their versions, the merge is redone when any of them changes
inputs() {
    stat -c '%n %s %Y' "${PBFS[@]}" "${POLYS[@]}"
}

if [ -e "$OUT_PBF" ] && [ -e "$OUT_POLY" ] && [ -f "$STAMP" ] && inputs | cmp -s - "$STAMP"; then
    echo "Extract '$NAME' is up to date"
    exit 0
fi

rm -f "$STAMP"
if [ ${#PBFS[@]} -eq 1 ]; then
    ln -sfn "$(basename "${PBFS[0]}")" "$OUT_PBF"
else
    echo "Merging ${#PBFS[@]} extracts into '$NAME'"
    osmium merge --overwrite --no-progress -o "$OUT_PBF.tmp" -f pbf "${PBFS[@]}"
    mv -f "$OUT_PBF.tmp" "$OUT_PBF"
fi

# Sections of all polygons in one file, renamed so that names are unique,
# holes (sections starting with '!') stay holes
{
    echo "$NAME"
    i=0
    for poly in "${POLYS[@]}"; do
        i=$((i + 1))
        awk -v prefix="$i-" '
            NR == 1 { next }                          # polygon name
            !in_section && $1 == "END" { next }       # end of the file
            !in_section {
                hole = substr($1, 1, 1) == "!"
                print (hole ? "!" prefix substr($1, 2) : prefix $1)
                in_section = 1
                next
            }
            $1 == "END" { in_section = 0 }
            { print }
        ' "$poly"
    done
    echo "END"
} > "$OUT_POLY.tmp"
mv -f "$OUT_POLY.tmp" "$OUT_POLY"

inputs > "$STAMP"
echo "Extract '$NAME' ready"

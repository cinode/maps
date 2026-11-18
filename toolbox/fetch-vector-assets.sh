#!/usr/bin/env bash
# Prepares the client-side assets directory for `maps-vector-uploader` (CINODE_VECTOR_ASSETS_DIR):
#
#   <out>/maplibre/  - MapLibre GL JS (ESM build + worker + css)
#   <out>/fonts/     - glyph PBFs used by the style (Noto Sans)
#   <out>/sprites/   - OSM Bright sprites
#
# Usage: toolbox/fetch-vector-assets.sh <output-dir>
set -euo pipefail

MAPLIBRE_VERSION="6.16.0"
OSM_BRIGHT_VERSION="v1.11"
FONTS_VERSION="v2.0"

OUT="${1:?usage: $0 <output-dir>}"
mkdir -p "$OUT"
OUT="$(cd "$OUT" && pwd)"

TMP="$(mktemp -d)"
trap 'rm -rf "$TMP"' EXIT

echo "Fetching MapLibre GL JS ${MAPLIBRE_VERSION}"
mkdir -p "$TMP/maplibre" "$OUT/maplibre"
(cd "$TMP/maplibre" && npm pack --silent "maplibre-gl@${MAPLIBRE_VERSION}" >/dev/null && tar xzf ./*.tgz)
for f in maplibre-gl.mjs maplibre-gl-worker.mjs maplibre-gl-shared.mjs maplibre-gl.css; do
    cp "$TMP/maplibre/package/dist/$f" "$OUT/maplibre/"
done
cp "$TMP/maplibre/package/LICENSE.txt" "$OUT/maplibre/"

echo "Fetching OSM Bright ${OSM_BRIGHT_VERSION} sprites"
mkdir -p "$TMP/osm-bright" "$OUT/sprites"
curl -sSfL -o "$TMP/osm-bright.zip" \
    "https://github.com/openmaptiles/osm-bright-gl-style/releases/download/${OSM_BRIGHT_VERSION}/${OSM_BRIGHT_VERSION}.zip"
unzip -q -o "$TMP/osm-bright.zip" -d "$TMP/osm-bright"
cp "$TMP/osm-bright"/sprite*.{json,png} "$OUT/sprites/"

echo "Fetching Noto Sans fonts ${FONTS_VERSION}"
mkdir -p "$TMP/fonts" "$OUT/fonts"
curl -sSfL -o "$TMP/noto-sans.zip" \
    "https://github.com/openmaptiles/fonts/releases/download/${FONTS_VERSION}/noto-sans.zip"
unzip -q -o "$TMP/noto-sans.zip" -d "$TMP/fonts"
for font in "Noto Sans Regular" "Noto Sans Bold" "Noto Sans Italic"; do
    rm -rf "${OUT:?}/fonts/$font"
    cp -r "$TMP/fonts/$font" "$OUT/fonts/"
done

echo "Assets ready in $OUT"

# Maps

Set of tools to synchronize Cinode maps using OpenStreetMap data as the source.

To view cinode maps visit <https://maps.cinodenet.org/>.

More info about this project can be found on the Cinode blog:

- [Strongman](https://blog.cinodenet.org/post/2023-08-27-strongman/)
- [Cinobelix chronicles - Librarian](https://blog.cinodenet.org/post/2024-01-15-cinobelix-chronicles-librarian/)
- [Cinode maps - reaching the scale](https://blog.cinodenet.org/post/2025-01-11-cinode-maps-reaching-the-scale/)
- [Cinode maps - PostgreSQL Tuning](https://blog.cinodenet.org/post/2025-03-03-cinode-maps-postgresql-tuning/)
- [Cinode maps - First, failed multi-region setup](https://blog.cinodenet.org/post/2025-04-26-cinode-maps-first-failed-multi-region-setup/)

## Import tuning

The `osm-machinery` helm chart imports OSM data into PostgreSQL with
osm2pgsql. By default it uses the `minimal` profile which is enough for small
extracts such as Luxembourg (the default region) and local tests. For large
extracts on a dedicated machine (at least 16GB of RAM) switch to the
`optimized` profile:

```yaml
global:
  importTuning:
    profile: optimized
```

The most important setting is the osm2pgsql node cache
(`global.importTuning.profiles.<profile>.osm2pgsql.cache`, in MB). Node
locations missing in the cache are looked up from the database which makes
processing of ways extremely slow. Ideally it holds all nodes of the imported
extract (osm2pgsql 1.6 needs ~5-9 bytes per node) while leaving enough memory
for PostgreSQL. See `global.importTuning` in
[values.yaml](helm/osm-machinery/values.yaml) for all settings, each of them
can be overridden individually.

Changing PostgreSQL server settings restarts PostgreSQL which aborts a running
import, the import is then started again from scratch. `noSync: true`
additionally disables crash safety of PostgreSQL, a crash can then corrupt
the database which has to be imported again.

## Vector map (prototype)

`maps-vector-uploader` publishes a vector tiles based map: pre-generated
tiles split by zoom level (same `{z}/{x}/{y}` layout as the raster map),
styled and rendered in the browser with MapLibre GL JS.

Resulting cinode directory:

```text
/index.html               MapLibre viewer
/overzoom.mjs             client-side over-zoom for regions with lower max zoom
/style.json               map style (OpenMapTiles schema, derived from OSM Bright
                          with OSM Carto-like colors, road widths and house numbers)
/maplibre/                MapLibre GL JS
/fonts/{fontstack}/{range}.pbf
/sprites/sprite*.{json,png}
/tiles/coverage.json      per-zoom bounding rectangles of existing tiles
/tiles/{z}/{x}/{y}.pbf    uncompressed Mapbox Vector Tiles
```

### 1. Generate tiles with Planetiler

[Planetiler](https://github.com/onthegomap/planetiler) builds OpenMapTiles-schema
tiles straight from an `.osm.pbf` file (no database). Tiles must be written as
plain files and **without gzip** (the cinode proxy does not set
`Content-Encoding`).

Low zoom levels for the whole world (Natural Earth + water polygons):

```bash
java -Xmx6g -jar planetiler.jar --area=luxembourg --bounds=world --download \
    --output="tiles-world?format=files" --tile_compression=none --maxzoom=6
```

Detailed region(s):

```bash
java -Xmx6g -jar planetiler.jar --area=luxembourg --download \
    --output="tiles-luxembourg?format=files" --tile_compression=none --maxzoom=14
```

### 2. Fetch client-side assets

```bash
toolbox/fetch-vector-assets.sh assets
```

### 3. Upload to cinode

```bash
CINODE_DATASTORE=<datastore> \
CINODE_MAPTILES_NEW_WRITERINFO=1 \
CINODE_VECTOR_TILES_DIRS=tiles-world:tiles-luxembourg \
CINODE_VECTOR_ASSETS_DIR=assets \
go run ./cmd/maps-vector-uploader
```

Tiles from later directories override earlier ones. Use
`CINODE_MAPTILES_WRITERINFO=<writer info>` instead of
`CINODE_MAPTILES_NEW_WRITERINFO` to update an existing map. Unchanged tiles
produce the same blobs, so an update only stores what actually changed.

### Different max zoom per region

MapLibre supports a single max zoom per source. When a tile doesn't exist
(e.g. z10 outside of detailed regions), `overzoom.mjs` fetches the closest
existing ancestor tile (using `tiles/coverage.json` to skip non-existing ones)
and rescales its geometry on the client. Above the highest generated zoom
(14), MapLibre over-zooms natively.

### Running in Kubernetes

The `osm-machinery` helm chart generates either map type, selected with
`mapType`:

* `raster` (default) - PostgreSQL + tile server, uploaded by
  `maps-tile-uploader`,
* `vector` - a single CronJob running the steps one after another:
  downloading extracts (`pbf` + `poly`) of enabled regions and merging them
  into one extract with osmium, generating tiles with Planetiler (low zoom
  levels for the whole world, then all regions at once, limited to their
  polygons) and uploading them with `maps-vector-uploader`, plus an
  optional viewer (cinode web proxy). Merging the extracts first makes tiles
  along borders of neighbouring regions contain data of all of them.
  Everything runs in the `maps-vector-generator` image. Requires
  `postgres.enabled=false`.

See `vectorMap` in [values.yaml](helm/osm-machinery/values.yaml) for all
options. By default the map is stored in a local datastore on the data
volume and the map link is created on the first run; use
`vectorMap.datastore` and `vectorMap.writerInfo` to publish to a real
datastore.

## Local testing in minikube

`toolbox/minikube-test.sh` starts minikube (if needed), builds images
from the working tree, loads them into minikube and deploys the chart with
one of the presets from [toolbox/minikube](toolbox/minikube):

```bash
# Raster map (default)
toolbox/minikube-test.sh

# Vector map of Luxembourg - quick test, a few minutes
toolbox/minikube-test.sh vector-luxembourg

# Vector map of Poland - realistic dataset, ~30 min, needs a bigger minikube
MINIKUBE_MEMORY=10g toolbox/minikube-test.sh vector-poland

# Vector map of two neighbouring regions (Lubuskie, Brandenburg) - checks
# that tiles along the border contain data of both, ~10 min
toolbox/minikube-test.sh vector-border
```

For vector presets the map generation starts right away.
`toolbox/minikube-view.sh` follows it and, once the first upload is done,
serves the map on <http://localhost:8080/>:

```bash
toolbox/minikube-view.sh

# Also reachable from other devices in the local network, e.g. a phone
ADDRESS=0.0.0.0 toolbox/minikube-view.sh
```

Running `minikube-test.sh` again deploys the current code and starts a new
generation, the same map link is updated. Downloaded data is kept on the
data volume, which also holds the map link and is kept when the release
is removed. To start from scratch remove the release
(`helm uninstall cinode-maps --kube-context minikube`) and the volume
(`minikube kubectl -- delete pvc cinode-maps-vector-data`) or the whole
minikube (`minikube delete`).

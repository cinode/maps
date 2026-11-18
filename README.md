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

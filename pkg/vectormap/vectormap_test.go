package vectormap

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"testing"

	"github.com/cinode/go/pkg/blenc"
	"github.com/cinode/go/pkg/cinodefs"
	"github.com/cinode/go/pkg/datastore"
	"github.com/stretchr/testify/require"
)

func testLog() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

// writeFile creates a file at a slash-separated path relative to dir
func writeFile(t *testing.T, dir, relPath, data string) {
	t.Helper()
	path := filepath.Join(dir, filepath.FromSlash(relPath))
	require.NoError(t, os.MkdirAll(filepath.Dir(path), 0o755))
	require.NoError(t, os.WriteFile(path, []byte(data), 0o644))
}

func TestGenerate(t *testing.T) {
	ctx := context.Background()

	world := t.TempDir()
	region := t.TempDir()
	assets := t.TempDir()

	writeFile(t, world, "0/0/0.pbf", "world-0")
	writeFile(t, world, "1/0/1.pbf", "world-1")
	writeFile(t, world, "metadata.json", "{}")
	writeFile(t, region, "1/0/1.pbf", "region-1")
	writeFile(t, region, "2/1/1.pbf", "region-2")
	writeFile(t, assets, "maplibre/maplibre-gl.mjs", "js")
	writeFile(t, assets, "fonts/Noto Sans Regular/0-255.pbf", "glyphs")

	be := blenc.FromDatastore(datastore.InMemory())
	fs, err := cinodefs.New(ctx, be, cinodefs.NewRootStaticDirectory())
	require.NoError(t, err)

	err = Generate(ctx, testLog(), Config{
		TilesDirs:   []string{world, region},
		AssetsDir:   assets,
		Parallelism: 4,
	}, fs)
	require.NoError(t, err)
	require.NoError(t, fs.Flush(ctx))

	read := func(path ...string) string {
		rc, err := fs.OpenEntryData(ctx, path)
		require.NoError(t, err)
		defer func() { require.NoError(t, rc.Close()) }()
		data, err := io.ReadAll(rc)
		require.NoError(t, err)
		return string(data)
	}

	require.Equal(t, "world-0", read("tiles", "0", "0", "0.pbf"))
	require.Equal(t, "region-1", read("tiles", "1", "0", "1.pbf"), "later dir must override")
	require.Equal(t, "region-2", read("tiles", "2", "1", "1.pbf"))
	require.Equal(t, "js", read("maplibre", "maplibre-gl.mjs"))
	require.Equal(t, "glyphs", read("fonts", "Noto Sans Regular", "0-255.pbf"))
	require.Contains(t, read("index.html"), "maplibre-gl.mjs")
	require.Contains(t, read("style.json"), "tiles/{z}/{x}/{y}.pbf")

	require.JSONEq(t,
		`{"zooms":{"0":[[0,0,0,0]],"1":[[0,1,0,1],[0,1,0,1]],"2":[[1,1,1,1]]}}`,
		read("tiles", "coverage.json"),
	)

	_, err = fs.FindEntry(ctx, []string{"tiles", "metadata.json"})
	require.ErrorIs(t, err, cinodefs.ErrEntryNotFound)

	ep, err := fs.FindEntry(ctx, []string{"tiles", "0", "0", "0.pbf"})
	require.NoError(t, err)
	require.Equal(t, TileMimeType, ep.MimeType())
}

func TestGenerateRejectsGzipTiles(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	writeFile(t, dir, "0/0/0.pbf", "\x1f\x8bgzipped")

	be := blenc.FromDatastore(datastore.InMemory())
	fs, err := cinodefs.New(ctx, be, cinodefs.NewRootStaticDirectory())
	require.NoError(t, err)

	err = Generate(ctx, testLog(), Config{TilesDirs: []string{dir}}, fs)
	require.ErrorContains(t, err, "gzip")
}

func TestGenerateDuplicateTilesInParallel(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	for x := range 16 {
		for y := range 16 {
			writeFile(t, dir, fmt.Sprintf("4/%d/%d.pbf", x, y), "ocean")
		}
	}

	be := blenc.FromDatastore(datastore.InMemory())
	fs, err := cinodefs.New(ctx, be, cinodefs.NewRootStaticDirectory())
	require.NoError(t, err)

	err = Generate(ctx, testLog(), Config{
		TilesDirs:   []string{dir},
		Parallelism: 16,
	}, fs)
	require.NoError(t, err)
	require.NoError(t, fs.Flush(ctx))

	rc, err := fs.OpenEntryData(ctx, []string{"tiles", "4", "15", "15.pbf"})
	require.NoError(t, err)
	defer func() { require.NoError(t, rc.Close()) }()
	data, err := io.ReadAll(rc)
	require.NoError(t, err)
	require.Equal(t, "ocean", string(data))
}

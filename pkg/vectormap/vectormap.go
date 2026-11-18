// Package vectormap publishes vector tiles based maps into cinode.
package vectormap

import (
	"bytes"
	"context"
	"embed"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"sync"
	"time"

	"github.com/cinode/go/pkg/cinodefs"
	"github.com/cinode/go/pkg/cinodefs/uploader"
	"github.com/cinode/go/pkg/datastore"
)

//go:embed static_data/vector
var vectorStatic embed.FS

// tilesDir is the directory with tiles inside the generated site
const tilesDir = "tiles"

// TileMimeType is the mime type used for stored vector tiles
const TileMimeType = "application/vnd.mapbox-vector-tile"

// Config is the configuration of the generated vector map
type Config struct {
	// AssetsDir contains client-side assets that are copied as-is to the
	// root of the generated site (maplibre/, fonts/, sprites/ ...)
	AssetsDir string

	// TilesDirs are directories with vector tiles in {z}/{x}/{y}.pbf layout
	// (e.g. Planetiler output with `format=files`). When the same tile exists
	// in more than one directory, the one from the later directory wins.
	// Tiles must not be gzip-compressed since cinode proxy does not set
	// Content-Encoding header.
	TilesDirs []string

	// Parallelism is the number of concurrent tile uploads
	Parallelism int
}

// Generate builds the whole vector map site:
//
//	/index.html           - MapLibre GL JS based viewer
//	/overzoom.mjs         - client-side over-zooming of missing tiles
//	/style.json           - map style
//	/tiles/{z}/{x}/{y}.pbf - vector tiles
//	/...                  - assets from AssetsDir
func Generate(
	ctx context.Context,
	log *slog.Logger,
	cfg Config,
	cfs cinodefs.FS,
) error {
	if cfg.AssetsDir != "" {
		log.InfoContext(ctx, "Uploading assets", "dir", cfg.AssetsDir)
		err := uploader.UploadStaticDirectory(ctx, os.DirFS(cfg.AssetsDir), cfs)
		if err != nil {
			return fmt.Errorf("failed to upload assets: %w", err)
		}
	}

	staticFS, err := fs.Sub(vectorStatic, "static_data/vector")
	if err != nil {
		return err
	}
	if err := uploader.UploadStaticDirectory(ctx, staticFS, cfs); err != nil {
		return fmt.Errorf("failed to upload viewer files: %w", err)
	}

	return UploadTiles(ctx, log, cfg, cfs, []string{tilesDir})
}

type tileFile struct {
	src     string
	z, x, y int
}

var tileFileRE = regexp.MustCompile(`^(\d+)/(\d+)/(\d+)\.pbf$`)

// TileRect is an inclusive range of tiles at a single zoom level:
// [minX, minY, maxX, maxY]
type TileRect [4]int

// TilesCoverage describes which tiles exist, it is stored as
// tiles/coverage.json and used by the viewer to find the best tile to
// over-zoom without probing for missing tiles. Tiles inside rectangles may
// still be missing (e.g. empty tiles), the viewer handles that as well.
type TilesCoverage struct {
	// Zooms maps zoom level to bounding rectangles of existing tiles,
	// one rectangle per source tiles directory.
	Zooms map[int][]TileRect `json:"zooms"`
}

// collectTiles finds all tiles in given directories, later directories
// override tiles from earlier ones. Result is grouped by zoom level.
func collectTiles(dirs []string) (map[int][]tileFile, *TilesCoverage, error) {
	type key struct{ z, x, y int }
	all := map[key]string{}
	coverage := &TilesCoverage{Zooms: map[int][]TileRect{}}

	for _, dir := range dirs {
		dirRects := map[int]TileRect{}
		err := filepath.WalkDir(dir, func(path string, d fs.DirEntry, err error) error {
			if err != nil || d.IsDir() {
				return err
			}
			rel, err := filepath.Rel(dir, path)
			if err != nil {
				return err
			}
			m := tileFileRE.FindStringSubmatch(filepath.ToSlash(rel))
			if m == nil {
				// metadata.json and other non-tile files
				return nil
			}
			z, _ := strconv.Atoi(m[1])
			x, _ := strconv.Atoi(m[2])
			y, _ := strconv.Atoi(m[3])
			all[key{z, x, y}] = path

			r, found := dirRects[z]
			if !found {
				r = TileRect{x, y, x, y}
			}
			dirRects[z] = TileRect{min(r[0], x), min(r[1], y), max(r[2], x), max(r[3], y)}
			return nil
		})
		if err != nil {
			return nil, nil, fmt.Errorf("failed to scan tiles dir %s: %w", dir, err)
		}
		for z, r := range dirRects {
			coverage.Zooms[z] = append(coverage.Zooms[z], r)
		}
	}

	ret := map[int][]tileFile{}
	for k, src := range all {
		ret[k.z] = append(ret[k.z], tileFile{src: src, z: k.z, x: k.x, y: k.y})
	}
	for z := range ret {
		sort.Slice(ret[z], func(i, j int) bool {
			a, b := ret[z][i], ret[z][j]
			if a.x != b.x {
				return a.x < b.x
			}
			return a.y < b.y
		})
	}
	return ret, coverage, nil
}

// UploadTiles uploads vector tiles from config's TilesDirs to
// the given path. The destination directory is reset first so that
// the result is always a snapshot of the source directories.
func UploadTiles(
	ctx context.Context,
	log *slog.Logger,
	cfg Config,
	cfs cinodefs.FS,
	basePath []string,
) error {
	tiles, coverage, err := collectTiles(cfg.TilesDirs)
	if err != nil {
		return err
	}

	zooms := make([]int, 0, len(tiles))
	for z := range tiles {
		zooms = append(zooms, z)
	}
	sort.Ints(zooms)

	if err := cfs.ResetDir(ctx, basePath); err != nil {
		return err
	}

	coverageJSON, err := json.Marshal(coverage)
	if err != nil {
		return err
	}
	_, err = cfs.SetEntryFile(ctx,
		append(append([]string{}, basePath...), "coverage.json"),
		bytes.NewReader(coverageJSON),
	)
	if err != nil {
		return fmt.Errorf("failed to upload tiles coverage: %w", err)
	}

	parallelism := max(cfg.Parallelism, 1)

	for _, z := range zooms {
		zLog := log.With("z", z)
		zLog.InfoContext(ctx, "Uploading zoom level", "tiles", len(tiles[z]))

		if err := uploadTilesParallel(ctx, zLog, cfs, basePath, tiles[z], parallelism); err != nil {
			return err
		}

		// Persist each finished zoom level to limit memory usage
		if err := cfs.Flush(ctx); err != nil {
			return err
		}
	}

	return nil
}

func uploadTilesParallel(
	ctx context.Context,
	log *slog.Logger,
	cfs cinodefs.FS,
	basePath []string,
	tiles []tileFile,
	parallelism int,
) error {
	ctx, cancel := context.WithCancelCause(ctx)
	defer cancel(nil)

	type result struct {
		ep *cinodefs.Entrypoint
		t  tileFile
	}

	jobs := make(chan tileFile)
	results := make(chan result)

	// Blob creation is independent of the directory tree and can run
	// concurrently, modifying the tree is done in a single goroutine below.
	wg := sync.WaitGroup{}
	for range parallelism {
		wg.Go(func() {
			for t := range jobs {
				ep, err := uploadTileBlob(ctx, cfs, t)
				if err != nil {
					cancel(err)
					return
				}
				select {
				case results <- result{ep: ep, t: t}:
				case <-ctx.Done():
					return
				}
			}
		})
	}

	go func() {
		defer close(jobs)
		for _, t := range tiles {
			select {
			case jobs <- t:
			case <-ctx.Done():
				return
			}
		}
	}()

	go func() {
		wg.Wait()
		close(results)
	}()

	done := 0
	for r := range results {
		path := append(
			append([]string{}, basePath...),
			strconv.Itoa(r.t.z), strconv.Itoa(r.t.x), strconv.Itoa(r.t.y)+".pbf",
		)
		if err := cfs.SetEntry(ctx, path, r.ep); err != nil {
			cancel(err)
			break
		}
		done++
		if done%1000 == 0 {
			log.InfoContext(ctx, "Upload progress", "done", done, "total", len(tiles))
		}
	}

	// drain in case of an early exit
	for range results {
	}

	return context.Cause(ctx)
}

func uploadTileBlob(ctx context.Context, cfs cinodefs.FS, t tileFile) (*cinodefs.Entrypoint, error) {
	data, err := os.ReadFile(t.src)
	if err != nil {
		return nil, err
	}
	if len(data) >= 2 && data[0] == 0x1f && data[1] == 0x8b {
		return nil, fmt.Errorf(
			"tile %s is gzip-compressed, generate tiles without compression"+
				" (e.g. planetiler --tile_compression=none)", t.src,
		)
	}

	// Blob names are content-addressed so identical tiles (e.g. water-only
	// ones) map to the same blob. When such tiles are uploaded concurrently,
	// the datastore rejects all but one writer - retry, by then the blob
	// is usually already in place.
	delay := 10 * time.Millisecond
	for retry := 0; ; retry++ {
		ep, err := cfs.CreateFileEntrypoint(ctx, bytes.NewReader(data), cinodefs.SetMimeType(TileMimeType))
		if !errors.Is(err, datastore.ErrUploadInProgress) || retry >= 20 {
			return ep, err
		}
		select {
		case <-time.After(delay):
		case <-ctx.Done():
			return nil, ctx.Err()
		}
		delay = min(2*delay, time.Second)
	}
}

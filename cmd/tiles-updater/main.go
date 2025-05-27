package main

import (
	"context"
	_ "embed"
	"fmt"
	"log"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/cinode/go/pkg/blenc"
	"github.com/cinode/go/pkg/cinodefs"
	"github.com/cinode/go/pkg/datastore"
	"github.com/cinode/go/pkg/utilities/golang"
	"github.com/cinode/osm-machinery/pkg/geo"

	"gopkg.in/yaml.v3"
)

//go:embed defaultConfig.yaml
var defaultConfig string

const usageStr = `
This program uploads map tiles from OpenStreetMap-compatible data source
to Cinode datastore. It is configured through environment variables:

 * CINODE_DATASTORE - address of the target datastore, required
 * CINODE_MAPTILES_WRITERINFO - writerinfo of map tiles entrypoint
 * CINODE_MAPTILES_NEW_WRITERINFO - create new writerinfo
 * CINODE_MAPTILES_CONFIG - additional yaml configuration
`

func usage(msg string) {
	for _, line := range strings.Split(usageStr, "\n") {
		log.Println(line)
	}
	log.Fatalf("Error: %s\n", msg)
}

func main() {
	ctx := context.Background()

	var (
		datastoreAddr = os.Getenv("CINODE_DATASTORE")
		writerInfo    = os.Getenv("CINODE_MAPTILES_WRITERINFO")
		newWriterInfo = os.Getenv("CINODE_MAPTILES_NEW_WRITERINFO") != ""
		cfgYaml       = os.Getenv("CINODE_MAPTILES_CONFIG")
	)

	if datastoreAddr == "" {
		usage("missing CINODE_DATASTORE env variable")
	}

	if writerInfo == "" && !newWriterInfo {
		usage("either CINODE_MAPTILES_WRITERINFO or CINODE_MAPTILES_NEW_WRITERINFO env variable must be specified")
	}

	if cfgYaml == "" {
		cfgYaml = defaultConfig
	}

	ds, err := datastore.FromLocation(datastoreAddr)
	if err != nil {
		usage("failed to open datastore: " + err.Error())
	}

	be := blenc.FromDatastore(ds)

	wiOpt := cinodefs.RootWriterInfoString(writerInfo)

	if newWriterInfo {
		wiOpt = cinodefs.NewRootDynamicLink()
	}

	fs, err := cinodefs.New(
		ctx,
		be,
		wiOpt,
	)
	if err != nil {
		usage("failed to open cinodefs root: " + err.Error())
	}

	cfg := Config{}

	err = yaml.Unmarshal([]byte(cfgYaml), &cfg)
	if err != nil {
		usage("failed to parse additional configuration: " + err.Error())
	}

	if newWriterInfo {
		fmt.Printf("Created new workspace:\n")
		fmt.Printf("  Entrypoint: %s\n", golang.Must(fs.RootEntrypoint()))
		fmt.Printf("  WriterInfo: %s\n", golang.Must(fs.RootWriterInfo(ctx)))
	}

	gen := tilesGenerator{
		cfg: cfg,
		fs:  fs,
		log: slog.Default(),
	}

	err = gen.Process(ctx)
	if err != nil {
		log.Fatal(err)
	}
}

type DetailedRegionConfig struct {
	Name    string   `yaml:"name"`
	BBox    geo.BBox `yaml:"geoBBox"`
	MaxZoom int      `yaml:"maxZoom"`
}

type Config struct {
	URLTemplate                  string                 `yaml:"urlTemplate"`
	MinZoom                      int                    `yaml:"minZoom"`
	PlanetMaxZoom                int                    `yaml:"planetMaxZoom"`
	MaxTileDownloadRetries       int                    `yaml:"maxTileDownloadRetries"`
	MaxTileDownloadRetryDelaySec int                    `yaml:"maxTileDownloadRetryDelaySec"`
	DetailedRegions              []DetailedRegionConfig `yaml:"detailedRegions"`
}

type tilesGenerator struct {
	cfg Config
	fs  cinodefs.FS
	log *slog.Logger
}

func (t *tilesGenerator) fetchTile(
	ctx context.Context,
	x, y, z int,
	log *slog.Logger,
) error {
	url := t.cfg.URLTemplate
	url = strings.ReplaceAll(url, "{x}", fmt.Sprint(x))
	url = strings.ReplaceAll(url, "{y}", fmt.Sprint(y))
	url = strings.ReplaceAll(url, "{z}", fmt.Sprint(z))

	retryDelay := time.Second

	delayTimer := time.NewTimer(retryDelay)
	defer delayTimer.Stop()

	nextLoopRetryDelay := func() {
		delayTimer.Reset(retryDelay)
		select {
		case <-delayTimer.C:
		case <-ctx.Done():
		}

		retryDelay = min(
			2*retryDelay,
			time.Second*time.Duration(t.cfg.MaxTileDownloadRetryDelaySec),
		)
	}

	for retry := 0; ctx.Err() == nil; retry++ {
		log := log.With("url", url, "retry", retry)

		log.InfoContext(ctx, "Fetching tile started")

		resp, err := http.Get(url)
		if err != nil || resp.StatusCode >= 400 {
			if err != nil {
				log.ErrorContext(ctx, "Error downloading tile", "err", err)
				err = fmt.Errorf("error downloading tile: %w", err)
			} else {
				resp.Body.Close()
				log.ErrorContext(ctx,
					"Incorrect http status code when downloading tile",
					"code", resp.StatusCode,
					"status", resp.Status,
				)
				err = fmt.Errorf("incorrect http status code when downloading tile: %d %s", resp.StatusCode, resp.Status)
			}

			if retry >= t.cfg.MaxTileDownloadRetries {
				return err
			}

			log.InfoContext(ctx, "Tile download failed, retrying", "retryDelay", retryDelay)
			nextLoopRetryDelay()

			continue
		}

		_, fName := filepath.Split(url)

		ep, err := t.fs.SetEntryFile(
			ctx,
			[]string{
				fmt.Sprint(z),
				fmt.Sprint(x),
				fName,
			},
			resp.Body,
		)
		resp.Body.Close()

		if err != nil {
			log.ErrorContext(ctx, "Error uploading tile to Cinode", "err", err, "retryDelay", retryDelay)
			nextLoopRetryDelay()

			continue
		}

		log.InfoContext(ctx, "Tile uploaded to cinode", "bn", ep.BlobName().String())

		return nil
	}

	return ctx.Err()
}

func (t *tilesGenerator) genXLayer(
	ctx context.Context,
	x, z int,
	log *slog.Logger,
) error {
	regions := make([]string, 0, len(t.cfg.DetailedRegions))
	for y := 0; ctx.Err() == nil && y < 1<<z; y++ {
		regions = regions[:0]
		// Check if this tile contains any detailed region
		for _, region := range t.cfg.DetailedRegions {
			if z > region.MaxZoom {
				continue
			}

			if region.BBox.ContainsTile(x, y, z) {
				regions = append(regions, region.Name)
				continue
			}
		}
		if len(regions) == 0 {
			continue
		}

		// Generate tile
		err := t.fetchTile(
			ctx,
			x, y, z,
			log.With("y", y, "regions", regions),
		)
		if err != nil {
			return err
		}
	}
	return nil
}

func (t *tilesGenerator) genZLayer(
	ctx context.Context,
	z int,
	log *slog.Logger,
) error {
	if z <= t.cfg.PlanetMaxZoom {
		return t.genZLayerNoConstraints(ctx, z, log)
	}

	regions := make([]string, 0, len(t.cfg.DetailedRegions))

	for x := 0; ctx.Err() == nil && x < 1<<z; x++ {
		// Check if this x stripe contains any detailed region
		regions = regions[:0]
		for _, region := range t.cfg.DetailedRegions {
			if z > region.MaxZoom {
				continue
			}
			if region.BBox.ContainsColumn(x, z) {
				regions = append(regions, region.Name)
			}
		}
		if len(regions) == 0 {
			// Whole x stripe does not contain any detailed region
			log.Info("skipping column", "x", x)
			continue
		}

		log.Info("column matches detailed region(s)", "x", x, "regions", regions)

		// Generate the layer
		err := t.genXLayer(ctx, x, z, log.With("x", x))
		if err != nil {
			return err
		}

		// For region-based z layer, flush once every column for better persistency and faster results
		err = t.fs.Flush(ctx)
		if err != nil {
			return fmt.Errorf("failed to flush the filesystem: %w", err)
		}
	}
	return nil
}

func (t *tilesGenerator) genZLayerNoConstraints(
	ctx context.Context,
	z int,
	log *slog.Logger,
) error {
	for x := 0; ctx.Err() == nil && x < 1<<z; x++ {
		// Generate the layer
		err := t.genXLayerNoConstraints(ctx, x, z, log.With("x", x))
		if err != nil {
			return err
		}
	}
	return nil
}

func (t *tilesGenerator) genXLayerNoConstraints(
	ctx context.Context,
	x, z int,
	log *slog.Logger,
) error {
	for y := 0; ctx.Err() == nil && y < 1<<z; y++ {
		// Generate tile
		err := t.fetchTile(
			ctx,
			x, y, z,
			log.With("y", y),
		)
		if err != nil {
			return err
		}
	}
	return nil
}

func (t *tilesGenerator) Process(ctx context.Context) error {
	maxZoomLevel := t.cfg.PlanetMaxZoom
	for _, region := range t.cfg.DetailedRegions {
		maxZoomLevel = max(maxZoomLevel, region.MaxZoom)
	}

	// Fetch all tiles
	for z := t.cfg.MinZoom; ctx.Err() == nil && z <= maxZoomLevel; z++ {
		err := t.genZLayer(ctx, z, t.log.With("z", z))
		if err != nil {
			return err
		}
		err = t.fs.Flush(ctx)
		if err != nil {
			return fmt.Errorf("failed to flush the filesystem: %w", err)
		}
	}

	return nil
}

// https://github.com/openstreetmap/mod_tile/pull/263#issuecomment-1006034286

// Command maps-vector-uploader publishes a vector tiles based map into cinode
package main

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"os"
	"os/signal"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"syscall"

	"github.com/cinode/go/pkg/blenc"
	"github.com/cinode/go/pkg/cinodefs"
	"github.com/cinode/go/pkg/datastore"
	"github.com/cinode/osm-machinery/pkg/vectormap"
)

const usageStr = `
This program uploads a vector tiles based map (pre-generated tiles,
MapLibre GL JS viewer and client-side assets) to Cinode datastore.
It is configured through environment variables:

 * CINODE_DATASTORE - address of the target datastore, required
 * CINODE_MAPTILES_WRITERINFO - writerinfo of map entrypoint
 * CINODE_MAPTILES_NEW_WRITERINFO - create new writerinfo
 * CINODE_MAPTILES_WRITERINFO_FILE - file with writerinfo of map entrypoint,
   if the file does not exist, new writerinfo is created and saved there
 * CINODE_MAPTILES_ENTRYPOINT_FILE - file where the map entrypoint is saved
   after a successful upload
 * CINODE_VECTOR_TILES_DIRS - list of directories with uncompressed
   {z}/{x}/{y}.pbf tiles separated with '` + string(os.PathListSeparator) + `', required,
   tiles from later directories override earlier ones
 * CINODE_VECTOR_ASSETS_DIR - directory with client-side assets
   (maplibre/, fonts/, sprites/)
 * CINODE_VECTOR_PARALLELISM - number of concurrent tile uploads,
   defaults to the number of CPUs
`

type envConfig struct {
	datastoreAddr  string
	writerInfo     string
	writerInfoFile string
	entrypointFile string
	vector         vectormap.Config
	newWriterInfo  bool
}

func configFromEnv(getenv func(string) string) (envConfig, error) {
	cfg := envConfig{
		datastoreAddr:  getenv("CINODE_DATASTORE"),
		writerInfo:     getenv("CINODE_MAPTILES_WRITERINFO"),
		writerInfoFile: getenv("CINODE_MAPTILES_WRITERINFO_FILE"),
		entrypointFile: getenv("CINODE_MAPTILES_ENTRYPOINT_FILE"),
		newWriterInfo:  getenv("CINODE_MAPTILES_NEW_WRITERINFO") != "",
		vector: vectormap.Config{
			AssetsDir:   getenv("CINODE_VECTOR_ASSETS_DIR"),
			Parallelism: runtime.NumCPU(),
		},
	}

	if cfg.datastoreAddr == "" {
		return cfg, errors.New("missing CINODE_DATASTORE env variable")
	}

	if cfg.writerInfo == "" && cfg.writerInfoFile == "" && !cfg.newWriterInfo {
		return cfg, errors.New(
			"one of CINODE_MAPTILES_WRITERINFO, CINODE_MAPTILES_WRITERINFO_FILE " +
				"or CINODE_MAPTILES_NEW_WRITERINFO env variables must be specified",
		)
	}

	for _, dir := range filepath.SplitList(getenv("CINODE_VECTOR_TILES_DIRS")) {
		if dir != "" {
			cfg.vector.TilesDirs = append(cfg.vector.TilesDirs, dir)
		}
	}
	if len(cfg.vector.TilesDirs) == 0 {
		return cfg, errors.New("missing CINODE_VECTOR_TILES_DIRS env variable")
	}

	if p := getenv("CINODE_VECTOR_PARALLELISM"); p != "" {
		n, err := strconv.Atoi(p)
		if err != nil || n < 1 {
			return cfg, fmt.Errorf("invalid CINODE_VECTOR_PARALLELISM value: '%s'", p)
		}
		cfg.vector.Parallelism = n
	}

	return cfg, nil
}

func usage(msg string) {
	fmt.Fprint(os.Stderr, usageStr)
	fmt.Fprintf(os.Stderr, "\nError: %s\n", msg)
	os.Exit(1)
}

func main() {
	cfg, err := configFromEnv(os.Getenv)
	if err != nil {
		usage(err.Error())
	}

	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	err = run(ctx, slog.Default(), cfg)
	cancel()
	if err != nil {
		slog.Error("Failed to upload vector map", "err", err)
		os.Exit(1)
	}
}

// rootOption selects the root of the uploaded map, the returned flag is set
// if a new link is created and its writer info must be saved
func rootOption(cfg envConfig) (cinodefs.Option, bool, error) {
	switch {
	case cfg.writerInfo != "":
		return cinodefs.RootWriterInfoString(cfg.writerInfo), false, nil

	case cfg.writerInfoFile != "":
		data, err := os.ReadFile(cfg.writerInfoFile)
		if errors.Is(err, fs.ErrNotExist) {
			return cinodefs.NewRootDynamicLink(), true, nil
		}
		if err != nil {
			return nil, false, fmt.Errorf("failed to read writer info file: %w", err)
		}
		return cinodefs.RootWriterInfoString(strings.TrimSpace(string(data))), false, nil

	default:
		return cinodefs.NewRootDynamicLink(), true, nil
	}
}

// writeFileAtomic replaces the file content so that readers never observe
// a partially written file
func writeFileAtomic(path, data string, perm os.FileMode) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, []byte(data+"\n"), perm); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

func run(ctx context.Context, log *slog.Logger, cfg envConfig) error {
	ds, err := datastore.FromLocation(cfg.datastoreAddr)
	if err != nil {
		return fmt.Errorf("failed to open datastore: %w", err)
	}

	rootOpt, newLink, err := rootOption(cfg)
	if err != nil {
		return err
	}

	cfs, err := cinodefs.New(ctx, blenc.FromDatastore(ds), rootOpt)
	if err != nil {
		return fmt.Errorf("failed to open cinodefs root: %w", err)
	}

	if newLink {
		// Store the link in the datastore before its writer info is saved,
		// otherwise a failed upload would leave writer info of a link that
		// does not exist
		if err := cfs.Flush(ctx); err != nil {
			return fmt.Errorf("failed to store new link: %w", err)
		}
		wi, err := cfs.RootWriterInfo(ctx)
		if err != nil {
			return err
		}
		if cfg.writerInfoFile != "" {
			// Saved before the upload so that the link is not lost if the upload fails
			if err := writeFileAtomic(cfg.writerInfoFile, wi.String(), 0o600); err != nil {
				return fmt.Errorf("failed to save writer info: %w", err)
			}
			log.InfoContext(ctx, "Created new map link", "writerInfoFile", cfg.writerInfoFile)
		} else {
			fmt.Printf("WriterInfo: %s\n", wi)
		}
	}

	if err := vectormap.Generate(ctx, log, cfg.vector, cfs); err != nil {
		return err
	}

	if err := cfs.Flush(ctx); err != nil {
		return fmt.Errorf("failed to flush the filesystem: %w", err)
	}

	ep, err := cfs.RootEntrypoint()
	if err != nil {
		return err
	}
	fmt.Printf("Entrypoint: %s\n", ep)

	if cfg.entrypointFile != "" {
		if err := writeFileAtomic(cfg.entrypointFile, ep.String(), 0o644); err != nil {
			return fmt.Errorf("failed to save entrypoint: %w", err)
		}
	}

	return nil
}

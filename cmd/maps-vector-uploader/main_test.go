package main

import (
	"context"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func envFrom(m map[string]string) func(string) string {
	return func(k string) string { return m[k] }
}

func TestConfigFromEnv(t *testing.T) {
	base := func() map[string]string {
		return map[string]string{
			"CINODE_DATASTORE":           "memory://",
			"CINODE_MAPTILES_WRITERINFO": "wi",
			"CINODE_VECTOR_TILES_DIRS":   "/world:/region",
		}
	}

	t.Run("valid", func(t *testing.T) {
		env := base()
		env["CINODE_VECTOR_ASSETS_DIR"] = "/assets"
		cfg, err := configFromEnv(envFrom(env))
		require.NoError(t, err)
		require.Equal(t, "memory://", cfg.datastoreAddr)
		require.Equal(t, "wi", cfg.writerInfo)
		require.False(t, cfg.newWriterInfo)
		require.Equal(t, []string{"/world", "/region"}, cfg.vector.TilesDirs)
		require.Equal(t, "/assets", cfg.vector.AssetsDir)
		require.Equal(t, runtime.NumCPU(), cfg.vector.Parallelism)
	})

	t.Run("new writer info and parallelism", func(t *testing.T) {
		env := base()
		delete(env, "CINODE_MAPTILES_WRITERINFO")
		env["CINODE_MAPTILES_NEW_WRITERINFO"] = "1"
		env["CINODE_VECTOR_PARALLELISM"] = "3"
		cfg, err := configFromEnv(envFrom(env))
		require.NoError(t, err)
		require.True(t, cfg.newWriterInfo)
		require.Equal(t, 3, cfg.vector.Parallelism)
	})

	t.Run("writer info file", func(t *testing.T) {
		env := base()
		delete(env, "CINODE_MAPTILES_WRITERINFO")
		env["CINODE_MAPTILES_WRITERINFO_FILE"] = "/data/wi"
		env["CINODE_MAPTILES_ENTRYPOINT_FILE"] = "/data/ep"
		cfg, err := configFromEnv(envFrom(env))
		require.NoError(t, err)
		require.Equal(t, "/data/wi", cfg.writerInfoFile)
		require.Equal(t, "/data/ep", cfg.entrypointFile)
	})

	for name, mod := range map[string]func(map[string]string){
		"no datastore":        func(m map[string]string) { delete(m, "CINODE_DATASTORE") },
		"no writer info":      func(m map[string]string) { delete(m, "CINODE_MAPTILES_WRITERINFO") },
		"no tiles dirs":       func(m map[string]string) { m["CINODE_VECTOR_TILES_DIRS"] = ":" },
		"invalid parallelism": func(m map[string]string) { m["CINODE_VECTOR_PARALLELISM"] = "0" },
	} {
		t.Run(name, func(t *testing.T) {
			env := base()
			mod(env)
			_, err := configFromEnv(envFrom(env))
			require.Error(t, err)
		})
	}
}

func readTrimmed(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	require.NoError(t, err)
	return strings.TrimSpace(string(data))
}

func TestRunPersistsLink(t *testing.T) {
	ctx := context.Background()
	log := slog.New(slog.NewTextHandler(io.Discard, nil))

	dir := t.TempDir()
	tiles := filepath.Join(dir, "tiles")
	require.NoError(t, os.MkdirAll(filepath.Join(tiles, "0", "0"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(tiles, "0", "0", "0.pbf"), []byte("tile"), 0o644))

	cfg, err := configFromEnv(envFrom(map[string]string{
		"CINODE_DATASTORE":                "file://" + filepath.Join(dir, "datastore"),
		"CINODE_MAPTILES_WRITERINFO_FILE": filepath.Join(dir, "link", "writerinfo"),
		"CINODE_MAPTILES_ENTRYPOINT_FILE": filepath.Join(dir, "link", "entrypoint"),
		"CINODE_VECTOR_TILES_DIRS":        tiles,
		"CINODE_VECTOR_PARALLELISM":       "2",
	}))
	require.NoError(t, err)

	// First run creates a new link and saves its writer info
	require.NoError(t, run(ctx, log, cfg))
	wi := readTrimmed(t, cfg.writerInfoFile)
	ep := readTrimmed(t, cfg.entrypointFile)
	require.NotEmpty(t, wi)
	require.NotEmpty(t, ep)

	st, err := os.Stat(cfg.writerInfoFile)
	require.NoError(t, err)
	require.Equal(t, os.FileMode(0o600), st.Mode().Perm())

	// Next run updates the same link
	require.NoError(t, os.WriteFile(filepath.Join(tiles, "0", "0", "0.pbf"), []byte("tile-v2"), 0o644))
	require.NoError(t, run(ctx, log, cfg))
	require.Equal(t, wi, readTrimmed(t, cfg.writerInfoFile))
	require.Equal(t, ep, readTrimmed(t, cfg.entrypointFile))
}

func TestRunReusesLinkAfterFailure(t *testing.T) {
	ctx := context.Background()
	log := slog.New(slog.NewTextHandler(io.Discard, nil))

	dir := t.TempDir()
	tiles := filepath.Join(dir, "tiles")

	cfg, err := configFromEnv(envFrom(map[string]string{
		"CINODE_DATASTORE":                "file://" + filepath.Join(dir, "datastore"),
		"CINODE_MAPTILES_WRITERINFO_FILE": filepath.Join(dir, "link", "writerinfo"),
		"CINODE_MAPTILES_ENTRYPOINT_FILE": filepath.Join(dir, "link", "entrypoint"),
		"CINODE_VECTOR_TILES_DIRS":        tiles,
	}))
	require.NoError(t, err)

	// Tiles dir is missing - the upload fails after the link is created
	require.Error(t, run(ctx, log, cfg))
	wi := readTrimmed(t, cfg.writerInfoFile)
	require.NoFileExists(t, cfg.entrypointFile)

	require.NoError(t, os.MkdirAll(filepath.Join(tiles, "0", "0"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(tiles, "0", "0", "0.pbf"), []byte("tile"), 0o644))
	require.NoError(t, run(ctx, log, cfg))
	require.Equal(t, wi, readTrimmed(t, cfg.writerInfoFile))
	require.FileExists(t, cfg.entrypointFile)
}

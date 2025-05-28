package main

import (
	"context"
	"log/slog"
	"time"

	"github.com/cinode/go/pkg/cinodefs"
)

type FlushStrategy interface {
	FlushOpportunity(ctx context.Context) error
	ColumnFinished(ctx context.Context, isDetailedRegion bool) error
	ZLayerFinished(ctx context.Context) error
	ProcessFinished(ctx context.Context) error
}

func NewFlushStrategy(
	fs cinodefs.FS,
	cfg FlushStrategyConfig,
	timeSource func() time.Time,
	log *slog.Logger,
) FlushStrategy {
	return &flushStrategy{
		fs:         fs,
		cfg:        cfg,
		timeSource: timeSource,
		log:        log,
	}
}

type FlushStrategyConfig struct {
	MaxFlushInterval              *time.Duration `yaml:"maxFlushInterval,omitempty"`
	FlushOnDetailedColumnFinished bool           `yaml:"flushOnDetailedColumnFinished"`
	FlushOnColumnFinished         bool           `yaml:"flushOnColumnFinished"`
	FlushOnZLayerFinished         bool           `yaml:"flushOnZLayerFinished"`
}

type flushStrategy struct {
	fs            cinodefs.FS
	lastFlushTime time.Time
	cfg           FlushStrategyConfig
	timeSource    func() time.Time
	log           *slog.Logger
}

func (f *flushStrategy) flush(ctx context.Context, reason string) error {
	f.log.InfoContext(ctx, "Flushing filesystem", "reason", reason)
	if err := f.fs.Flush(ctx); err != nil {
		return err
	}

	f.lastFlushTime = f.timeSource()

	return nil
}

func (f *flushStrategy) FlushOpportunity(ctx context.Context) error {
	if f.cfg.MaxFlushInterval == nil {
		return nil
	}

	if f.timeSource().Sub(f.lastFlushTime) < *f.cfg.MaxFlushInterval {
		return nil
	}

	return f.flush(ctx, "maxFlushInterval reached")
}

func (f *flushStrategy) ColumnFinished(ctx context.Context, isDetailedRegion bool) error {
	if isDetailedRegion && f.cfg.FlushOnDetailedColumnFinished {
		return f.flush(ctx, "detailed column finished")
	}

	if f.cfg.FlushOnColumnFinished {
		return f.flush(ctx, "column finished")
	}

	return nil
}

func (f *flushStrategy) ZLayerFinished(ctx context.Context) error {
	if f.cfg.FlushOnZLayerFinished {
		return f.flush(ctx, "zoom layer finished")
	}

	return nil
}

func (f *flushStrategy) ProcessFinished(ctx context.Context) error {
	return f.flush(ctx, "process finished")
}

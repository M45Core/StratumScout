package scout

import (
	"context"
	"errors"
	"fmt"
	"log"
	"os"
	"time"

	"github.com/M45Core/StratumScout/internal/model"
	"github.com/M45Core/StratumScout/internal/probe"
)

const (
	continuousRestartDelay = time.Second
	continuousRetryMax     = time.Minute
)

func Main(ctx context.Context) error {
	cfg, err := LoadConfig(os.Getenv)
	if err != nil {
		return err
	}
	if err := setProcessPriority(cfg.ProcessNice); err != nil {
		return fmt.Errorf("set process priority: %w", err)
	}
	if !cfg.Continuous {
		return Run(ctx, cfg)
	}
	return runContinuously(ctx, cfg, Run, continuousRestartDelay, continuousRetryMax)
}

func runContinuously(ctx context.Context, cfg Config, run func(context.Context, Config) error, restartDelay, retryMax time.Duration) error {
	retryDelay := restartDelay
	for {
		err := run(ctx, cfg)
		if ctx.Err() != nil {
			return nil
		}
		delay := restartDelay
		if err != nil {
			log.Printf("probe run failed; retrying in %s", retryDelay)
			delay = retryDelay
			retryDelay *= 2
			if retryDelay > retryMax {
				retryDelay = retryMax
			}
		} else {
			retryDelay = restartDelay
		}
		timer := time.NewTimer(delay)
		select {
		case <-ctx.Done():
			if !timer.Stop() {
				<-timer.C
			}
			return nil
		case <-timer.C:
		}
	}
}

func Run(ctx context.Context, cfg Config) error {
	var hardCtx context.Context
	var hardCancel context.CancelFunc
	if cfg.Continuous {
		hardCtx, hardCancel = context.WithCancel(ctx)
	} else {
		hardCtx, hardCancel = context.WithTimeout(ctx, cfg.RunFor+45*time.Second)
	}
	defer hardCancel()
	var measureCtx context.Context
	var measureCancel context.CancelFunc
	if cfg.Continuous {
		measureCtx, measureCancel = context.WithCancel(hardCtx)
	} else {
		measureCtx, measureCancel = context.WithTimeout(hardCtx, cfg.RunFor)
	}
	defer measureCancel()

	remote, pools, err := fetchProbeConfig(hardCtx, cfg)
	if err != nil {
		return err
	}
	upload := newUploader(cfg, remote.ConfigRevision)
	mode := "continuous"
	if !cfg.Continuous {
		mode = cfg.RunFor.String()
	}
	log.Printf("probe region=%s vantage=%s endpoints=%d mode=%s", cfg.Region, cfg.Vantage, endpointCount(pools), mode)
	emit := func(sample model.BlockSample) error {
		if err := upload.uploadBlock(hardCtx, sample); err != nil {
			log.Printf("probe block=%s endpoints=%d upload=dropped", sample.BlockID, len(sample.EndpointSamples))
			return nil
		}
		log.Printf("probe block=%s endpoints=%d uploaded", sample.BlockID, len(sample.EndpointSamples))
		return nil
	}
	collectErr := probe.Collect(measureCtx, pools, emit)
	if ctx.Err() != nil {
		return nil
	}
	unexpectedStop := cfg.Continuous || (collectErr != nil && !errors.Is(collectErr, context.DeadlineExceeded) && !errors.Is(collectErr, context.Canceled))
	if unexpectedStop {
		log.Print("collector stopped unexpectedly")
	}
	if unexpectedStop {
		if collectErr != nil {
			return fmt.Errorf("collector stopped unexpectedly: %w", collectErr)
		}
		return errors.New("collector stopped unexpectedly")
	}
	return nil
}

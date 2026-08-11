package scout

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"log"
	"os"
	"strconv"
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
	hardCtx, hardCancel := context.WithCancel(ctx)
	if !cfg.Continuous {
		hardCtx, hardCancel = context.WithTimeout(ctx, cfg.RunFor+45*time.Second)
	}
	defer hardCancel()
	remote, pools, err := fetchProbeConfig(hardCtx, cfg)
	if err != nil {
		return err
	}
	var runID string
	var startedAt time.Time
	var upload *uploader
	measureCtx, measureCancel := context.WithCancel(hardCtx)
	if !cfg.Continuous {
		measureCtx, measureCancel = context.WithTimeout(hardCtx, cfg.RunFor)
	}
	defer measureCancel()

	successful := make(map[string]bool)
	blocks := make(map[string]bool)
	startCohort := func() error {
		var err error
		runID, err = randomID()
		if err != nil {
			return err
		}
		startedAt = time.Now().UTC()
		upload = newUploader(hardCtx, cfg, runID, startedAt, remote.ConfigRevision)
		blocks = make(map[string]bool)
		mode := "continuous"
		if !cfg.Continuous {
			mode = cfg.RunFor.String()
		}
		log.Printf("probe run=%s region=%s vantage=%s endpoints=%d mode=%s", runID, cfg.Region, cfg.Vantage, endpointCount(pools), mode)
		return nil
	}
	completeCohort := func(status string) error {
		currentUpload := upload
		upload = nil
		stats := currentUpload.closeAndFlush()
		if status == "ok" && (stats.Failed || stats.Dropped > 0) {
			status = "partial"
		}
		now := time.Now().UTC()
		final := model.Observation{
			Version:              model.ObservationVersion,
			RecordType:           model.RecordTypeProbeRun,
			ObservedAt:           now,
			Vantage:              cfg.Vantage,
			RunStartedAt:         &startedAt,
			RunStatus:            status,
			ConfiguredEndpoints:  endpointCount(pools),
			SuccessfulSessions:   len(successful),
			AcceptedBlocks:       len(blocks),
			UploadedObservations: stats.Uploaded,
			DroppedObservations:  stats.Dropped,
		}
		if err := currentUpload.uploadFinal(hardCtx, final); err != nil {
			return fmt.Errorf("upload final probe status: %w", err)
		}
		log.Printf("probe run=%s status=%s sessions=%d blocks=%d uploaded=%d dropped=%d", runID, status, len(successful), len(blocks), stats.Uploaded+1, stats.Dropped)
		return nil
	}
	if err := startCohort(); err != nil {
		return err
	}
	emit := func(records []model.Observation) error {
		finalizedBlock := hasFinalizedBlock(records)
		for _, record := range records {
			if record.RecordType == model.RecordTypeProtocol && record.ProtocolMethod == model.ProtocolAuthorize && record.ResponseStatus == model.ProtocolStatusOK {
				successful[record.PoolID+"\x00"+record.Endpoint+"\x00"+strconv.FormatBool(record.TLS)] = true
			}
			if record.RecordType == "" && record.Arrived && record.BlockID != "" {
				blocks[record.BlockID] = true
			}
		}
		if err := upload.enqueue(records); err != nil {
			return err
		}
		if cfg.Continuous && finalizedBlock {
			if err := completeCohort("ok"); err != nil {
				return err
			}
			return startCohort()
		}
		return nil
	}

	var collectErr error
	for {
		collectErr = probe.Collect(measureCtx, pools, cfg.Vantage, emit)
		if !errors.Is(collectErr, probe.ErrConnectionRefresh) {
			break
		}
		log.Print("refreshing Stratum sessions after maximum connection age")
	}
	if cfg.Continuous && ctx.Err() != nil {
		if upload != nil {
			upload.closeAndFlush()
		}
		return nil
	}
	status := "ok"
	unexpectedStop := cfg.Continuous || (collectErr != nil && !errors.Is(collectErr, context.DeadlineExceeded) && !errors.Is(collectErr, context.Canceled))
	if unexpectedStop {
		log.Print("collector stopped unexpectedly")
		status = "error"
	}
	if upload != nil {
		if err := completeCohort(status); err != nil {
			return err
		}
	}
	if unexpectedStop {
		if collectErr != nil {
			return fmt.Errorf("collector stopped unexpectedly: %w", collectErr)
		}
		return errors.New("collector stopped unexpectedly")
	}
	return nil
}

func hasFinalizedBlock(records []model.Observation) bool {
	for _, record := range records {
		if record.RecordType == "" && record.BlockID != "" {
			return true
		}
	}
	return false
}

func randomID() (string, error) {
	raw := make([]byte, 16)
	if _, err := rand.Read(raw); err != nil {
		return "", fmt.Errorf("generate run ID: %w", err)
	}
	return hex.EncodeToString(raw), nil
}

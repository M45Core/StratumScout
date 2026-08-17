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
	var hardCtx context.Context
	var hardCancel context.CancelFunc
	if cfg.Continuous {
		hardCtx, hardCancel = context.WithCancel(ctx)
	} else {
		hardCtx, hardCancel = context.WithTimeout(ctx, cfg.RunFor+45*time.Second)
	}
	defer hardCancel()
	remote, pools, err := fetchProbeConfig(hardCtx, cfg)
	if err != nil {
		return err
	}
	var measureCtx context.Context
	var measureCancel context.CancelFunc
	if cfg.Continuous {
		measureCtx, measureCancel = context.WithCancel(hardCtx)
	} else {
		measureCtx, measureCancel = context.WithTimeout(hardCtx, cfg.RunFor)
	}
	defer measureCancel()

	successful := make(map[string]bool)
	type reportingCohort struct {
		runID          string
		startedAt      time.Time
		upload         *uploader
		acceptedBlocks int
	}
	startCohort := func(startedAt time.Time) (*reportingCohort, error) {
		runID, err := randomID()
		if err != nil {
			return nil, err
		}
		if startedAt.IsZero() {
			startedAt = time.Now().UTC()
		}
		cohort := &reportingCohort{runID: runID, startedAt: startedAt.UTC()}
		cohort.upload = newUploader(hardCtx, cfg, cohort.runID, cohort.startedAt, remote.ConfigRevision)
		mode := "continuous"
		if !cfg.Continuous {
			mode = cfg.RunFor.String()
		}
		log.Printf("probe run=%s region=%s vantage=%s endpoints=%d mode=%s", cohort.runID, cfg.Region, cfg.Vantage, endpointCount(pools), mode)
		return cohort, nil
	}
	completeCohort := func(cohort *reportingCohort, status string) error {
		stats := cohort.upload.closeAndFlush()
		if status == "ok" && (stats.Failed || stats.Dropped > 0) {
			status = "partial"
		}
		now := time.Now().UTC()
		final := model.Observation{
			Version:              model.ObservationVersion,
			RecordType:           model.RecordTypeProbeRun,
			ObservedAt:           now,
			Vantage:              cfg.Vantage,
			RunStartedAt:         &cohort.startedAt,
			RunStatus:            status,
			ConfiguredEndpoints:  endpointCount(pools),
			SuccessfulSessions:   len(successful),
			AcceptedBlocks:       cohort.acceptedBlocks,
			UploadedObservations: stats.Uploaded,
			DroppedObservations:  stats.Dropped,
		}
		if err := cohort.upload.uploadFinal(hardCtx, final); err != nil {
			return fmt.Errorf("upload final probe status: %w", err)
		}
		log.Printf("probe run=%s status=%s sessions=%d blocks=%d uploaded=%d dropped=%d", cohort.runID, status, len(successful), cohort.acceptedBlocks, stats.Uploaded+1, stats.Dropped)
		return nil
	}
	current, err := startCohort(time.Time{})
	if err != nil {
		return err
	}
	emit := func(records []model.Observation, nextStartedAt time.Time) error {
		finalizedBlock := hasFinalizedBlock(records)
		for _, record := range records {
			if record.RecordType == model.RecordTypeProtocol && record.ProtocolMethod == model.ProtocolAuthorize && record.ResponseStatus == model.ProtocolStatusOK {
				successful[record.PoolID+"\x00"+record.Endpoint+"\x00"+strconv.FormatBool(record.TLS)] = true
			}
		}
		if finalizedBlock {
			current.acceptedBlocks++
		}
		if err := current.upload.enqueue(records); err != nil {
			return err
		}
		if cfg.Continuous && !nextStartedAt.IsZero() {
			completed := current
			next, err := startCohort(nextStartedAt)
			if err != nil {
				return err
			}
			current = next
			if err := completeCohort(completed, "ok"); err != nil {
				return err
			}
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
		if current != nil {
			current.upload.closeAndFlush()
		}
		return nil
	}
	status := "ok"
	unexpectedStop := cfg.Continuous || (collectErr != nil && !errors.Is(collectErr, context.DeadlineExceeded) && !errors.Is(collectErr, context.Canceled))
	if unexpectedStop {
		log.Print("collector stopped unexpectedly")
		status = "error"
	}
	if current != nil {
		if err := completeCohort(current, status); err != nil {
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

package scout

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"log"
	"os"
	"sync"
	"time"

	"github.com/Distortions81/StratumScout/internal/model"
	"github.com/Distortions81/StratumScout/internal/probe"
)

func Main(ctx context.Context) error {
	cfg, err := LoadConfig(os.Getenv)
	if err != nil {
		return err
	}
	return Run(ctx, cfg)
}

func Run(ctx context.Context, cfg Config) error {
	hardCtx, hardCancel := context.WithTimeout(ctx, cfg.RunFor+45*time.Second)
	defer hardCancel()
	remote, pools, err := fetchProbeConfig(hardCtx, cfg)
	if err != nil {
		return err
	}
	runID, err := randomID()
	if err != nil {
		return err
	}
	startedAt := time.Now().UTC()
	upload := newUploader(hardCtx, cfg, runID, startedAt, remote.ConfigRevision)
	measureCtx, measureCancel := context.WithTimeout(hardCtx, cfg.RunFor)
	defer measureCancel()

	var countersMu sync.Mutex
	successful := make(map[string]bool)
	blocks := make(map[string]bool)
	emit := func(records []model.Observation) error {
		countersMu.Lock()
		for _, record := range records {
			if record.RecordType == model.RecordTypeProtocol && record.ProtocolMethod == model.ProtocolAuthorize && record.ResponseStatus == model.ProtocolStatusOK {
				successful[record.PoolID+"\x00"+record.Endpoint] = true
			}
			if record.RecordType == "" && record.Arrived && record.BlockID != "" {
				blocks[record.BlockID] = true
			}
		}
		countersMu.Unlock()
		return upload.enqueue(records)
	}

	log.Printf("probe run=%s region=%s vantage=%s endpoints=%d duration=%s", runID, cfg.Region, cfg.Vantage, endpointCount(pools), cfg.RunFor)
	collectErr := probe.Collect(measureCtx, pools, cfg.Vantage, emit)
	if collectErr != nil && !errors.Is(collectErr, context.DeadlineExceeded) && !errors.Is(collectErr, context.Canceled) {
		log.Print("collector stopped unexpectedly")
	}
	stats := upload.closeAndFlush()
	countersMu.Lock()
	successCount, blockCount := len(successful), len(blocks)
	countersMu.Unlock()
	status := "ok"
	if collectErr != nil && !errors.Is(collectErr, context.DeadlineExceeded) && !errors.Is(collectErr, context.Canceled) {
		status = "error"
	} else if stats.Failed || stats.Dropped > 0 {
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
		SuccessfulSessions:   successCount,
		AcceptedBlocks:       blockCount,
		UploadedObservations: stats.Uploaded,
		DroppedObservations:  stats.Dropped,
	}
	if err := upload.uploadFinal(hardCtx, final); err != nil {
		return fmt.Errorf("upload final probe status: %w", err)
	}
	log.Printf("probe run=%s status=%s sessions=%d blocks=%d uploaded=%d dropped=%d", runID, status, successCount, blockCount, stats.Uploaded+1, stats.Dropped)
	return nil
}

func randomID() (string, error) {
	raw := make([]byte, 16)
	if _, err := rand.Read(raw); err != nil {
		return "", fmt.Errorf("generate run ID: %w", err)
	}
	return hex.EncodeToString(raw), nil
}

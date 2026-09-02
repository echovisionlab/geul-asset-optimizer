package main

import (
	"context"
	"log/slog"
	"os"
	"time"

	"github.com/echovisionlab/geul-asset-optimizer/internal/config"
	"github.com/echovisionlab/geul-asset-optimizer/internal/telemetry"
	sharedtelemetry "github.com/echovisionlab/geul-telemetry"
)

const (
	serviceComponent           = "worker"
	telemetryPipelineComponent = "log_exporter"
)

func setupLogging(parent context.Context, deps dependencies, cfg *config.Config) func() {
	logLevel := slog.LevelInfo
	if cfg.LogLevel == "debug" {
		logLevel = slog.LevelDebug
	}
	stdoutHandler := slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: logLevel})
	slog.SetDefault(slog.New(telemetry.NewNormalizingHandler(stdoutHandler)))
	telemetryResult, telemetryErr := deps.initTelemetry(parent, sharedtelemetry.ServiceAssetOptimizer)
	if telemetryErr != nil {
		emitTelemetryPipelineDegraded(parent, telemetryErr)
		return func() {}
	}
	slog.SetDefault(slog.New(telemetry.NewNormalizingHandler(telemetry.NewFanoutHandler(stdoutHandler, telemetryResult.LogHandler))))
	return func() {
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if shutdownErr := telemetryResult.Shutdown(shutdownCtx); shutdownErr != nil {
			emitTelemetryPipelineDegraded(shutdownCtx, shutdownErr)
		}
	}
}

func emitServiceReady(ctx context.Context) {
	record, err := sharedtelemetry.NewServiceReadyRecord(
		telemetry.SystemMetadata(ctx, time.Now()), serviceComponent,
	)
	_ = telemetry.EmitSystem(ctx, record, err)
}

func emitServiceStopping(ctx context.Context) {
	record, err := sharedtelemetry.NewServiceStoppingRecord(
		telemetry.SystemMetadata(ctx, time.Now()), serviceComponent,
	)
	_ = telemetry.EmitSystem(ctx, record, err)
}

func emitServiceFailed(ctx context.Context, failure error) {
	record, err := sharedtelemetry.NewServiceFailedRecord(
		telemetry.SystemMetadata(ctx, time.Now()), serviceComponent,
		sharedtelemetry.SystemFailure{ErrorCode: sharedtelemetry.StableErrorType(failure)},
	)
	_ = telemetry.EmitSystem(ctx, record, err)
}

func emitTelemetryPipelineDegraded(ctx context.Context, failure error) {
	record, err := sharedtelemetry.NewTelemetryPipelineDegradedRecord(
		telemetry.SystemMetadata(ctx, time.Now()), telemetryPipelineComponent,
		sharedtelemetry.SystemFailure{ErrorCode: sharedtelemetry.StableErrorType(failure)},
	)
	_ = telemetry.EmitSystem(ctx, record, err)
}

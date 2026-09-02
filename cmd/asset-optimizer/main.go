package main

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"syscall"
	"time"

	"github.com/echovisionlab/geul-asset-optimizer/internal/handler"
	"github.com/echovisionlab/geul-asset-optimizer/internal/jobs"
	"github.com/echovisionlab/geul-asset-optimizer/internal/telemetry"
)

var (
	exitProcess    = os.Exit
	executeService = execute
)

func main() {
	exitProcess(executeService(productionDependencies()))
}

func execute(deps dependencies) int {
	if err := run(context.Background(), deps); err != nil {
		return 1
	}
	return 0
}

func run(parent context.Context, deps dependencies) (runErr error) {
	bootstrapHandler := slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelInfo})
	slog.SetDefault(slog.New(telemetry.NewNormalizingHandler(bootstrapHandler)))

	cfg, err := deps.loadConfig()
	if err != nil {
		loadErr := fmt.Errorf("load configuration: %w", err)
		emitServiceFailed(context.Background(), loadErr)
		return loadErr
	}
	shutdownLogging := setupLogging(parent, deps, cfg)
	defer shutdownLogging()
	defer func() {
		if runErr != nil {
			emitServiceFailed(context.Background(), runErr)
		}
	}()

	resources, err := initializeWorkResources(deps, cfg)
	if err != nil {
		return err
	}
	ctx, cancel := context.WithCancel(parent)
	defer cancel()

	mqConn, err := deps.newConnection(cfg.DatabaseDSN)
	if err != nil {
		return fmt.Errorf("connect to PostgreSQL: %w", err)
	}
	defer deps.closeConnection(mqConn)

	publisher, err := deps.newPublisher(mqConn)
	if err != nil {
		return fmt.Errorf("initialize publisher: %w", err)
	}
	defer deps.closePublisher(publisher)

	meshOptimizer := deps.newOptimizer(cfg.GLTFTransformPath, cfg.NodeBinaryPath, cfg.ParticleMeshScriptPath)
	processor, err := deps.newProcessor(
		handler.Config{MaxInputBytes: cfg.MaxInputBytes},
		resources.workDirs,
		resources.storage,
		meshOptimizer,
		publisher,
	)
	if err != nil {
		return fmt.Errorf("initialize mesh job processor: %w", err)
	}

	queueConfig := jobs.DefaultMeshQueueConfig()
	queueConfig.Workers = cfg.WorkerCount
	queueConfig.Timeout = time.Duration(cfg.JobTimeoutMins) * time.Minute

	consumer, err := deps.newConsumer(mqConn, queueConfig, processor.HandleMeshJob)
	if err != nil {
		return fmt.Errorf("initialize mesh optimizer consumer: %w", err)
	}
	defer deps.closeConsumer(consumer)
	if err := deps.startConsumer(consumer, ctx); err != nil {
		return fmt.Errorf("start mesh optimizer consumer: %w", err)
	}

	server, serverErrors, err := startHealthServer(deps, cfg, mqConn)
	if err != nil {
		return err
	}
	emitServiceReady(ctx)

	sigChan := make(chan os.Signal, 1)
	deps.notifySignals(sigChan, syscall.SIGINT, syscall.SIGTERM)
	defer deps.stopSignals(sigChan)

	shutdownReason, shutdownSignal, waitErr := waitForShutdown(ctx, sigChan, serverErrors)
	slog.Info("Shutting down asset optimizer", "reason", shutdownReason, "signal", shutdownSignal)
	emitServiceStopping(ctx)
	cancel()

	shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer shutdownCancel()
	if err := deps.shutdownServer(server, shutdownCtx); err != nil {
		return fmt.Errorf("shutdown HTTP server: %w", err)
	}
	runErr = waitErr
	return runErr
}

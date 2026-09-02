package main

import (
	"context"
	"net"
	"net/http"
	"os"
	"os/signal"

	"github.com/echovisionlab/geul-asset-optimizer/internal/config"
	"github.com/echovisionlab/geul-asset-optimizer/internal/handler"
	"github.com/echovisionlab/geul-asset-optimizer/internal/jobs"
	"github.com/echovisionlab/geul-asset-optimizer/internal/mq"
	"github.com/echovisionlab/geul-asset-optimizer/internal/optimizer"
	"github.com/echovisionlab/geul-asset-optimizer/internal/storage"
	"github.com/echovisionlab/geul-asset-optimizer/internal/telemetry"
	"github.com/echovisionlab/geul-asset-optimizer/internal/workdir"
	sharedtelemetry "github.com/echovisionlab/geul-telemetry"
)

type dependencies struct {
	loadConfig           func() (*config.Config, error)
	newWorkDirs          func(string) (*workdir.Manager, error)
	cleanupStaleWorkDirs func(*workdir.Manager) (int, error)
	newStorage           func(*config.Config) (*storage.S3Client, error)
	newConnection        func(string) (*mq.Connection, error)
	closeConnection      func(*mq.Connection) error
	newPublisher         func(*mq.Connection) (*mq.Publisher, error)
	closePublisher       func(*mq.Publisher) error
	newOptimizer         func(string, string, string) *optimizer.CLI
	newProcessor         func(handler.Config, handler.WorkDirManager, handler.StorageClient, handler.MeshOptimizer, handler.EventPublisher) (*handler.Processor, error)
	newConsumer          func(*mq.Connection, jobs.QueueConfig, mq.Handler) (*mq.Consumer, error)
	startConsumer        func(*mq.Consumer, context.Context) error
	closeConsumer        func(*mq.Consumer) error
	listen               func(string, string) (net.Listener, error)
	serve                func(*http.Server, net.Listener) error
	shutdownServer       func(*http.Server, context.Context) error
	notifySignals        func(chan<- os.Signal, ...os.Signal)
	stopSignals          func(chan<- os.Signal)
	initTelemetry        func(context.Context, sharedtelemetry.ServiceName) (*telemetry.InitResult, error)
}

func productionDependencies() dependencies {
	return dependencies{
		loadConfig: config.Load, newWorkDirs: workdir.NewManager,
		cleanupStaleWorkDirs: (*workdir.Manager).CleanupStaleWorkDirs,
		newStorage:           storage.NewS3Client, newConnection: mq.NewConnection,
		closeConnection: (*mq.Connection).Close,
		newPublisher:    mq.NewPublisher, closePublisher: (*mq.Publisher).Close,
		newOptimizer: optimizer.NewCLI, newProcessor: handler.NewProcessor,
		newConsumer: mq.NewConsumer, startConsumer: (*mq.Consumer).Start,
		closeConsumer:  (*mq.Consumer).Close,
		listen:         net.Listen,
		serve:          (*http.Server).Serve,
		shutdownServer: (*http.Server).Shutdown,
		notifySignals:  signal.Notify, stopSignals: signal.Stop,
		initTelemetry: telemetry.Init,
	}
}

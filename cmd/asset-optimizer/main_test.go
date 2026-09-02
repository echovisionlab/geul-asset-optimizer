package main

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"syscall"
	"testing"

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

func TestMainDelegatesExitCode(t *testing.T) {
	originalExit := exitProcess
	originalExecute := executeService
	t.Cleanup(func() {
		exitProcess = originalExit
		executeService = originalExecute
	})
	want := 7
	executeService = func(dependencies) int { return want }
	got := -1
	exitProcess = func(code int) { got = code }
	main()
	if got != want {
		t.Fatalf("exit code: want %d got %d", want, got)
	}
}

func TestExecute(t *testing.T) {
	if code := execute(successDependencies(t)); code != 0 {
		t.Fatalf("success exit code: %d", code)
	}
	deps := successDependencies(t)
	deps.loadConfig = func() (*config.Config, error) { return nil, errors.New("config failed") }
	if code := execute(deps); code != 1 {
		t.Fatalf("failure exit code: %d", code)
	}
}

func TestRunInitializationErrors(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*dependencies)
		want   string
	}{
		{name: "config", mutate: func(deps *dependencies) {
			deps.loadConfig = func() (*config.Config, error) { return nil, errors.New("failed") }
		}, want: "load configuration"},
		{name: "work dirs", mutate: func(deps *dependencies) {
			deps.newWorkDirs = func(string) (*workdir.Manager, error) { return nil, errors.New("failed") }
		}, want: "initialize work directory"},
		{name: "stale cleanup", mutate: func(deps *dependencies) {
			deps.cleanupStaleWorkDirs = func(*workdir.Manager) (int, error) { return 0, errors.New("failed") }
		}, want: "clean stale"},
		{name: "storage", mutate: func(deps *dependencies) {
			deps.newStorage = func(*config.Config) (*storage.S3Client, error) { return nil, errors.New("failed") }
		}, want: "initialize S3"},
		{name: "connection", mutate: func(deps *dependencies) {
			deps.newConnection = func(string) (*mq.Connection, error) { return nil, errors.New("failed") }
		}, want: "connect to PostgreSQL"},
		{name: "publisher", mutate: func(deps *dependencies) {
			deps.newPublisher = func(*mq.Connection) (*mq.Publisher, error) { return nil, errors.New("failed") }
		}, want: "initialize publisher"},
		{name: "processor", mutate: func(deps *dependencies) {
			deps.newProcessor = func(handler.Config, handler.WorkDirManager, handler.StorageClient, handler.MeshOptimizer, handler.EventPublisher) (*handler.Processor, error) {
				return nil, errors.New("failed")
			}
		}, want: "initialize mesh job processor"},
		{name: "consumer", mutate: func(deps *dependencies) {
			deps.newConsumer = func(*mq.Connection, jobs.QueueConfig, mq.Handler) (*mq.Consumer, error) {
				return nil, errors.New("failed")
			}
		}, want: "initialize mesh optimizer consumer"},
		{name: "consumer start", mutate: func(deps *dependencies) {
			deps.startConsumer = func(*mq.Consumer, context.Context) error { return errors.New("failed") }
		}, want: "start mesh optimizer consumer"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			deps := successDependencies(t)
			test.mutate(&deps)
			err := run(context.Background(), deps)
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("expected %q, got %v", test.want, err)
			}
		})
	}
}

func TestRunShutdownPaths(t *testing.T) {
	t.Run("context", func(t *testing.T) {
		deps := successDependencies(t)
		deps.notifySignals = func(chan<- os.Signal, ...os.Signal) {}
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		if err := run(ctx, deps); err != nil {
			t.Fatalf("context shutdown: %v", err)
		}
	})

	t.Run("signal", func(t *testing.T) {
		deps := successDependencies(t)
		deps.loadConfig = func() (*config.Config, error) {
			cfg := testServiceConfig()
			cfg.LogLevel = "debug"
			return cfg, nil
		}
		if err := run(context.Background(), deps); err != nil {
			t.Fatalf("signal shutdown: %v", err)
		}
	})

	t.Run("health bind", func(t *testing.T) {
		deps := successDependencies(t)
		deps.listen = func(string, string) (net.Listener, error) { return nil, errors.New("listen failed") }
		deps.notifySignals = func(chan<- os.Signal, ...os.Signal) {}
		err := run(context.Background(), deps)
		if err == nil || !strings.Contains(err.Error(), "listen health endpoint") {
			t.Fatalf("expected bind error, got %v", err)
		}
	})

	t.Run("http server", func(t *testing.T) {
		deps := successDependencies(t)
		deps.serve = func(*http.Server, net.Listener) error { return errors.New("serve failed") }
		deps.notifySignals = func(chan<- os.Signal, ...os.Signal) {}
		err := run(context.Background(), deps)
		if err == nil || !strings.Contains(err.Error(), "serve health endpoint") {
			t.Fatalf("expected serve error, got %v", err)
		}
	})

	t.Run("shutdown failure", func(t *testing.T) {
		deps := successDependencies(t)
		deps.shutdownServer = func(server *http.Server, _ context.Context) error {
			_ = server.Close()
			return errors.New("shutdown failed")
		}
		err := run(context.Background(), deps)
		if err == nil || !strings.Contains(err.Error(), "shutdown HTTP server") {
			t.Fatalf("expected shutdown error, got %v", err)
		}
	})
}

func TestRunTelemetryShutdown(t *testing.T) {
	for _, test := range []struct {
		name        string
		shutdownErr error
	}{
		{name: "success"},
		{name: "failure", shutdownErr: errors.New("shutdown failed")},
	} {
		t.Run(test.name, func(t *testing.T) {
			deps := successDependencies(t)
			shutdownCalled := false
			deps.initTelemetry = func(context.Context, sharedtelemetry.ServiceName) (*telemetry.InitResult, error) {
				return &telemetry.InitResult{
					LogHandler: slog.NewTextHandler(io.Discard, nil),
					Shutdown: func(context.Context) error {
						shutdownCalled = true
						return test.shutdownErr
					},
				}, nil
			}
			if err := run(context.Background(), deps); err != nil {
				t.Fatalf("run returned error: %v", err)
			}
			if !shutdownCalled {
				t.Fatal("telemetry shutdown was not called")
			}
		})
	}
}

func TestHealthHandler(t *testing.T) {
	for _, test := range []struct {
		name       string
		closed     bool
		statusCode int
		status     string
	}{
		{name: "healthy", statusCode: http.StatusOK, status: "ok"},
		{name: "degraded", closed: true, statusCode: http.StatusServiceUnavailable, status: "degraded"},
	} {
		t.Run(test.name, func(t *testing.T) {
			request := httptest.NewRequest(http.MethodGet, "/health", nil)
			response := httptest.NewRecorder()
			healthHandler(fakeHealth{closed: test.closed}).ServeHTTP(response, request)
			if response.Code != test.statusCode || response.Header().Get("Content-Type") != "application/json" {
				t.Fatalf("unexpected response: code=%d headers=%v", response.Code, response.Header())
			}
			var body struct {
				Status     string `json:"status"`
				PostgreSQL bool   `json:"postgresql"`
			}
			if err := json.NewDecoder(response.Body).Decode(&body); err != nil {
				t.Fatalf("decode health response: %v", err)
			}
			if body.Status != test.status || body.PostgreSQL == test.closed {
				t.Fatalf("unexpected health body: %#v", body)
			}
		})
	}
}

func successDependencies(t *testing.T) dependencies {
	t.Helper()
	deps := productionDependencies()
	deps.loadConfig = func() (*config.Config, error) { return testServiceConfig(), nil }
	deps.newWorkDirs = func(string) (*workdir.Manager, error) { return &workdir.Manager{}, nil }
	deps.cleanupStaleWorkDirs = func(*workdir.Manager) (int, error) { return 2, nil }
	deps.newStorage = func(*config.Config) (*storage.S3Client, error) { return &storage.S3Client{}, nil }
	deps.newConnection = func(string) (*mq.Connection, error) { return &mq.Connection{}, nil }
	deps.closeConnection = func(*mq.Connection) error { return nil }
	deps.newPublisher = func(*mq.Connection) (*mq.Publisher, error) { return &mq.Publisher{}, nil }
	deps.closePublisher = func(*mq.Publisher) error { return nil }
	deps.newOptimizer = optimizer.NewCLI
	deps.newProcessor = func(handler.Config, handler.WorkDirManager, handler.StorageClient, handler.MeshOptimizer, handler.EventPublisher) (*handler.Processor, error) {
		return &handler.Processor{}, nil
	}
	deps.newConsumer = func(*mq.Connection, jobs.QueueConfig, mq.Handler) (*mq.Consumer, error) { return &mq.Consumer{}, nil }
	deps.startConsumer = func(*mq.Consumer, context.Context) error { return nil }
	deps.closeConsumer = func(*mq.Consumer) error { return nil }
	deps.listen = func(string, string) (net.Listener, error) { return net.Listen("tcp", "127.0.0.1:0") }
	deps.serve = (*http.Server).Serve
	deps.shutdownServer = (*http.Server).Shutdown
	deps.notifySignals = func(ch chan<- os.Signal, _ ...os.Signal) { ch <- syscall.SIGTERM }
	deps.stopSignals = func(chan<- os.Signal) {}
	deps.initTelemetry = func(context.Context, sharedtelemetry.ServiceName) (*telemetry.InitResult, error) {
		return nil, errors.New("telemetry disabled in test")
	}
	return deps
}

func testServiceConfig() *config.Config {
	return &config.Config{
		Port:                   0,
		InstanceID:             "worker-a",
		TempDir:                "/tmp/unused",
		MaxInputBytes:          config.DefaultMaxInputBytes,
		DatabaseDSN:            "postgres://geul_asset_optimizer@postgres/geul",
		GLTFTransformPath:      "gltf-transform",
		NodeBinaryPath:         "node",
		ParticleMeshScriptPath: "scripts/optimize-particle-mesh.mjs",
		WorkerCount:            1,
		JobTimeoutMins:         20,
		LogLevel:               "info",
	}
}

type fakeHealth struct {
	closed bool
}

func (h fakeHealth) Healthy() bool { return !h.closed }

package telemetry

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	sharedtelemetry "github.com/echovisionlab/geul-telemetry"
	sdklog "go.opentelemetry.io/otel/sdk/log"
	"go.opentelemetry.io/otel/sdk/resource"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	semconv "go.opentelemetry.io/otel/semconv/v1.26.0"
)

func TestCanonicalResourceServiceNameCannotBeOverriddenByEnvironment(t *testing.T) {
	t.Setenv("OTEL_SERVICE_NAME", "wrong-service")
	t.Setenv("OTEL_RESOURCE_ATTRIBUTES", "service.name=also-wrong,deployment.environment=test")

	res, err := newCanonicalResource(t.Context(), sharedtelemetry.ServiceAssetOptimizer.String())
	if err != nil {
		t.Fatal(err)
	}
	value, ok := res.Set().Value(semconv.ServiceNameKey)
	if !ok || value.AsString() != sharedtelemetry.ServiceAssetOptimizer.String() {
		t.Fatalf("service.name = %q, want %q", value.AsString(), sharedtelemetry.ServiceAssetOptimizer)
	}
}

type testLogExporter struct {
	shutdowns int
}

type testTraceExporter struct {
	shutdowns   int
	shutdownErr error
}

func (*testTraceExporter) ExportSpans(context.Context, []sdktrace.ReadOnlySpan) error { return nil }
func (e *testTraceExporter) Shutdown(context.Context) error {
	e.shutdowns++
	return e.shutdownErr
}

func (*testLogExporter) Export(context.Context, []sdklog.Record) error { return nil }
func (*testLogExporter) ForceFlush(context.Context) error              { return nil }
func (e *testLogExporter) Shutdown(context.Context) error {
	e.shutdowns++
	return nil
}

func TestInitCreatesLogProviderAndShutsItDown(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()
	t.Setenv("OTEL_EXPORTER_OTLP_ENDPOINT", server.URL)
	t.Setenv("OTEL_EXPORTER_OTLP_PROTOCOL", "http/protobuf")

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	result, err := Init(ctx, sharedtelemetry.ServiceAssetOptimizer)
	if err != nil {
		t.Fatalf("Init returned error: %v", err)
	}
	if result.LogHandler == nil || !result.LogHandler.Enabled(ctx, slog.LevelInfo) {
		t.Fatal("expected enabled OTLP log handler")
	}
	if err := result.Shutdown(ctx); err != nil {
		t.Fatalf("Shutdown returned error: %v", err)
	}
}

func TestInitReportsDependencyFailures(t *testing.T) {
	want := errors.New("factory failed")
	tests := []struct {
		name   string
		deps   initDependencies
		prefix string
	}{
		{
			name: "resource",
			deps: initDependencies{
				newResource: func(context.Context, string) (*resource.Resource, error) { return nil, want },
			},
			prefix: "create telemetry resource",
		},
		{
			name: "trace exporter",
			deps: initDependencies{
				newResource:      func(context.Context, string) (*resource.Resource, error) { return resource.Empty(), nil },
				newTraceExporter: func(context.Context) (sdktrace.SpanExporter, error) { return nil, want },
			},
			prefix: "create OTLP trace exporter",
		},
		{
			name: "log exporter",
			deps: initDependencies{
				newResource:      func(context.Context, string) (*resource.Resource, error) { return resource.Empty(), nil },
				newTraceExporter: func(context.Context) (sdktrace.SpanExporter, error) { return &testTraceExporter{}, nil },
				newLogExporter:   func(context.Context) (sdklog.Exporter, error) { return nil, want },
			},
			prefix: "create OTLP log exporter",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			result, err := initWithDependencies(context.Background(), sharedtelemetry.ServiceAssetOptimizer, test.deps)
			if result != nil || err == nil || !strings.Contains(err.Error(), test.prefix) || !errors.Is(err, want) {
				t.Fatalf("result=%#v error=%v", result, err)
			}
		})
	}
}

func TestInitWithDependenciesUsesExporterLifecycle(t *testing.T) {
	logExporter := &testLogExporter{}
	traceExporter := &testTraceExporter{}
	result, err := initWithDependencies(context.Background(), sharedtelemetry.ServiceAssetOptimizer, initDependencies{
		newResource:      func(context.Context, string) (*resource.Resource, error) { return resource.Empty(), nil },
		newTraceExporter: func(context.Context) (sdktrace.SpanExporter, error) { return traceExporter, nil },
		newLogExporter:   func(context.Context) (sdklog.Exporter, error) { return logExporter, nil },
	})
	if err != nil {
		t.Fatalf("initWithDependencies returned error: %v", err)
	}
	if result.LogHandler == nil {
		t.Fatal("expected log handler")
	}
	if err := result.Shutdown(context.Background()); err != nil || logExporter.shutdowns != 1 || traceExporter.shutdowns != 1 {
		t.Fatalf("shutdown error=%v log_calls=%d trace_calls=%d", err, logExporter.shutdowns, traceExporter.shutdowns)
	}
}

func TestShutdownTraceProviderWrapsExporterFailure(t *testing.T) {
	want := errors.New("trace exporter shutdown failed")
	exporter := &testTraceExporter{shutdownErr: want}
	provider := sdktrace.NewTracerProvider(sdktrace.WithSyncer(exporter))
	err := shutdownTraceProvider(context.Background(), provider)
	if !errors.Is(err, want) || exporter.shutdowns != 1 {
		t.Fatalf("shutdown error=%v calls=%d", err, exporter.shutdowns)
	}
}

func TestInitRejectsUnknownServiceIdentity(t *testing.T) {
	result, err := initWithDependencies(context.Background(), "asset-optimizer-test", initDependencies{})
	if result != nil || err == nil || !strings.Contains(err.Error(), "unknown canonical service name") {
		t.Fatalf("result=%#v error=%v", result, err)
	}
}

type fanoutSink struct {
	records        int
	withAttrsCalls int
	withGroupCalls int
}

type fanoutTestHandler struct {
	sink     *fanoutSink
	minLevel slog.Level
	err      error
}

func (h fanoutTestHandler) Enabled(_ context.Context, level slog.Level) bool {
	return level >= h.minLevel
}
func (h fanoutTestHandler) Handle(context.Context, slog.Record) error {
	if h.err != nil {
		return h.err
	}
	h.sink.records++
	return nil
}
func (h fanoutTestHandler) WithAttrs([]slog.Attr) slog.Handler {
	h.sink.withAttrsCalls++
	return h
}
func (h fanoutTestHandler) WithGroup(string) slog.Handler {
	h.sink.withGroupCalls++
	return h
}

func TestFanoutHandlerRoutesAndDecoratesChildren(t *testing.T) {
	infoSink := &fanoutSink{}
	errorSink := &fanoutSink{}
	handler := NewFanoutHandler(
		fanoutTestHandler{sink: errorSink, minLevel: slog.LevelError},
		fanoutTestHandler{sink: infoSink, minLevel: slog.LevelInfo},
	)
	if !handler.Enabled(context.Background(), slog.LevelInfo) || handler.Enabled(context.Background(), slog.LevelDebug) {
		t.Fatal("unexpected fanout enabled decision")
	}
	record := slog.NewRecord(time.Now(), slog.LevelInfo, "processed", 0)
	if err := handler.Handle(context.Background(), record); err != nil {
		t.Fatalf("Handle returned error: %v", err)
	}
	if infoSink.records != 1 || errorSink.records != 0 {
		t.Fatalf("record counts info=%d error=%d", infoSink.records, errorSink.records)
	}

	decorated := handler.WithAttrs([]slog.Attr{slog.String("job_id", "job-1")}).WithGroup("job")
	if err := decorated.Handle(context.Background(), record); err != nil {
		t.Fatalf("decorated Handle returned error: %v", err)
	}
	if infoSink.withAttrsCalls != 1 || infoSink.withGroupCalls != 1 || errorSink.withAttrsCalls != 1 || errorSink.withGroupCalls != 1 {
		t.Fatalf("decoration calls info=%#v error=%#v", infoSink, errorSink)
	}
}

func TestFanoutHandlerReturnsFirstEnabledChildError(t *testing.T) {
	want := errors.New("handler failed")
	first := &fanoutSink{}
	second := &fanoutSink{}
	handler := NewFanoutHandler(
		fanoutTestHandler{sink: first, minLevel: slog.LevelDebug, err: want},
		fanoutTestHandler{sink: second, minLevel: slog.LevelDebug},
	)
	err := handler.Handle(context.Background(), slog.NewRecord(time.Now(), slog.LevelInfo, "processed", 0))
	if !errors.Is(err, want) || second.records != 0 {
		t.Fatalf("error=%v second records=%d", err, second.records)
	}
}

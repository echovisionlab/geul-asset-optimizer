package telemetry

import (
	"context"
	"errors"
	"fmt"
	"log/slog"

	sharedtelemetry "github.com/echovisionlab/geul-telemetry"
	"go.opentelemetry.io/contrib/bridges/otelslog"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/exporters/otlp/otlplog/otlploghttp"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracehttp"
	"go.opentelemetry.io/otel/log/global"
	"go.opentelemetry.io/otel/propagation"
	sdklog "go.opentelemetry.io/otel/sdk/log"
	"go.opentelemetry.io/otel/sdk/resource"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	semconv "go.opentelemetry.io/otel/semconv/v1.26.0"
)

type InitResult struct {
	LogHandler slog.Handler
	Shutdown   func(context.Context) error
}

func Init(ctx context.Context, serviceName sharedtelemetry.ServiceName) (*InitResult, error) {
	return initWithDependencies(ctx, serviceName, initDependencies{
		newResource: newCanonicalResource,
		newTraceExporter: func(ctx context.Context) (sdktrace.SpanExporter, error) {
			return otlptracehttp.New(ctx)
		},
		newLogExporter: func(ctx context.Context) (sdklog.Exporter, error) {
			return otlploghttp.New(ctx)
		},
	})
}

func newCanonicalResource(ctx context.Context, serviceName string) (*resource.Resource, error) {
	return resource.New(ctx,
		resource.WithFromEnv(),
		resource.WithHost(),
		resource.WithAttributes(semconv.ServiceName(serviceName)),
	)
}

type initDependencies struct {
	newResource      func(context.Context, string) (*resource.Resource, error)
	newTraceExporter func(context.Context) (sdktrace.SpanExporter, error)
	newLogExporter   func(context.Context) (sdklog.Exporter, error)
}

func initWithDependencies(ctx context.Context, serviceName sharedtelemetry.ServiceName, deps initDependencies) (*InitResult, error) {
	canonicalServiceName, err := sharedtelemetry.ParseServiceName(serviceName.String())
	if err != nil {
		return nil, err
	}
	serviceNameValue := canonicalServiceName.String()
	res, err := deps.newResource(ctx, serviceNameValue)
	if err != nil {
		return nil, fmt.Errorf("create telemetry resource: %w", err)
	}
	traceExporter, err := deps.newTraceExporter(ctx)
	if err != nil {
		return nil, fmt.Errorf("create OTLP trace exporter: %w", err)
	}
	traceProvider := sdktrace.NewTracerProvider(
		sdktrace.WithBatcher(traceExporter),
		sdktrace.WithResource(res),
	)
	exporter, err := deps.newLogExporter(ctx)
	if err != nil {
		return nil, errors.Join(
			fmt.Errorf("create OTLP log exporter: %w", err),
			shutdownTraceProvider(ctx, traceProvider),
		)
	}
	provider := sdklog.NewLoggerProvider(
		sdklog.WithProcessor(sdklog.NewBatchProcessor(exporter)),
		sdklog.WithResource(res),
	)
	otel.SetTracerProvider(traceProvider)
	global.SetLoggerProvider(provider)
	otel.SetTextMapPropagator(propagation.NewCompositeTextMapPropagator(
		propagation.TraceContext{},
		propagation.Baggage{},
	))
	return &InitResult{
		LogHandler: otelslog.NewHandler(serviceNameValue, otelslog.WithLoggerProvider(provider)),
		Shutdown: func(ctx context.Context) error {
			return errors.Join(provider.Shutdown(ctx), shutdownTraceProvider(ctx, traceProvider))
		},
	}, nil
}

func shutdownTraceProvider(ctx context.Context, provider *sdktrace.TracerProvider) error {
	if err := provider.Shutdown(ctx); err != nil {
		return fmt.Errorf("trace shutdown: %w", err)
	}
	return nil
}

var NewFanoutHandler = sharedtelemetry.NewFanoutHandler

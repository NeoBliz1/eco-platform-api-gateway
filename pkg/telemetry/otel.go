package telemetry

import (
	"context"
	"fmt"
	"time"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracegrpc"
	"go.opentelemetry.io/otel/propagation"
	"go.opentelemetry.io/otel/sdk/resource"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	semconv "go.opentelemetry.io/otel/semconv/v1.26.0"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
)

// InitTracer configures a global OpenTelemetry TracerProvider targeting Jaeger v2 over gRPC.
func InitTracer(ctx context.Context, serviceName string) (func(context.Context) error, error) {
	conn, err := grpc.NewClient("127.0.0.1:4317",
		grpc.WithTransportCredentials(insecure.NewCredentials()),
	)
	if err != nil {
		return nil, fmt.Errorf("failed to create Jaeger gRPC client: %w", err)
	}

	exporter, err := otlptracegrpc.New(ctx, otlptracegrpc.WithGRPCConn(conn))
	if err != nil {
		_ = conn.Close()
		return nil, fmt.Errorf("failed to create OTLP trace exporter: %w", err)
	}

	res, err := resource.New(ctx,
		resource.WithAttributes(
			semconv.ServiceNameKey.String(serviceName),
			semconv.DeploymentEnvironmentKey.String("local-dev"),
		),
	)
	if err != nil {
		_ = conn.Close()
		return nil, fmt.Errorf("failed to construct OTel resource attributes: %w", err)
	}

	bsp := sdktrace.NewBatchSpanProcessor(
		exporter,
		sdktrace.WithBatchTimeout(5*time.Second),
		sdktrace.WithMaxQueueSize(2048),
	)

	tp := sdktrace.NewTracerProvider(
		sdktrace.WithSampler(sdktrace.AlwaysSample()),
		sdktrace.WithSpanProcessor(bsp),
		sdktrace.WithResource(res),
	)

	otel.SetTracerProvider(tp)
	otel.SetTextMapPropagator(propagation.NewCompositeTextMapPropagator(
		propagation.TraceContext{},
		propagation.Baggage{},
	))

	shutdownFunc := func(shCtx context.Context) error {
		var closeErr error
		if err := tp.Shutdown(shCtx); err != nil {
			closeErr = fmt.Errorf("tracer provider shutdown error: %w", err)
		}
		if err := conn.Close(); err != nil {
			if closeErr != nil {
				closeErr = fmt.Errorf("%v; gRPC channel close error: %w", closeErr, err)
			} else {
				closeErr = fmt.Errorf("gRPC channel close error: %w", err)
			}
		}
		return closeErr
	}

	return shutdownFunc, nil
}
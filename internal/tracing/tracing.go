package tracing

import (
	"context"
	"fmt"
	"log/slog"
	"net/url"
	"os"
	"strings"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracegrpc"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracehttp"
	"go.opentelemetry.io/otel/propagation"
	"go.opentelemetry.io/otel/sdk/resource"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	semconv "go.opentelemetry.io/otel/semconv/v1.26.0"
)

// ShutdownFunc flushes any buffered spans and shuts down the tracer provider.
type ShutdownFunc func(context.Context) error

// Init configures a global OTLP tracer provider. It is a no-op (returns
// nil shutdown) when OTEL_EXPORTER_OTLP_ENDPOINT is not set, so the app can
// run without a collector in development.
//
// Sampler is AlwaysSample so the collector receives every span. The actual
// sampling decision is made downstream by the tail-based sampling processor.
//
// Honors the standard OpenTelemetry env vars:
//   - OTEL_EXPORTER_OTLP_ENDPOINT  (e.g. otel-collector:4317, or
//                                   http://victoriatraces:10428/insert/opentelemetry/v1/traces)
//   - OTEL_EXPORTER_OTLP_PROTOCOL  ("grpc" (default) or "http/protobuf")
//   - OTEL_EXPORTER_OTLP_HEADERS   (e.g. authorization=Bearer ...)
//   - OTEL_EXPORTER_OTLP_INSECURE  (true to disable TLS — gRPC only)
//   - OTEL_SERVICE_NAME            (falls back to the serviceName arg)
func Init(ctx context.Context, serviceName, serviceVersion string) (ShutdownFunc, error) {
	rawEndpoint := strings.TrimSpace(os.Getenv("OTEL_EXPORTER_OTLP_ENDPOINT"))
	if rawEndpoint == "" {
		return nil, nil
	}

	protocol := strings.ToLower(strings.TrimSpace(os.Getenv("OTEL_EXPORTER_OTLP_PROTOCOL")))
	if protocol == "" {
		protocol = "grpc"
	}

	if envName := strings.TrimSpace(os.Getenv("OTEL_SERVICE_NAME")); envName != "" {
		serviceName = envName
	}

	// Route SDK-internal errors (export failures, dropped spans, etc.) into
	// our logger. Without this, OTel logs them through its default handler
	// at low verbosity and the messages are easy to miss.
	otel.SetErrorHandler(otel.ErrorHandlerFunc(func(err error) {
		slog.Error("OpenTelemetry SDK error",
			"error", err,
			"endpoint", rawEndpoint,
			"protocol", protocol,
			"insecure", isInsecure(),
		)
	}))

	exporter, err := newExporter(ctx, protocol, rawEndpoint)
	if err != nil {
		return nil, fmt.Errorf("create otlp exporter: %w", err)
	}

	res, err := resource.New(ctx,
		resource.WithFromEnv(),
		resource.WithProcess(),
		resource.WithTelemetrySDK(),
		resource.WithAttributes(
			semconv.ServiceName(serviceName),
			semconv.ServiceVersion(serviceVersion),
		),
	)
	if err != nil {
		return nil, fmt.Errorf("build resource: %w", err)
	}

	tp := sdktrace.NewTracerProvider(
		sdktrace.WithBatcher(exporter),
		sdktrace.WithResource(res),
		sdktrace.WithSampler(sdktrace.AlwaysSample()),
	)

	otel.SetTracerProvider(tp)
	otel.SetTextMapPropagator(propagation.NewCompositeTextMapPropagator(
		propagation.TraceContext{},
		propagation.Baggage{},
	))

	return tp.Shutdown, nil
}

func isInsecure() bool {
	v := strings.TrimSpace(os.Getenv("OTEL_EXPORTER_OTLP_INSECURE"))
	switch strings.ToLower(v) {
	case "1", "true", "yes":
		return true
	default:
		return false
	}
}

// newExporter builds a trace exporter for the requested protocol.
//
//   - "grpc"          → otlptracegrpc, endpoint as host:port (scheme stripped).
//   - "http/protobuf" → otlptracehttp. If the endpoint includes a path
//                       (e.g. VictoriaTraces' /insert/opentelemetry/v1/traces)
//                       we use WithEndpointURL so the SDK doesn't append
//                       /v1/traces itself; otherwise WithEndpoint + the
//                       default path.
func newExporter(ctx context.Context, protocol, rawEndpoint string) (*otlptrace.Exporter, error) {
	switch protocol {
	case "grpc":
		ep := strings.TrimPrefix(rawEndpoint, "http://")
		ep = strings.TrimPrefix(ep, "https://")
		opts := []otlptracegrpc.Option{otlptracegrpc.WithEndpoint(ep)}
		if isInsecure() {
			opts = append(opts, otlptracegrpc.WithInsecure())
		}
		return otlptracegrpc.New(ctx, opts...)

	case "http", "http/protobuf":
		// If user supplied a full URL (with scheme), pass it through verbatim
		// so any custom path (VictoriaTraces ingest, etc.) is preserved.
		if u, err := url.Parse(rawEndpoint); err == nil && u.Scheme != "" && u.Host != "" {
			return otlptracehttp.New(ctx, otlptracehttp.WithEndpointURL(rawEndpoint))
		}
		// Otherwise treat as host:port and use the OTLP default /v1/traces path.
		opts := []otlptracehttp.Option{otlptracehttp.WithEndpoint(rawEndpoint)}
		if isInsecure() {
			opts = append(opts, otlptracehttp.WithInsecure())
		}
		return otlptracehttp.New(ctx, opts...)

	default:
		return nil, fmt.Errorf("unsupported OTEL_EXPORTER_OTLP_PROTOCOL: %q (use grpc or http/protobuf)", protocol)
	}
}

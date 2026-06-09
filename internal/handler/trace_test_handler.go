package handler

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"time"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/trace"
)

var traceTestTracer = otel.Tracer("trace-test")

// TraceTestHandler exposes /success, /failure, /latency endpoints used to
// validate the OTel collector's tail-based sampling policy.
type TraceTestHandler struct{}

func NewTraceTestHandler() *TraceTestHandler {
	return &TraceTestHandler{}
}

// child runs fn inside a child span, sleeps `dur`, logs entry/exit. Logs
// pick up trace_id automatically via the global trace-aware slog handler.
func child(ctx context.Context, name string, dur time.Duration, fn func(ctx context.Context) error) error {
	ctx, span := traceTestTracer.Start(ctx, name)
	defer span.End()
	span.SetAttributes(attribute.Int64("sim.sleep_ms", dur.Milliseconds()))

	slog.DebugContext(ctx, "span.start", "span", name)
	time.Sleep(dur)

	var err error
	if fn != nil {
		err = fn(ctx)
	}
	if err != nil {
		span.RecordError(err)
		span.SetStatus(codes.Error, err.Error())
		return err
	}
	slog.DebugContext(ctx, "span.end", "span", name)
	return nil
}

func writeJSON(w http.ResponseWriter, status int, body any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(body)
}

// Success: a fast, healthy request. Tail sampler should keep ~1% of these.
//
// Trace shape: handler → cache.lookup → store.user.fetch → response.encode
// Total wall time ~25ms.
func (TraceTestHandler) Success(w http.ResponseWriter, r *http.Request) {
	ctx, span := traceTestTracer.Start(r.Context(), "trace-test.success")
	defer span.End()
	span.SetAttributes(attribute.String("scenario", "success"))

	slog.InfoContext(ctx, "handling success request", "path", r.URL.Path)

	_ = child(ctx, "cache.lookup", 4*time.Millisecond, func(ctx context.Context) error {
		trace.SpanFromContext(ctx).SetAttributes(
			attribute.String("cache.key", "user:42"),
			attribute.Bool("cache.hit", true),
		)
		return nil
	})
	_ = child(ctx, "store.user.fetch", 12*time.Millisecond, func(ctx context.Context) error {
		trace.SpanFromContext(ctx).SetAttributes(
			attribute.String("db.system", "postgresql"),
			attribute.String("db.operation", "select"),
			attribute.Int("db.rows_returned", 1),
		)
		return nil
	})
	_ = child(ctx, "response.encode", 2*time.Millisecond, nil)

	slog.InfoContext(ctx, "success request handled", "status", http.StatusOK)
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

// Failure: a request that hits a transient downstream outage. Always returns
// 500. Tail sampler should keep 100% of these.
//
// Trace shape: handler → cache.lookup (miss) → store.user.fetch (FAIL: db
// connection refused) → 500.
func (TraceTestHandler) Failure(w http.ResponseWriter, r *http.Request) {
	ctx, span := traceTestTracer.Start(r.Context(), "trace-test.failure")
	defer span.End()
	span.SetAttributes(attribute.String("scenario", "failure"))

	slog.InfoContext(ctx, "handling failure request", "path", r.URL.Path)

	_ = child(ctx, "cache.lookup", 3*time.Millisecond, func(ctx context.Context) error {
		trace.SpanFromContext(ctx).SetAttributes(
			attribute.String("cache.key", "user:42"),
			attribute.Bool("cache.hit", false),
		)
		slog.WarnContext(ctx, "cache miss, falling through to primary store", "cache.key", "user:42")
		return nil
	})

	dbErr := errors.New("dial tcp 10.0.1.42:5432: connect: connection refused")
	storeErr := child(ctx, "store.user.fetch", 35*time.Millisecond, func(ctx context.Context) error {
		s := trace.SpanFromContext(ctx)
		s.SetAttributes(
			attribute.String("db.system", "postgresql"),
			attribute.String("db.operation", "select"),
			attribute.String("net.peer.name", "postgres-primary"),
			attribute.Int("net.peer.port", 5432),
		)
		slog.ErrorContext(ctx, "primary database unreachable",
			"db.system", "postgresql",
			"net.peer.name", "postgres-primary",
			"error", dbErr,
		)
		return dbErr
	})

	span.RecordError(storeErr)
	span.SetStatus(codes.Error, "primary database unreachable")
	span.SetAttributes(attribute.String("error.kind", "downstream_unavailable"))

	slog.ErrorContext(ctx, "failure request returning 500",
		"status", http.StatusInternalServerError,
		"error", storeErr,
	)
	writeJSON(w, http.StatusInternalServerError, map[string]string{
		"error":   "internal_error",
		"message": "database unavailable",
	})
}

// Latency: a slow but successful request — the kind of trace tail-based
// sampling should keep even when no error is present. Total wall time
// ~1.5s, dominated by a slow upstream call.
//
// Trace shape: handler → auth.validate → upstream.search (slow) →
// response.encode.
func (TraceTestHandler) Latency(w http.ResponseWriter, r *http.Request) {
	ctx, span := traceTestTracer.Start(r.Context(), "trace-test.latency")
	defer span.End()
	span.SetAttributes(attribute.String("scenario", "latency"))

	slog.InfoContext(ctx, "handling latency request", "path", r.URL.Path)

	_ = child(ctx, "auth.validate", 30*time.Millisecond, func(ctx context.Context) error {
		trace.SpanFromContext(ctx).SetAttributes(
			attribute.String("auth.method", "jwt"),
			attribute.Bool("auth.cached", true),
		)
		return nil
	})

	_ = child(ctx, "upstream.search", 1400*time.Millisecond, func(ctx context.Context) error {
		s := trace.SpanFromContext(ctx)
		s.SetAttributes(
			attribute.String("peer.service", "search-service"),
			attribute.String("rpc.system", "grpc"),
			attribute.String("rpc.method", "Search"),
			attribute.Int("search.results", 247),
		)
		slog.WarnContext(ctx, "upstream call exceeded SLO",
			"peer.service", "search-service",
			"slo_ms", 500,
			"observed_ms", 1400,
		)
		return nil
	})

	_ = child(ctx, "response.encode", 50*time.Millisecond, nil)

	slog.InfoContext(ctx, "latency request handled", "status", http.StatusOK)
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok", "slow": "true"})
}

package handler

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
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

// Heavy: a realistic multi-tier request — auth, two DB reads, an outbound
// HTTP call, a cache write, and a background job enqueue. No artificial
// errors or extreme latency; total wall time ~120ms.
//
// Trace shape (10 spans):
//
//	handler
//	├── auth.token.validate      (jwt decode + claims check)
//	├── db.user.fetch            (primary read)
//	├── db.permissions.fetch     (secondary read, parallel in real code)
//	├── cache.user.write         (populate after DB miss)
//	├── http.downstream.enrich   (outbound call to enrichment service)
//	│   └── http.downstream.retry (one transparent retry)
//	├── business.rules.evaluate  (CPU-bound policy check)
//	├── db.audit.insert          (write audit log)
//	└── queue.job.enqueue        (async background job)
func (TraceTestHandler) Heavy(w http.ResponseWriter, r *http.Request) {
	ctx, root := traceTestTracer.Start(r.Context(), "trace-test.heavy",
		trace.WithAttributes(
			attribute.String("scenario", "heavy"),
			attribute.String("http.method", r.Method),
			attribute.String("http.target", r.URL.Path),
			attribute.String("http.scheme", "http"),
			attribute.String("net.peer.ip", r.RemoteAddr),
		),
	)
	defer root.End()

	slog.InfoContext(ctx, "handling heavy request", "path", r.URL.Path)

	// 1. Auth — JWT decode + claims validation
	_ = child(ctx, "auth.token.validate", 8*time.Millisecond, func(ctx context.Context) error {
		s := trace.SpanFromContext(ctx)
		s.SetAttributes(
			attribute.String("auth.method", "jwt"),
			attribute.String("auth.scheme", "Bearer"),
			attribute.Bool("auth.cached", false),
			attribute.String("auth.subject", "user:8821"),
			attribute.StringSlice("auth.scopes", []string{"read:profile", "read:orders", "write:audit"}),
			attribute.Int("auth.token.exp_in_s", 3542),
		)
		s.AddEvent("claims.validated", trace.WithAttributes(
			attribute.String("auth.subject", "user:8821"),
		))
		return nil
	})

	// 2. DB — fetch user row (primary)
	var userID int64 = 8821
	_ = child(ctx, "db.user.fetch", 14*time.Millisecond, func(ctx context.Context) error {
		s := trace.SpanFromContext(ctx)
		s.SetAttributes(
			attribute.String("db.system", "postgresql"),
			attribute.String("db.name", "appdb"),
			attribute.String("db.operation", "SELECT"),
			attribute.String("db.sql.table", "users"),
			attribute.String("db.statement", "SELECT id,email,display_name,plan,created_at FROM users WHERE id=$1"),
			attribute.Int64("db.user.id", userID),
			attribute.Int("db.rows_returned", 1),
			attribute.String("net.peer.name", "postgres-primary"),
			attribute.Int("net.peer.port", 5432),
			attribute.Int("db.connection_pool.wait_ms", 1),
		)
		return nil
	})

	// 3. DB — fetch permissions (would be parallel in real code)
	_ = child(ctx, "db.permissions.fetch", 11*time.Millisecond, func(ctx context.Context) error {
		s := trace.SpanFromContext(ctx)
		s.SetAttributes(
			attribute.String("db.system", "postgresql"),
			attribute.String("db.name", "appdb"),
			attribute.String("db.operation", "SELECT"),
			attribute.String("db.sql.table", "user_permissions"),
			attribute.String("db.statement", "SELECT permission FROM user_permissions WHERE user_id=$1"),
			attribute.Int64("db.user.id", userID),
			attribute.Int("db.rows_returned", 4),
			attribute.String("net.peer.name", "postgres-primary"),
			attribute.Int("net.peer.port", 5432),
		)
		return nil
	})

	// 4. Cache — write fetched user back to cache
	_ = child(ctx, "cache.user.write", 3*time.Millisecond, func(ctx context.Context) error {
		s := trace.SpanFromContext(ctx)
		s.SetAttributes(
			attribute.String("cache.system", "redis"),
			attribute.String("cache.operation", "SET"),
			attribute.String("cache.key", fmt.Sprintf("user:%d", userID)),
			attribute.Int("cache.ttl_s", 300),
			attribute.String("net.peer.name", "redis-primary"),
			attribute.Int("net.peer.port", 6379),
		)
		return nil
	})

	// 5. Outbound HTTP — call enrichment service (with one retry)
	_ = child(ctx, "http.downstream.enrich", 38*time.Millisecond, func(ctx context.Context) error {
		s := trace.SpanFromContext(ctx)
		s.SetAttributes(
			attribute.String("http.method", "GET"),
			attribute.String("http.url", fmt.Sprintf("http://enrichment-svc/v1/profile/%d", userID)),
			attribute.String("peer.service", "enrichment-svc"),
			attribute.String("net.peer.name", "enrichment-svc"),
			attribute.Int("net.peer.port", 80),
			attribute.Int("http.status_code", 200),
			attribute.Int("http.response_content_length", 412),
		)
		// Simulate a transparent retry — first attempt took 503, second succeeded
		_ = child(ctx, "http.downstream.retry", 18*time.Millisecond, func(ctx context.Context) error {
			trace.SpanFromContext(ctx).SetAttributes(
				attribute.Int("http.retry.attempt", 1),
				attribute.Int("http.retry.previous_status", 503),
				attribute.String("peer.service", "enrichment-svc"),
			)
			return nil
		})
		return nil
	})

	// 6. Business logic — CPU-bound policy evaluation (no I/O)
	_ = child(ctx, "business.rules.evaluate", 5*time.Millisecond, func(ctx context.Context) error {
		s := trace.SpanFromContext(ctx)
		s.SetAttributes(
			attribute.Int("rules.evaluated", 12),
			attribute.Int("rules.matched", 3),
			attribute.String("rules.outcome", "allow"),
			attribute.String("user.plan", "pro"),
		)
		s.AddEvent("rule.matched", trace.WithAttributes(attribute.String("rule.id", "rate-limit-pro")))
		s.AddEvent("rule.matched", trace.WithAttributes(attribute.String("rule.id", "feature-flag-beta")))
		s.AddEvent("rule.matched", trace.WithAttributes(attribute.String("rule.id", "geo-allow-eu")))
		return nil
	})

	// 7. DB — write audit record
	_ = child(ctx, "db.audit.insert", 9*time.Millisecond, func(ctx context.Context) error {
		s := trace.SpanFromContext(ctx)
		s.SetAttributes(
			attribute.String("db.system", "postgresql"),
			attribute.String("db.name", "appdb"),
			attribute.String("db.operation", "INSERT"),
			attribute.String("db.sql.table", "audit_log"),
			attribute.String("db.statement", "INSERT INTO audit_log(user_id,action,ip,ts) VALUES($1,$2,$3,now())"),
			attribute.Int64("db.user.id", userID),
			attribute.String("audit.action", "profile.read"),
			attribute.String("net.peer.name", "postgres-primary"),
			attribute.Int("net.peer.port", 5432),
		)
		return nil
	})

	// 8. Queue — enqueue background job
	_ = child(ctx, "queue.job.enqueue", 6*time.Millisecond, func(ctx context.Context) error {
		s := trace.SpanFromContext(ctx)
		s.SetAttributes(
			attribute.String("messaging.system", "rabbitmq"),
			attribute.String("messaging.destination", "profile-sync"),
			attribute.String("messaging.destination_kind", "queue"),
			attribute.String("messaging.operation", "publish"),
			attribute.String("messaging.message_id", fmt.Sprintf("msg-%d-%d", userID, time.Now().UnixMilli())),
			attribute.Int("messaging.message_payload_size_bytes", 256),
			attribute.String("net.peer.name", "rabbitmq"),
			attribute.Int("net.peer.port", 5672),
		)
		return nil
	})

	root.SetAttributes(attribute.Int("http.status_code", http.StatusOK))
	slog.InfoContext(ctx, "heavy request handled", "status", http.StatusOK, "user_id", userID)
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok", "user_id": fmt.Sprintf("%d", userID)})
}

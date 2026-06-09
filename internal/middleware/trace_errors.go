package middleware

import (
	"bytes"
	"log/slog"
	"net/http"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/trace"
)

// errorCaptureWriter buffers the response body so we can attach it to the span
// when the status code indicates an error. It still writes through to the
// underlying ResponseWriter so client behavior is unchanged.
type errorCaptureWriter struct {
	http.ResponseWriter
	status      int
	wroteHeader bool
	buf         bytes.Buffer
}

func (w *errorCaptureWriter) WriteHeader(code int) {
	if !w.wroteHeader {
		w.status = code
		w.wroteHeader = true
		w.ResponseWriter.WriteHeader(code)
	}
}

func (w *errorCaptureWriter) Write(b []byte) (int, error) {
	if !w.wroteHeader {
		w.status = http.StatusOK
		w.wroteHeader = true
	}
	// Cap body capture so a giant response can't bloat span attributes.
	const bodyCapBytes = 4096
	if remaining := bodyCapBytes - w.buf.Len(); remaining > 0 {
		if len(b) <= remaining {
			w.buf.Write(b)
		} else {
			w.buf.Write(b[:remaining])
		}
	}
	return w.ResponseWriter.Write(b)
}

// TraceErrors records non-2xx responses onto the active OTel span. Without
// this, errors raised by middleware that runs *before* the handler (e.g.
// the oapi-codegen request validator returning "security requirements
// failed: missing AuthenticationFunc") never appear as errors in traces.
//
// Mount this *inside* the otelchi server span so the active span is the
// request span when we annotate it.
func TraceErrors(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		cw := &errorCaptureWriter{ResponseWriter: w, status: http.StatusOK}
		next.ServeHTTP(cw, r)

		span := trace.SpanFromContext(r.Context())
		if !span.SpanContext().IsValid() {
			return
		}

		span.SetAttributes(attribute.Int("http.status_code", cw.status))

		if cw.status >= 400 {
			body := cw.buf.String()
			attrs := []attribute.KeyValue{
				attribute.Int("http.status_code", cw.status),
				attribute.String("http.method", r.Method),
				attribute.String("http.route", r.URL.Path),
			}
			if body != "" {
				attrs = append(attrs, attribute.String("http.response.body", body))
			}
			span.AddEvent("http.error", trace.WithAttributes(attrs...))

			level := slog.LevelWarn
			if cw.status >= 500 {
				level = slog.LevelError
				span.SetStatus(codes.Error, http.StatusText(cw.status))
			} else {
				span.SetAttributes(attribute.String("error.kind", "client"))
			}
			slog.Log(r.Context(), level, "http error response",
				"status", cw.status,
				"method", r.Method,
				"path", r.URL.Path,
				"body", body,
			)
		}
	})
}

package main

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"time"

	"github.com/getkin/kin-openapi/openapi3"
	"github.com/joho/godotenv"

	"com.tom-ludwig/go-server-template/internal/api/health"
	"com.tom-ludwig/go-server-template/internal/api/users"
	"com.tom-ludwig/go-server-template/internal/config"
	"com.tom-ludwig/go-server-template/internal/logging"
	"com.tom-ludwig/go-server-template/internal/middleware"
	"com.tom-ludwig/go-server-template/internal/repository"
	"com.tom-ludwig/go-server-template/internal/routes"
	"com.tom-ludwig/go-server-template/internal/tracing"
)

const serviceName = "go-server-template"

func main() {
	err := godotenv.Load()
	if err != nil {
		// Use a temporary logger before config is loaded
		logger := slog.New(slog.NewJSONHandler(os.Stdout, nil))
		slog.SetDefault(logger)
		slog.Warn("Error loading .env file", "error", err)
	}

	// Load configuration
	cfg := config.Load()

	// Setup Logger with configured log level, wrapped to inject trace_id/span_id
	// from the active OTel span on every log line.
	opts := &slog.HandlerOptions{
		Level: cfg.LogLevel,
	}
	logger := slog.New(logging.NewTraceHandler(slog.NewJSONHandler(os.Stdout, opts)))
	slog.SetDefault(logger)

	// Initialize tracing. No-op when OTEL_EXPORTER_OTLP_ENDPOINT is unset.
	shutdownTracer, err := tracing.Init(context.Background(), serviceName, "dev")
	if err != nil {
		slog.Error("Failed to initialize tracing", "error", err)
		os.Exit(1)
	}
	if shutdownTracer != nil {
		defer func() {
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			if err := shutdownTracer(ctx); err != nil {
				slog.Error("Error shutting down tracer", "error", err)
			}
		}()
		slog.Info("Tracing enabled", "service", serviceName, "endpoint", os.Getenv("OTEL_EXPORTER_OTLP_ENDPOINT"))
	}

	// dbpool, err := connectToDatabase(cfg)
	// if err != nil {
	// 	slog.Error("Failed to connect to database", "error", err)
	// 	os.Exit(1)
	// }
	// defer dbpool.Close()
	// slog.Info("Successfully connected to database")
	//
	// queries := repository.New(dbpool)
	var queries *repository.Queries // DB disabled

	// Initialize JWT auth if OIDC is enabled
	var jwtAuth *middleware.JWTAuth
	if cfg.OIDCEnabled {
		if cfg.OIDCIssuer == "" {
			slog.Error("OIDC_ISSUER must be set when OIDC_ENABLED is true")
			os.Exit(1)
		}
		var err error
		jwtAuth, err = middleware.NewJWTAuth(context.Background(), cfg.OIDCIssuer, cfg.OIDCAudience)
		if err != nil {
			slog.Error("Failed to initialize JWT auth", "error", err)
			os.Exit(1)
		}
		slog.Info("JWT authentication enabled", "issuer", cfg.OIDCIssuer)
	}

	router := routes.NewRouter(cfg, queries, jwtAuth)

	// Print registered routes in debug mode
	if cfg.LogLevel == slog.LevelDebug {
		// Add swagger specs here when you create new OpenAPI files
		swaggers := []*openapi3.T{}
		if s, err := health.GetSpec(); err == nil {
			swaggers = append(swaggers, s)
		}
		if s, err := users.GetSpec(); err == nil {
			swaggers = append(swaggers, s)
		}
		routes.PrintRoutes(router, swaggers)
	}

	port := fmt.Sprintf(":%s", cfg.Port)
	server := &http.Server{
		Addr:         port,
		Handler:      router,
		ReadTimeout:  15 * time.Second,
		WriteTimeout: 15 * time.Second,
		IdleTimeout:  60 * time.Second,
	}

	slog.Info("Server starting", "port", cfg.Port, "log_level", cfg.LogLevel.String())
	err = server.ListenAndServe()
	if err != nil && err != http.ErrServerClosed {
		slog.Error("Server failed to start", "error", err)
		os.Exit(1)
	}
}

// connectToDatabase is kept for reference when re-enabling the DB.
// func connectToDatabase(cfg *config.Config) (*pgxpool.Pool, error) {
// 	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
// 	defer cancel()
//
// 	var dsn string
// 	if !cfg.PGLocal {
// 		dsn = fmt.Sprintf(
// 			"host=%s port=%s dbname=%s user=%s sslmode=%s sslcert=%s sslkey=%s sslrootcert=%s password=%s",
// 			cfg.PGHost, cfg.PGPort, cfg.PGDB, cfg.PGUser, cfg.PGSSLMode,
// 			cfg.PGTLSCert, cfg.PGTLSKey, cfg.PGSSLRootCert, cfg.PGPassword,
// 		)
// 	} else {
// 		dsn = fmt.Sprintf(
// 			"host=%s port=%s dbname=%s user=%s password=%s sslmode=%s",
// 			cfg.PGHost, cfg.PGPort, cfg.PGDB, cfg.PGUser, cfg.PGPassword, cfg.PGSSLMode,
// 		)
// 	}
//
// 	config, err := pgxpool.ParseConfig(dsn)
// 	if err != nil {
// 		return nil, fmt.Errorf("failed to parse database configuration: %w", err)
// 	}
//
// 	config.ConnConfig.Tracer = otelpgx.NewTracer()
// 	return pgxpool.NewWithConfig(ctx, config)
// }

package main

import (
	"context"
	"math/rand"
	"net/http"
	"os"
	"time"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/exporters/otlp/otlpmetric/otlpmetrichttp"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracehttp"
	"go.opentelemetry.io/otel/metric"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/resource"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	semconv "go.opentelemetry.io/otel/semconv/v1.21.0"
	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
)

func initTracer(ctx context.Context, otelCollector string) (*sdktrace.TracerProvider, error) {
	res, err := resource.New(ctx,
		resource.WithAttributes(
			semconv.ServiceName("go-sample-app"),
			semconv.ServiceVersion("1.0.0"),
		),
	)
	if err != nil {
		return nil, err
	}

	traceExp, err := otlptracehttp.New(ctx,
		otlptracehttp.WithEndpoint(otelCollector),
		otlptracehttp.WithInsecure(),
	)
	if err != nil {
		return nil, err
	}

	tp := sdktrace.NewTracerProvider(
		sdktrace.WithBatcher(traceExp),
		sdktrace.WithResource(res),
	)
	otel.SetTracerProvider(tp)
	return tp, nil
}

func initMeter(ctx context.Context, otelCollector string) (*sdkmetric.MeterProvider, error) {
	res, err := resource.New(ctx,
		resource.WithAttributes(
			semconv.ServiceName("go-sample-app"),
			semconv.ServiceVersion("1.0.0"),
		),
	)
	if err != nil {
		return nil, err
	}

	metricExp, err := otlpmetrichttp.New(ctx,
		otlpmetrichttp.WithEndpoint(otelCollector),
		otlpmetrichttp.WithInsecure(),
	)
	if err != nil {
		return nil, err
	}

	mp := sdkmetric.NewMeterProvider(
		sdkmetric.WithReader(
			sdkmetric.NewPeriodicReader(metricExp,
				sdkmetric.WithInterval(1*time.Second),
			),
		),
		sdkmetric.WithResource(res),
	)
	otel.SetMeterProvider(mp)
	return mp, nil
}

func initLogger() *zap.Logger {
	// Create Zap logger configuration
	config := zap.NewProductionConfig()
	config.EncoderConfig.TimeKey = "timestamp"
	config.EncoderConfig.EncodeTime = zapcore.ISO8601TimeEncoder

	// Create logger
	logger, err := config.Build()
	if err != nil {
		panic(err)
	}

	return logger
}

func main() {
	ctx := context.Background()

	otelCollector := os.Getenv("OTEL_COLLECTOR_ENDPOINT")
	if otelCollector == "" {
		otelCollector = "localhost:4318"
	}

	// Initialize logger
	logger := initLogger()
	defer logger.Sync()

	// Replace global logger
	zap.ReplaceGlobals(logger)

	// Initialize tracer provider
	tp, err := initTracer(ctx, otelCollector)
	if err != nil {
		panic("failed to initialize tracer provider: " + err.Error())
	}
	defer func() {
		if err := tp.Shutdown(ctx); err != nil {
			logger.Error("Error shutting down tracer provider", zap.Error(err))
		}
	}()

	// Initialize meter provider
	mp, err := initMeter(ctx, otelCollector)
	if err != nil {
		panic("failed to initialize meter provider: " + err.Error())
	}
	defer func() {
		if err := mp.Shutdown(ctx); err != nil {
			logger.Error("Error shutting down meter provider", zap.Error(err))
		}
	}()

	// Create instruments
	meter := mp.Meter("http-server")
	tracer := tp.Tracer("http-server")

	requestCounter, err := meter.Int64Counter(
		"http.requests.total",
		metric.WithDescription("Total number of HTTP requests"),
	)
	if err != nil {
		logger.Fatal("failed to create request counter", zap.Error(err))
	}

	requestDuration, err := meter.Float64Histogram(
		"http.request.duration",
		metric.WithDescription("HTTP request duration"),
		metric.WithUnit("ms"),
	)
	if err != nil {
		logger.Fatal("failed to create request duration histogram", zap.Error(err))
	}

	http.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		ctx, span := tracer.Start(r.Context(), "handle_request")
		span.SetAttributes(
			attribute.String("path", r.URL.Path),
			attribute.String("method", r.Method),
		)
		defer span.End()

		startTime := time.Now()

		// Log request
		logger.Info("handling request",
			zap.String("path", r.URL.Path),
			zap.String("method", r.Method),
			zap.String("remote_addr", r.RemoteAddr),
		)

		// Simulate some work
		time.Sleep(time.Duration(rand.Intn(100)) * time.Millisecond)

		// Record metrics
		requestCounter.Add(ctx, 1,
			metric.WithAttributes(
				attribute.String("path", r.URL.Path),
				attribute.String("method", r.Method),
			),
		)

		duration := float64(time.Since(startTime).Milliseconds())
		requestDuration.Record(ctx, duration,
			metric.WithAttributes(
				attribute.String("path", r.URL.Path),
				attribute.String("method", r.Method),
			),
		)

		// Log response
		logger.Info("request completed",
			zap.String("path", r.URL.Path),
			zap.String("method", r.Method),
			zap.Float64("duration_ms", duration),
			zap.Int("status", http.StatusOK),
		)

		w.Header().Set("Content-Type", "text/plain")
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("Hello, World!"))
	})

	logger.Info("starting server", zap.String("address", ":8080"))
	if err := http.ListenAndServe(":8080", nil); err != nil {
		logger.Fatal("server failed", zap.Error(err))
	}
}

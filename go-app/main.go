package main

import (
	"context"
	"github.com/pyroscope-io/client/pyroscope"
	"math/rand"
	"net/http"
	"os"
	"runtime"
	"runtime/pprof"
	"time"

	_ "net/http/pprof"

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

func handleRequest(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	tracer := otel.Tracer("go-sample-app")
	ctx, span := tracer.Start(ctx, "handleRequest")
	defer span.End()

	// Add trace ID to pprof labels
	traceID := span.SpanContext().TraceID().String()
	labels := pprof.Labels("trace_id", traceID)

	// Set labels for the main goroutine
	ctx = pprof.WithLabels(ctx, labels)
	pprof.SetGoroutineLabels(ctx)
	defer pprof.SetGoroutineLabels(context.Background())

	startTime := time.Now()
	logger := zap.L()

	// Log request with trace ID
	logger.Info("handling request",
		zap.String("path", r.URL.Path),
		zap.String("method", r.Method),
		zap.String("remote_addr", r.RemoteAddr),
		zap.String("trace_id", traceID),
	)

	// Simulate CPU-intensive work
	for i := 0; i < 100; i++ {
		_ = make([]byte, 1024*1024) // Allocate more memory
		time.Sleep(time.Duration(rand.Intn(10)) * time.Millisecond)
	}

	// Get trace ID from span context
	traceID = span.SpanContext().TraceID().String()

	// Create attributes for metrics
	attrs := []attribute.KeyValue{
		attribute.String("path", r.URL.Path),
		attribute.String("method", r.Method),
		attribute.String("trace_id", traceID),
	}

	// Record metrics (trace ID will be automatically used as exemplar)
	meter := otel.Meter("http-server")
	requestCounter, err := meter.Int64Counter(
		"http.requests.total",
		metric.WithDescription("Total number of HTTP requests"),
	)
	if err != nil {
		logger.Fatal("failed to create request counter", zap.Error(err))
	}
	requestCounter.Add(ctx, 1, metric.WithAttributes(attrs...))

	duration := float64(time.Since(startTime).Milliseconds())
	requestDuration, err := meter.Float64Histogram(
		"http.request.duration",
		metric.WithDescription("HTTP request duration"),
		metric.WithUnit("ms"),
	)
	if err != nil {
		logger.Fatal("failed to create request duration histogram", zap.Error(err))
	}
	requestDuration.Record(ctx, duration, metric.WithAttributes(attrs...))

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
}

func main() {
	ctx := context.Background()

	// Enable profiling with higher sampling rates
	runtime.SetMutexProfileFraction(1)
	runtime.SetBlockProfileRate(1)
	runtime.SetCPUProfileRate(100)

	// Start profiling server
	go func() {
		// If you're using Pyroscope Go SDK, initialize pyroscope profiler.
		_, _ = pyroscope.Start(pyroscope.Config{
			ApplicationName: "my-go-app",
			ServerAddress:   "http://localhost:4040",
		})
	}()

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

	http.HandleFunc("/hello", handleRequest)

	logger.Info("Server starting on :8080")

	if err := http.ListenAndServe(":8080", nil); err != nil {
		logger.Fatal("failed to start server", zap.Error(err))
	}
}

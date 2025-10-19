package main

import (
	"os"
	"log"
	"math/rand"
	"net/http"
	"time"
	"strconv"
	"context"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
	"gopkg.in/natefinch/lumberjack.v2"

	"go.opentelemetry.io/otel"
    "go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracegrpc"
    "go.opentelemetry.io/otel/sdk/resource"
    sdktrace "go.opentelemetry.io/otel/sdk/trace"
    "go.opentelemetry.io/otel/semconv/v1.21.0"
    "google.golang.org/grpc"
)

func randomFloat(min, max float64) float64 {
    return min + rand.Float64()*(max-min)
}

var Iteration = "iteration"

var tracer = otel.Tracer("example-tracer")

func doRequest(ctx context.Context) {
	ctx, span := tracer.Start(ctx, "do-request")
    defer span.End()

	go op1(ctx, "work1")
	go op1(ctx, "work2")

	time.Sleep(time.Second)
}

func op1(ctx context.Context, name string) {
    _, span := tracer.Start(ctx, name)
    defer span.End()

	time.Sleep(2 * time.Second)
}

func main() {
	ctx := context.Background()

	shutdown := initTracer()
	defer shutdown()

    ctx, span := tracer.Start(ctx, "main")
    defer span.End()

// Rotating file writer
    lumberjackLogger := &lumberjack.Logger{
        Filename:   "../log/app.log",
        MaxSize:    100, // MB
        MaxBackups: 7,
        MaxAge:     30, // days
        Compress:   true,
    }

	fileSyncer := zapcore.AddSync(lumberjackLogger)
    fileEncoder := zapcore.NewJSONEncoder(zap.NewProductionEncoderConfig())
    fileCore := zapcore.NewCore(fileEncoder, fileSyncer, zapcore.InfoLevel)

    consoleSyncer := zapcore.AddSync(os.Stdout)
    consoleEncoder := zapcore.NewConsoleEncoder(zap.NewDevelopmentEncoderConfig())
    consoleCore := zapcore.NewCore(consoleEncoder, consoleSyncer, zapcore.DebugLevel)
	
    combinedCore := zapcore.NewTee(fileCore, consoleCore)
    lg := zap.New(combinedCore)
    defer lg.Sync()

	// Create a custom counter metric
	requests := prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "test_requests_total",
			Help: "Total number of test requests",
		},
		[]string{"endpoint"},
	)

	// Create a custom gauge metric
	temperature := prometheus.NewGauge(
		prometheus.GaugeOpts{
			Name: "test_temperature_celsius",
			Help: "Random test temperature in Celsius",
		},
	)

	requestDuration := prometheus.NewHistogramVec(prometheus.HistogramOpts{
        Name:    "xxx_http_request_duration_seconds",
        Help:    "Histogram of HTTP request durations.",
        Buckets: prometheus.LinearBuckets(0.05, 0.05, 20), // 0.05s to 1.0s
    }, []string{Iteration})

	// Register metrics with Prometheus
	prometheus.MustRegister(requests)
	prometheus.MustRegister(temperature)
	prometheus.MustRegister(requestDuration)

	// Simulate metric updates in a background goroutine
	go func() {
		endpoints := []string{"/foo", "/bar", "/baz"}
		for i := 0; ; i++{

			latency := randomFloat(0, 1)

			// Increment requests randomly
			req := endpoints[rand.Intn(len(endpoints))]
			requests.WithLabelValues(req).Add(1)

			requestDuration.WithLabelValues(strconv.Itoa(i)).Observe(latency)

			lg.Info("in progress ...", zap.Int("iteration", i), zap.Float64("latency", latency))

			// Set a random temperature
			temperature.Set(20 + rand.Float64()*10)

			doRequest(ctx)

			time.Sleep(2 * time.Second)
		}
	}()

	// Expose metrics endpoint
	http.Handle("/metrics", promhttp.Handler())
	log.Println("Prometheus test metrics running on :8080/metrics")
	if err := http.ListenAndServe(":8080", nil); err != nil {
		log.Fatal(err)
	}
}

func initTracer() func() {
    ctx := context.Background()

    // Create OTLP gRPC exporter
    exporter, err := otlptracegrpc.New(ctx,
        otlptracegrpc.WithInsecure(), // no TLS
        otlptracegrpc.WithEndpoint("localhost:4317"), // Grafana Agent / Tempo OTLP gRPC
        otlptracegrpc.WithDialOption(grpc.WithBlock()),
    )
    if err != nil {
        log.Fatal(err)
    }

    // Create trace provider
    tp := sdktrace.NewTracerProvider(
        sdktrace.WithBatcher(exporter),
        sdktrace.WithResource(resource.NewWithAttributes(
            semconv.SchemaURL,
            semconv.ServiceNameKey.String("my-go-service"),
        )),
    )

    otel.SetTracerProvider(tp)

    return func() {
        _ = tp.Shutdown(ctx)
    }
}
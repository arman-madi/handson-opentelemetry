package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"math/rand"
	"net/http"
	"os"
	"time"

	// "go.opentelemetry.io/contrib/instrumentation/net/http/httptrace/otelhttptrace"
	"go.opentelemetry.io/contrib/instrumentation/net/http/otelhttp"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/exporters/jaeger"
	"go.opentelemetry.io/otel/exporters/stdout/stdouttrace"
	"go.opentelemetry.io/otel/exporters/zipkin"
	"go.opentelemetry.io/otel/propagation"
	"go.opentelemetry.io/otel/sdk/resource"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	semconv "go.opentelemetry.io/otel/semconv/v1.4.0"
	"go.opentelemetry.io/otel/trace"
)

type Dhl struct {
	Address string   `json:"address"`
	Basket  []string `json:"basket"`
}

var logger = log.New(os.Stderr, "[dhl] ", log.Ldate|log.Ltime|log.Llongfile)

// Create one tracer per package
// NOTE: You only need a tracer if you are creating your own spans
var tracer trace.Tracer

// initTracer creates a new trace provider instance and registers it as global trace provider.
func initTracer() /*(*sdktrace.TracerProvider, error)*/ func() {

	// ** STDOUT Exporter
	stdoutExporter, err := stdouttrace.New( /*stdouttrace.WithPrettyPrint()*/ )
	if err != nil {
		log.Fatal("failed to initialize stdouttrace exporter: ", err)
	}

	// ** Jaeger Exporter
	jaegerUrl := "http://jaeger:14268/api/traces"
	jaegerExporter, err := jaeger.New(
		jaeger.WithCollectorEndpoint(jaeger.WithEndpoint(jaegerUrl)),
	)
	if err != nil {
		log.Fatal("failed to initialize jaeger exporter: ", err)
	}

	// ** Zipkin Exporter
	zipkinUrl := "http://zipkin:9411/api/v2/spans"
	zipkinExporter, err := zipkin.New(
		zipkinUrl,
		// zipkin.WithLogger(logger),
	)
	if err != nil {
		log.Fatal(err)
	}

	// ** Trace Provider
	// For demoing purposes, always sample. In a production application, you should
	// configure the sampler to a trace.ParentBased(trace.TraceIDRatioBased) set at the desired
	// ratio.
	tp := sdktrace.NewTracerProvider(
		sdktrace.WithSampler(sdktrace.AlwaysSample()),
		sdktrace.WithBatcher(zipkinExporter, sdktrace.WithMaxExportBatchSize(1)),
		sdktrace.WithBatcher(jaegerExporter, sdktrace.WithMaxExportBatchSize(1)),
		sdktrace.WithBatcher(stdoutExporter, sdktrace.WithMaxExportBatchSize(1)),
		sdktrace.WithResource(resource.NewWithAttributes(
			semconv.SchemaURL,
			semconv.ServiceNameKey.String("dhl"),
			attribute.String("environment", "demo"),
			attribute.Int64("ID", 4),
		)),
	)

	// Register our TracerProvider as the global so any imported
	// instrumentation in the future will default to using it.
	otel.SetTracerProvider(tp)

	// Name the tracer after the package, or the service if you are in main
	tracer = otel.Tracer("handson-opentelemetry/dhl")

	// Register the TraceContext propagator globally.
	otel.SetTextMapPropagator(propagation.TraceContext{})

	return func() {
		_ = tp.Shutdown(context.Background())
	}
}

func main() {
	logger.Println("Hello, this is dhl service which is responsible to ship goods via DHL in order to demonestrate how OpenTelemetry works!")

	shutdown := initTracer()
	defer shutdown()

	dhlHandler := func(w http.ResponseWriter, req *http.Request) {

		ctx := req.Context()
		span := trace.SpanFromContext(ctx)
		traceId := span.SpanContext().TraceID().String()
		logger.Printf("Handle request with trace id: %+v\n", traceId)

		var dhl Dhl
		err := json.NewDecoder(req.Body).Decode(&dhl)
		if err != nil {
			span.AddEvent("Error decoding dhl json", trace.WithAttributes(attribute.Key("err").String(err.Error())))
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		logger.Printf("New request received: %+v\n", dhl)

		ship(ctx, dhl)

		_, _ = io.WriteString(w, fmt.Sprintf("{\"trace-id\": \"%v\"}\n", traceId))
	}

	otelHandler := otelhttp.NewHandler(http.HandlerFunc(dhlHandler), "handle-dhl")

	http.Handle("/", otelHandler)
	logger.Printf("Listening on port 80\n")
	http.ListenAndServe(":80", nil)
}

func ship(ctx context.Context, dhl Dhl) {
	ctx, span := tracer.Start(ctx, "dhl-ship")
	defer span.End()

	span.AddEvent("Start shipping with DHL")

	<-time.After(time.Second * time.Duration(rand.Intn(3)))

	span.SetAttributes(attribute.StringSlice("Products", dhl.Basket))
	span.AddEvent("Successfully shipped with DHL")

}

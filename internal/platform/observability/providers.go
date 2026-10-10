// Package observability configures nonfatal asynchronous telemetry exporting.
package observability

import (
	"context"
	"errors"
	"github.com/Diaszano/election-bot/internal/platform/config"
	"go.opentelemetry.io/contrib/bridges/otelslog"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/exporters/otlp/otlplog/otlploggrpc"
	"go.opentelemetry.io/otel/exporters/otlp/otlpmetric/otlpmetricgrpc"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracegrpc"
	"go.opentelemetry.io/otel/propagation"
	sdklog "go.opentelemetry.io/otel/sdk/log"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/resource"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/trace"
	"log/slog"
	"os"
	"time"
)

type Runtime struct {
	Logger *slog.Logger
	trace  *sdktrace.TracerProvider
	metric *sdkmetric.MeterProvider
	log    *sdklog.LoggerProvider
}
type handler struct{ json, otel slog.Handler }

func (h handler) Enabled(c context.Context, l slog.Level) bool { return h.json.Enabled(c, l) }
func (h handler) Handle(c context.Context, r slog.Record) error {
	sc := trace.SpanContextFromContext(c)
	if sc.IsValid() {
		r.AddAttrs(slog.String("trace_id", sc.TraceID().String()), slog.String("span_id", sc.SpanID().String()))
	}
	e := h.json.Handle(c, r)
	if h.otel != nil {
		e = errors.Join(e, h.otel.Handle(c, r))
	}
	return e
}
func (h handler) WithAttrs(a []slog.Attr) slog.Handler {
	h.json = h.json.WithAttrs(a)
	if h.otel != nil {
		h.otel = h.otel.WithAttrs(a)
	}
	return h
}
func (h handler) WithGroup(g string) slog.Handler {
	h.json = h.json.WithGroup(g)
	if h.otel != nil {
		h.otel = h.otel.WithGroup(g)
	}
	return h
}
func Setup(ctx context.Context, c config.Config, version string) (*Runtime, error) {
	level := slog.LevelInfo
	if e := level.UnmarshalText([]byte(c.LogLevel)); e != nil {
		return nil, e
	}
	r := &Runtime{}
	h := handler{json: slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: level})}
	r.Logger = slog.New(h)
	slog.SetDefault(r.Logger)
	if !c.OTELEnabled {
		return r, nil
	}
	res := resource.NewWithAttributes("", attribute.String("service.name", "election-bot"), attribute.String("service.namespace", "elections"), attribute.String("service.version", version), attribute.String("deployment.environment", c.Environment))
	te, e := otlptracegrpc.New(ctx, otlptracegrpc.WithEndpointURL(c.OTLPEndpoint), otlptracegrpc.WithTimeout(2*time.Second))
	if e != nil {
		return nil, e
	}
	r.trace = sdktrace.NewTracerProvider(sdktrace.WithResource(res), sdktrace.WithSampler(sdktrace.ParentBased(sdktrace.TraceIDRatioBased(c.Sampling))), sdktrace.WithBatcher(te, sdktrace.WithBatchTimeout(time.Second), sdktrace.WithExportTimeout(2*time.Second)))
	me, e := otlpmetricgrpc.New(ctx, otlpmetricgrpc.WithEndpointURL(c.OTLPEndpoint), otlpmetricgrpc.WithTimeout(2*time.Second))
	if e != nil {
		r.Shutdown(ctx)
		return nil, e
	}
	r.metric = sdkmetric.NewMeterProvider(sdkmetric.WithResource(res), sdkmetric.WithReader(sdkmetric.NewPeriodicReader(me, sdkmetric.WithInterval(5*time.Second), sdkmetric.WithTimeout(2*time.Second))))
	le, e := otlploggrpc.New(ctx, otlploggrpc.WithEndpointURL(c.OTLPEndpoint), otlploggrpc.WithTimeout(2*time.Second))
	if e != nil {
		r.Shutdown(ctx)
		return nil, e
	}
	r.log = sdklog.NewLoggerProvider(sdklog.WithResource(res), sdklog.WithProcessor(sdklog.NewBatchProcessor(le, sdklog.WithExportInterval(time.Second), sdklog.WithExportTimeout(2*time.Second))))
	h.otel = otelslog.NewHandler("election-bot", otelslog.WithLoggerProvider(r.log))
	r.Logger = slog.New(h)
	slog.SetDefault(r.Logger)
	otel.SetTracerProvider(r.trace)
	otel.SetMeterProvider(r.metric)
	otel.SetTextMapPropagator(propagation.TraceContext{})
	otel.SetErrorHandler(otel.ErrorHandlerFunc(func(error) { slog.New(h.json).Warn("telemetry export failed") }))
	return r, nil
}
func (r *Runtime) Shutdown(ctx context.Context) error {
	var e error
	if r.metric != nil {
		e = errors.Join(e, r.metric.Shutdown(ctx))
	}
	if r.log != nil {
		e = errors.Join(e, r.log.Shutdown(ctx))
	}
	if r.trace != nil {
		e = errors.Join(e, r.trace.Shutdown(ctx))
	}
	return e
}

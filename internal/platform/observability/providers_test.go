package observability

import (
	"context"
	"encoding/json"
	"github.com/Diaszano/election-bot/internal/platform/config"
	"go.opentelemetry.io/otel"
	logpb "go.opentelemetry.io/proto/otlp/collector/logs/v1"
	metricpb "go.opentelemetry.io/proto/otlp/collector/metrics/v1"
	tracepb "go.opentelemetry.io/proto/otlp/collector/trace/v1"
	"google.golang.org/grpc"
	"net"
	"strings"
	"sync"
	"testing"
	"time"
)

type receiver struct {
	mu                    sync.Mutex
	payloads              []string
	logs, traces, metrics int
}
type logsReceiver struct {
	logpb.UnimplementedLogsServiceServer
	r *receiver
}

func (s *logsReceiver) Export(ctx context.Context, r *logpb.ExportLogsServiceRequest) (*logpb.ExportLogsServiceResponse, error) {
	s.r.mu.Lock()
	defer s.r.mu.Unlock()
	b, _ := json.Marshal(r)
	s.r.payloads = append(s.r.payloads, string(b))
	s.r.logs++
	return &logpb.ExportLogsServiceResponse{}, nil
}

type tracesReceiver struct {
	tracepb.UnimplementedTraceServiceServer
	r *receiver
}

func (s *tracesReceiver) Export(ctx context.Context, r *tracepb.ExportTraceServiceRequest) (*tracepb.ExportTraceServiceResponse, error) {
	s.r.mu.Lock()
	defer s.r.mu.Unlock()
	b, _ := json.Marshal(r)
	s.r.payloads = append(s.r.payloads, string(b))
	s.r.traces++
	return &tracepb.ExportTraceServiceResponse{}, nil
}

type metricsReceiver struct {
	metricpb.UnimplementedMetricsServiceServer
	r *receiver
}

func (s *metricsReceiver) Export(ctx context.Context, r *metricpb.ExportMetricsServiceRequest) (*metricpb.ExportMetricsServiceResponse, error) {
	s.r.mu.Lock()
	defer s.r.mu.Unlock()
	b, _ := json.Marshal(r)
	s.r.payloads = append(s.r.payloads, string(b))
	s.r.metrics++
	return &metricpb.ExportMetricsServiceResponse{}, nil
}
func TestAllSignalsExportAndCorrelation(t *testing.T) {
	l, e := net.Listen("tcp", "127.0.0.1:0")
	if e != nil {
		t.Fatal(e)
	}
	srv := grpc.NewServer()
	r := &receiver{}
	logpb.RegisterLogsServiceServer(srv, &logsReceiver{r: r})
	tracepb.RegisterTraceServiceServer(srv, &tracesReceiver{r: r})
	metricpb.RegisterMetricsServiceServer(srv, &metricsReceiver{r: r})
	go srv.Serve(l)
	defer srv.Stop()
	c, _ := config.Load()
	c.OTELEnabled = true
	c.OTLPEndpoint = "http://" + l.Addr().String()
	runtime, e := Setup(context.Background(), c, "test-version")
	if e != nil {
		t.Fatal(e)
	}
	ctx, span := otel.Tracer("election-bot").Start(context.Background(), "election.poll")
	runtime.Logger.InfoContext(ctx, "safe operation")
	counter, _ := otel.Meter("election-bot").Int64Counter("election_poll")
	counter.Add(ctx, 1)
	span.End()
	timeout, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if e = runtime.Shutdown(timeout); e != nil {
		t.Fatal(e)
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.logs == 0 || r.traces == 0 || r.metrics == 0 {
		t.Fatalf("signals logs=%d traces=%d metrics=%d", r.logs, r.traces, r.metrics)
	}
	joined := strings.Join(r.payloads, "\n")
	for _, v := range []string{"election-bot", "elections", "test-version", "election_poll", "trace_id", "span_id"} {
		if !strings.Contains(joined, v) {
			t.Fatal("missing exported field", v)
		}
	}
}

package observations

import (
	"context"
	"expvar"
	"net/http"
	"net/http/pprof"

	ocgorpc "github.com/lanzafame/go-libp2p-ocgorpc"
	rpc "github.com/libp2p/go-libp2p-gorpc"

	"go.opencensus.io/exporter/jaeger"
	"go.opencensus.io/exporter/prometheus"
	"go.opencensus.io/plugin/ochttp"
	"go.opencensus.io/stats/view"
	"go.opencensus.io/trace"
	"go.opencensus.io/zpages"

	manet "github.com/multiformats/go-multiaddr-net"

	prom "github.com/prometheus/client_golang/prometheus"
)

// SetupMetrics configures and starts stats tooling,
// if enabled.
func SetupMetrics(cfg *MetricsConfig) error {
	if cfg.EnableStats {
		logger.Info("stats collection enabled...")
		return setupMetrics(cfg)
	}
	return nil
}

// JaegerTracer implements ipfscluster.Tracer.
type JaegerTracer struct {
	jaeger *jaeger.Exporter
}

// SetClient no-op.
func (t *JaegerTracer) SetClient(*rpc.Client) {}

// Shutdown the tracer and flush any remaining traces.
func (t *JaegerTracer) Shutdown(context.Context) error {
	// nil check for testing, where tracer may not be configured
	if t != (*JaegerTracer)(nil) && t.jaeger != nil {
		t.jaeger.Flush()
	}
	return nil
}

// SetupTracing configures and starts tracing tooling,
// if enabled.
func SetupTracing(cfg *TracingConfig) (*JaegerTracer, error) {
	if !cfg.EnableTracing {
		return nil, nil
	}
	logger.Info("tracing enabled...")
	je, err := setupTracing(cfg)
	if err != nil {
		return nil, err
	}
	return &JaegerTracer{je}, nil
}

func setupMetrics(cfg *MetricsConfig) error {
	// setup Prometheus
	registry := prom.NewRegistry()
	goCollector := prom.NewGoCollector()
	procCollector := prom.NewProcessCollector(prom.ProcessCollectorOpts{})
	registry.MustRegister(goCollector, procCollector)
	pe, err := prometheus.NewExporter(prometheus.Options{
		Namespace: "cluster",
		Registry:  registry,
	})
	if err != nil {
		return err
	}

	// register prometheus with opencensus
	view.RegisterExporter(pe)
	view.SetReportingPeriod(cfg.StatsReportingInterval)

	// register the metrics views of interest
	if err := view.Register(DefaultViews...); err != nil {
		return err
	}
	if err := view.Register(
		ochttp.ClientCompletedCount,
		ochttp.ClientRoundtripLatencyDistribution,
		ochttp.ClientReceivedBytesDistribution,
		ochttp.ClientSentBytesDistribution,
	); err != nil {
		return err
	}
	if err := view.Register(
		ochttp.ServerRequestCountView,
		ochttp.ServerRequestBytesView,
		ochttp.ServerResponseBytesView,
		ochttp.ServerLatencyView,
		ochttp.ServerRequestCountByMethod,
		ochttp.ServerResponseCountByStatusCode,
	); err != nil {
		return err
	}
	if err := view.Register(ocgorpc.DefaultServerViews...); err != nil {
		return err
	}

	_, promAddr, err := manet.DialArgs(cfg.PrometheusEndpoint)
	if err != nil {
		return err
	}
	go func() {
		mux := http.NewServeMux()
		zpages.Handle(mux, "/debug")
		mux.Handle("/metrics", pe)
		mux.Handle("/debug/vars", expvar.Handler())
		mux.HandleFunc("/debug/pprof", pprof.Index)
		mux.HandleFunc("/debug/cmdline", pprof.Cmdline)
		mux.HandleFunc("/debug/profile", pprof.Profile)
		mux.HandleFunc("/debug/symbol", pprof.Symbol)
		mux.HandleFunc("/debug/trace", pprof.Trace)
		mux.Handle("/debug/block", pprof.Handler("block"))
		mux.Handle("/debug/goroutine", pprof.Handler("goroutine"))
		mux.Handle("/debug/heap", pprof.Handler("heap"))
		mux.Handle("/debug/mutex", pprof.Handler("mutex"))
		mux.Handle("/debug/threadcreate", pprof.Handler("threadcreate"))
		if err := http.ListenAndServe(promAddr, mux); err != nil {
			logger.Fatalf("Failed to run Prometheus /metrics endpoint: %v", err)
		}
	}()
	return nil
}

// setupTracing configures a OpenCensus Tracing exporter for Jaeger.
func setupTracing(cfg *TracingConfig) (*jaeger.Exporter, error) {
	_, agentAddr, err := manet.DialArgs(cfg.JaegerAgentEndpoint)
	if err != nil {
		return nil, err
	}
	// setup Jaeger
	je, err := jaeger.NewExporter(jaeger.Options{
		AgentEndpoint: agentAddr,
		Process: jaeger.Process{
			ServiceName: cfg.TracingServiceName,
		},
	})
	if err != nil {
		return nil, err
	}

	// register jaeger with opencensus
	trace.RegisterExporter(je)
	// configure tracing
	trace.ApplyConfig(trace.Config{DefaultSampler: trace.ProbabilitySampler(cfg.TracingSamplingProb)})
	return je, nil
}

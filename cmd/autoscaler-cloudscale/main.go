package main

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"flag"
	"fmt"
	"net"
	"net/http"
	"net/http/pprof"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/prometheus/client_golang/prometheus/promhttp"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/health"
	healthpb "google.golang.org/grpc/health/grpc_health_v1"
	"google.golang.org/grpc/reflection"
	"k8s.io/klog/v2"

	"github.com/kubeterm-sh/autoscaler-cloudscale/internal/cloudscale"
	"github.com/kubeterm-sh/autoscaler-cloudscale/internal/config"
	"github.com/kubeterm-sh/autoscaler-cloudscale/internal/interceptor"
	_ "github.com/kubeterm-sh/autoscaler-cloudscale/internal/metrics"
	"github.com/kubeterm-sh/autoscaler-cloudscale/internal/provider"
	"github.com/kubeterm-sh/autoscaler-cloudscale/pkg/version"
	pb "github.com/kubeterm-sh/autoscaler-cloudscale/proto"
)

func main() {
	if err := run(); err != nil {
		klog.ErrorS(err, "fatal error")
		os.Exit(1)
	}
}

func run() error {
	klog.InitFlags(nil)

	configPath := flag.String("config", "", "Path to configuration file")
	metricsAddr := flag.String("metrics-addr", ":9090", "Address for the metrics/health HTTP server")
	flag.Parse()

	if *configPath == "" {
		return errors.New("missing required --config flag")
	}

	klog.InfoS("starting cloudscale autoscaler",
		"version", version.Version,
		"commit", version.Commit,
		"buildDate", version.BuildDate,
	)

	cfg, err := config.Load(*configPath)
	if err != nil {
		return fmt.Errorf("loading config: %w", err)
	}

	token := os.Getenv("CLOUDSCALE_API_TOKEN")
	if token == "" {
		return errors.New("CLOUDSCALE_API_TOKEN environment variable is required")
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	client := cloudscale.New(token, cfg.ClusterTag)

	if err := client.RefreshFlavors(ctx); err != nil {
		return fmt.Errorf("loading flavors: %w", err)
	}

	if err := client.Refresh(ctx); err != nil {
		return fmt.Errorf("loading servers: %w", err)
	}

	prov, err := provider.New(cfg, client)
	if err != nil {
		return fmt.Errorf("creating provider: %w", err)
	}

	serverOpts := []grpc.ServerOption{
		grpc.ChainUnaryInterceptor(
			interceptor.Recovery,
			interceptor.Metrics,
			interceptor.Logging,
		),
	}

	if cfg.TLS != nil {
		creds, err := loadTLSCredentials(cfg.TLS)
		if err != nil {
			return fmt.Errorf("loading TLS credentials: %w", err)
		}
		serverOpts = append(serverOpts, grpc.Creds(creds))
		klog.InfoS("mTLS enabled")
	}

	srv := grpc.NewServer(serverOpts...)
	pb.RegisterCloudProviderServer(srv, prov)

	healthSrv := health.NewServer()
	healthpb.RegisterHealthServer(srv, healthSrv)
	healthSrv.SetServingStatus("", healthpb.HealthCheckResponse_SERVING)

	reflection.Register(srv)

	var lc net.ListenConfig
	lis, err := lc.Listen(ctx, "tcp", cfg.Listen)
	if err != nil {
		return fmt.Errorf("listening on %s: %w", cfg.Listen, err)
	}

	klog.InfoS("gRPC server listening", "address", cfg.Listen)

	httpMux := http.NewServeMux()
	httpMux.Handle("/metrics", promhttp.Handler())
	httpMux.HandleFunc("/healthz", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})

	if os.Getenv("PPROF_ENABLED") == "true" {
		httpMux.HandleFunc("/debug/pprof/", pprof.Index)
		httpMux.HandleFunc("/debug/pprof/cmdline", pprof.Cmdline)
		httpMux.HandleFunc("/debug/pprof/profile", pprof.Profile)
		httpMux.HandleFunc("/debug/pprof/symbol", pprof.Symbol)
		httpMux.HandleFunc("/debug/pprof/trace", pprof.Trace)
		klog.InfoS("pprof enabled", "address", *metricsAddr)
	}

	httpSrv := &http.Server{
		Addr:              *metricsAddr,
		Handler:           httpMux,
		ReadHeaderTimeout: 5 * time.Second,
	}
	errCh := make(chan error, 2)
	go func() {
		klog.InfoS("metrics HTTP server listening", "address", *metricsAddr)
		if err := httpSrv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			klog.ErrorS(err, "metrics HTTP server failed")
			errCh <- fmt.Errorf("metrics HTTP server: %w", err)
		}
	}()
	go func() { errCh <- srv.Serve(lis) }()

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)

	select {
	case sig := <-sigCh:
		klog.InfoS("received signal, shutting down", "signal", sig)
	case err := <-errCh:
		klog.ErrorS(err, "gRPC server failed")
		return err
	}

	healthSrv.SetServingStatus("", healthpb.HealthCheckResponse_NOT_SERVING)

	stopped := make(chan struct{})
	go func() { srv.GracefulStop(); close(stopped) }()
	select {
	case <-stopped:
		klog.InfoS("gRPC server stopped gracefully")
	case <-time.After(15 * time.Second):
		klog.InfoS("graceful stop timed out, forcing stop")
		srv.Stop()
	}

	shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer shutdownCancel()
	if err := httpSrv.Shutdown(shutdownCtx); err != nil {
		klog.ErrorS(err, "metrics HTTP server shutdown failed")
	}

	return nil
}

func loadTLSCredentials(tlsCfg *config.TLS) (credentials.TransportCredentials, error) {
	cert, err := tls.LoadX509KeyPair(tlsCfg.CertFile, tlsCfg.KeyFile)
	if err != nil {
		return nil, fmt.Errorf("loading server cert/key: %w", err)
	}

	caPEM, err := os.ReadFile(tlsCfg.CAFile)
	if err != nil {
		return nil, fmt.Errorf("reading CA file: %w", err)
	}

	caPool := x509.NewCertPool()
	if !caPool.AppendCertsFromPEM(caPEM) {
		return nil, errors.New("failed to parse CA certificate")
	}

	return credentials.NewTLS(&tls.Config{
		Certificates: []tls.Certificate{cert},
		ClientAuth:   tls.RequireAndVerifyClientCert,
		ClientCAs:    caPool,
		MinVersion:   tls.VersionTLS13,
	}), nil
}

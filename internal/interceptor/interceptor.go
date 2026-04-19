package interceptor

import (
	"context"
	"runtime/debug"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"k8s.io/klog/v2"

	"github.com/kubeterm-sh/autoscaler-cloudscale/internal/metrics"
)

func Recovery(ctx context.Context, req any, info *grpc.UnaryServerInfo, handler grpc.UnaryHandler) (resp any, err error) {
	defer func() {
		if r := recover(); r != nil {
			klog.ErrorS(nil, "panic recovered in gRPC handler",
				"method", info.FullMethod,
				"panic", r,
				"stack", string(debug.Stack()),
			)
			err = status.Errorf(codes.Internal, "internal server error")
		}
	}()
	return handler(ctx, req)
}

func Metrics(ctx context.Context, req any, info *grpc.UnaryServerInfo, handler grpc.UnaryHandler) (any, error) {
	start := time.Now()
	resp, err := handler(ctx, req)
	duration := time.Since(start).Seconds()
	code := status.Code(err).String()

	metrics.GRPCRequestsTotal.WithLabelValues(info.FullMethod, code).Inc()
	metrics.GRPCRequestDuration.WithLabelValues(info.FullMethod).Observe(duration)
	return resp, err
}

func Logging(ctx context.Context, req any, info *grpc.UnaryServerInfo, handler grpc.UnaryHandler) (any, error) {
	start := time.Now()
	resp, err := handler(ctx, req)
	duration := time.Since(start)
	code := status.Code(err)

	switch {
	case err != nil && code == codes.Unimplemented:
		klog.V(5).InfoS("gRPC call unimplemented",
			"method", info.FullMethod,
			"duration", duration,
		)
	case err != nil:
		klog.InfoS("gRPC call failed",
			"method", info.FullMethod,
			"code", code.String(),
			"duration", duration,
			"error", err,
		)
	default:
		klog.V(3).InfoS("gRPC call",
			"method", info.FullMethod,
			"code", code.String(),
			"duration", duration,
		)
	}
	return resp, err
}

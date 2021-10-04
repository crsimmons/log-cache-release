package main

import (
	"fmt"
	"log"
	"os"
	"time"

	"code.cloudfoundry.org/log-cache/internal/blackbox"
	"code.cloudfoundry.org/log-cache/internal/plumbing"
	"google.golang.org/grpc"
)

func main() {
	var infoLogger *log.Logger
	var errorLogger *log.Logger

	cfg, err := LoadConfig()
	if err != nil {
		log.Fatalf("failed to load configuration: %s", err)
	}

	if cfg.UseRFC339 {
		infoLogger = log.New(new(plumbing.LogWriter), "", 0)
		errorLogger = log.New(new(plumbing.LogWriter), "", 0)
		log.SetOutput(new(plumbing.LogWriter))
		log.SetFlags(0)
	} else {
		infoLogger = log.New(os.Stderr, "", log.LstdFlags|log.Lmicroseconds)
		errorLogger = log.New(os.Stderr, "", log.LstdFlags|log.Lmicroseconds)
		log.SetFlags(log.LstdFlags | log.Lmicroseconds)
	}

	ingressClient := blackbox.NewIngressClient(
		fmt.Sprintf("dns:///%s", cfg.DataSourceGrpcAddr),
		grpc.WithTransportCredentials(cfg.TLS.Credentials("log-cache")),
		grpc.WithDefaultServiceConfig(`{"loadBalancingPolicy":"round_robin"}`),
	)

	go blackbox.StartEmittingTestMetrics(cfg.SourceId, cfg.EmissionInterval, ingressClient)

	grpcEgressClient := blackbox.NewGrpcEgressClient(
		fmt.Sprintf("dns:///%s", cfg.DataSourceGrpcAddr),
		grpc.WithTransportCredentials(cfg.TLS.Credentials("log-cache")),
		grpc.WithDefaultServiceConfig(`{"loadBalancingPolicy":"round_robin"}`),
	)

	var httpEgressClient blackbox.QueryableClient

	if cfg.CfBlackboxEnabled {
		httpEgressClient = blackbox.NewHttpEgressClient(
			cfg.DataSourceHTTPAddr,
			cfg.UaaAddr,
			cfg.ClientID,
			cfg.ClientSecret,
			cfg.SkipTLSVerify,
		)
	}

	t := time.NewTicker(cfg.SampleInterval)
	rc := blackbox.ReliabilityCalculator{
		SampleInterval:   cfg.SampleInterval,
		WindowInterval:   cfg.WindowInterval,
		WindowLag:        cfg.WindowLag,
		EmissionInterval: cfg.EmissionInterval,
		SourceId:         cfg.SourceId,
		InfoLogger:       infoLogger,
		ErrorLogger:      errorLogger,
	}

	for range t.C {
		reliabilityMetrics := make(map[string]float64)

		infoLogger.Println("Querying for gRPC reliability metric...")
		grpcReliability, err := rc.Calculate(grpcEgressClient)
		if err == nil {
			reliabilityMetrics["blackbox.grpc_reliability"] = grpcReliability
		}

		if cfg.CfBlackboxEnabled {
			infoLogger.Println("Querying for HTTP reliability metric...")
			httpReliability, err := rc.Calculate(httpEgressClient)
			if err == nil {
				reliabilityMetrics["blackbox.http_reliability"] = httpReliability
			}
		}

		blackbox.EmitMeasuredMetrics(cfg.SourceId, ingressClient, httpEgressClient, reliabilityMetrics)
	}
}

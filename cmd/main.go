package main

import (
	"context"
	"flag"
	"log"
	"log/slog"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/maksim-paskal/gitlab-token-rotate/internal"
	"github.com/maksim-paskal/gitlab-token-rotate/pkg/client"
)

func main() {
	application := internal.NewApplication()

	var debug bool

	var gracePeriod time.Duration

	flag.BoolVar(&debug, "debug", false, "enable debug logging")
	flag.DurationVar(&gracePeriod, "grace-period", 5*time.Second, "grace period for shutting down")

	flag.StringVar(&application.KubeConfig, "kubeconfig", application.KubeConfig, "absolute path to the kubeconfig file")
	flag.StringVar(&application.Namespace, "namespace", application.Namespace, "namespace to watch for secrets")
	flag.DurationVar(&application.Interval, "interval", application.Interval, "interval between checks")
	flag.DurationVar(&application.Timeout, "timeout", application.Timeout, "timeout for each check")
	flag.DurationVar(&application.TokenRotation, "token-rotation", application.TokenRotation, "token rotation period")
	flag.DurationVar(&application.TokenValidity, "token-validity", application.TokenValidity, "token validity period")

	flag.Parse()

	level := slog.LevelInfo
	if debug {
		level = slog.LevelDebug
	}

	if _, ok := os.LookupEnv("KUBERNETES_SERVICE_HOST"); ok {
		slog.SetDefault(slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{
			Level: level,
		})))
	} else {
		slog.SetLogLoggerLevel(level)
	}

	if err := client.Init(application.KubeConfig); err != nil {
		log.Fatal("Error initializing application:", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	signalChanInterrupt := make(chan os.Signal, 1)
	signal.Notify(signalChanInterrupt, syscall.SIGINT, syscall.SIGTERM)

	go func() {
		select {
		case <-signalChanInterrupt:
			slog.Warn("Got interruption signal...")
			cancel()
		case <-ctx.Done():
		}
		<-signalChanInterrupt
		os.Exit(1)
	}()

	go application.ScheduleSecretRotation(ctx)
	go application.Start(ctx)

	<-ctx.Done()

	slog.Warn("Grace period...", "period", gracePeriod.String())
	time.Sleep(gracePeriod)
}

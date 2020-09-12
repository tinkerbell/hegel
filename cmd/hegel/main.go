package main

import (
	"context"
	"flag"
	_ "net/http/pprof"
	"os"
	"os/signal"
	"sync"
	"syscall"
	"time"

	"github.com/packethost/pkg/log"
	grpcserver "github.com/tinkerbell/hegel/grpc-server"
	"github.com/tinkerbell/hegel/hardware"
	httpserver "github.com/tinkerbell/hegel/http-server"
	"github.com/tinkerbell/hegel/metrics"
)

var (
	GitRev   string
	logger log.Logger
)

func main() {
	flag.Parse()
	// setup structured logging
	l, err := log.Init("github.com/tinkerbell/hegel")
	if err != nil {
		panic(err)
	}
	logger = l.Package("main")
	defer l.Close()
	metrics.Init(l)

	metrics.State.Set(metrics.Initializing)

	hg, err := hardware.New()
	if err != nil {
		l.Fatal(err, "failed to create hegel server")
	}

	hegelServer := &grpcserver.Server{
		Log:            l,
		HardwareClient: hg,
		Subscriptions:  make(map[string]*grpcserver.Subscription),
	}

	c := make(chan os.Signal)
	signal.Notify(c, os.Interrupt, syscall.SIGTERM)
	var ret error
	ctx, cancel := context.WithCancel(context.TODO())
	var once sync.Once
	var wg sync.WaitGroup
	runGoroutine(&ret, cancel, &once, &wg, func() error {
		return httpserver.Serve(ctx, logger, hegelServer, GitRev, time.Now())
	})
	runGoroutine(&ret, cancel, &once, &wg, func() error {
		return grpcserver.Serve(ctx, logger, hegelServer)
	})
	runGoroutine(&ret, cancel, &once, &wg, func() error {
		select {
		case sig, ok := <-c:
			if ok {
				l.With("signal", sig).Info("received stop signal, gracefully shutting down")
				cancel()
			}
		case <-ctx.Done():
		}
		return nil
	})
	l.Info("waiting")
	wg.Wait()
	if ret != nil {
		l.Fatal(ret)
	}
}

func runGoroutine(ret *error, cancel context.CancelFunc, once *sync.Once, wg *sync.WaitGroup, fn func() error) {
	wg.Add(1)
	go func() {
		defer wg.Done()
		err := fn()
		if err != nil {
			// we only care about the first error
			once.Do(func() {
				*ret = err
				cancel()
			})
		}
	}()
}

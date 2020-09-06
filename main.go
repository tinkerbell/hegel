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

	grpcserver "github.com/packethost/hegel/grpc-server"
	httpserver "github.com/packethost/hegel/http-server"
	"github.com/packethost/hegel/metrics"
	"github.com/packethost/pkg/log"
)

var (
	GitRev string
	logger log.Logger
)

func main() {
	flag.Parse()
	// setup structured logging
	l, err := log.Init("github.com/packethost/hegel")
	logger = l.Package("main")
	if err != nil {
		panic(err)
	}
	defer l.Close()
	metrics.Init(l)

	metrics.State.Set(metrics.Initializing)

	c := make(chan os.Signal)
	signal.Notify(c, os.Interrupt, syscall.SIGTERM)
	var ret error
	ctx, cancel := context.WithCancel(context.TODO())
	var once sync.Once
	var wg sync.WaitGroup
	runGoroutine(&ret, cancel, &once, &wg, func() error {
		return httpserver.Serve(ctx, l, GitRev, time.Now())
	})
	runGoroutine(&ret, cancel, &once, &wg, func() error {
		return grpcserver.Serve(ctx, l)
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

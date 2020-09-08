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

	cacherClient "github.com/packethost/cacher/client"
	"github.com/packethost/cacher/protos/cacher"
	grpcserver "github.com/packethost/hegel/grpc-server"
	hardwaregetter "github.com/packethost/hegel/hardware-getter"
	httpserver "github.com/packethost/hegel/http-server"
	"github.com/packethost/hegel/metrics"
	"github.com/packethost/pkg/env"
	"github.com/packethost/pkg/log"
	tinkClient "github.com/tinkerbell/tink/client"
)

var (
	GitRev   string
	facility = flag.String("facility", env.Get("HEGEL_FACILITY", "onprem"),
		"The facility we are running in (mostly to connect to cacher)")
	logger log.Logger
)

func main() {
	flag.Parse()
	// setup structured logging
	l, err := log.Init("github.com/packethost/hegel")
	if err != nil {
		panic(err)
	}
	logger = l.Package("main")
	defer l.Close()
	metrics.Init(l)

	metrics.State.Set(metrics.Initializing)

	var hg hardwaregetter.Client
	dataModelVersion := env.Get("DATA_MODEL_VERSION")
	switch dataModelVersion {
	case "1":
		tc, err := tinkClient.TinkHardwareClient()
		if err != nil {
			l.Fatal(err, "Failed to create the tink client")
		}
		hg = hardwaregetter.TinkerbellClient{Client: tc}
		// add health check for tink?
	default:
		cc, err := cacherClient.New(*facility)
		if err != nil {
			l.Fatal(err, "Failed to create the cacher client")
		}
		hg = hardwaregetter.CacherClient{Client: cc}
		go func() {
			c := time.Tick(15 * time.Second)
			for range c {
				// Get All hardware as a proxy for a healthcheck
				// TODO (patrickdevivo) until Cacher gets a proper healthcheck RPC
				// a la https://github.com/grpc/grpc/blob/master/doc/health-checking.md
				// this will have to do.
				// Note that we don't do anything with the stream (we don't read from it)
				var isCacherAvailableTemp bool
				ctx, cancel := context.WithCancel(context.Background())
				_, err := cc.All(ctx, &cacher.Empty{})
				if err == nil {
					isCacherAvailableTemp = true
				}
				cancel()

				httpserver.IsCacherAvailableMu.Lock()
				httpserver.IsCacherAvailable = isCacherAvailableTemp
				httpserver.IsCacherAvailableMu.Unlock()

				if isCacherAvailableTemp {
					metrics.CacherConnected.Set(1)
					metrics.CacherHealthcheck.WithLabelValues("true").Inc()
					l.With("status", isCacherAvailableTemp).Debug("tick")
				} else {
					metrics.CacherConnected.Set(0)
					metrics.CacherHealthcheck.WithLabelValues("false").Inc()
					metrics.Errors.WithLabelValues("cacher", "healthcheck").Inc()
					l.With("status", isCacherAvailableTemp).Error(err)
				}
			}

		}()
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

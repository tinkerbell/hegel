package httpserver

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"net/http"
	"sync"
	"time"

	"github.com/packethost/pkg/env"
	"github.com/packethost/pkg/log"
	"github.com/pkg/errors"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	grpcserver "github.com/tinkerbell/hegel/grpc-server"
	"github.com/tinkerbell/hegel/metrics"
	"github.com/tinkerbell/hegel/xff"
)

var (
	isCacherAvailableMu sync.RWMutex
	isCacherAvailable   bool
	StartTime           time.Time
	metricsPort         = flag.Int("http_port", env.Int("HEGEL_HTTP_PORT", 50061), "Port to liten on http")
	customEndpoints     string
	gitRev              string
	gitRevJSON          []byte
	logger              log.Logger
	hegelServer         *grpcserver.Server
)

func Serve(ctx context.Context, l log.Logger, srv *grpcserver.Server, gRev string, time time.Time) error {
	StartTime = time
	gitRev = gRev
	logger = l
	hegelServer = srv

	go checkCacherHealth()

	mux := &http.ServeMux{}
	mux.Handle("/metrics", promhttp.Handler())
	mux.HandleFunc("/_packet/healthcheck", healthCheckHandler)
	mux.HandleFunc("/_packet/version", versionHandler)
	mux.HandleFunc("/2009-04-04", ec2Handler) // workaround for making trailing slash optional
	mux.HandleFunc("/2009-04-04/", ec2Handler)

	buildSubscriberHandlers(hegelServer)

	err := registerCustomEndpoints(mux)
	if err != nil {
		l.Fatal(err, "could not register custom endpoints")
	}

	trustedProxies := xff.ParseTrustedProxies()
	http.Handle("/", xff.HTTPHandler(logger, mux, trustedProxies))

	l.With("port", *metricsPort).Info("Starting http server")
	err = http.ListenAndServe(fmt.Sprintf(":%d", *metricsPort), nil)
	if err != nil {
		l.Error(err, "failed to serve http")
		panic(err)
	}

	return nil
}

func registerCustomEndpoints(mux *http.ServeMux) error {
	customEndpoints = env.Get("CUSTOM_ENDPOINTS", `{"/metadata":".metadata"}`)
	if mux == nil {
		mux = http.DefaultServeMux
	}

	endpoints := make(map[string]string)
	err := json.Unmarshal([]byte(customEndpoints), &endpoints)
	if err != nil {
		return errors.Wrap(err, "error in parsing custom endpoints")
	}
	for endpoint, filter := range endpoints {
		mux.HandleFunc(endpoint, getMetadata(filter))
	}

	return nil
}

func checkCacherHealth() {
	c := time.Tick(15 * time.Second)
	for range c {
		// Get All hardware as a proxy for a healthcheck
		// TODO (patrickdevivo) until Cacher gets a proper healthcheck RPC
		// a la https://github.com/grpc/grpc/blob/master/doc/health-checking.md
		// this will have to do.
		// Note that we don't do anything with the stream (we don't read from it)
		var isCacherAvailableTemp bool
		ctx, cancel := context.WithCancel(context.Background())
		_, err := hegelServer.HardwareClient().All(ctx) // checks for tink health as well
		if err == nil {
			isCacherAvailableTemp = true
		}
		cancel()

		isCacherAvailableMu.Lock()
		isCacherAvailable = isCacherAvailableTemp
		isCacherAvailableMu.Unlock()

		if isCacherAvailableTemp {
			metrics.CacherConnected.Set(1)
			metrics.CacherHealthcheck.WithLabelValues("true").Inc()
			logger.With("status", isCacherAvailableTemp).Debug("tick")
		} else {
			metrics.CacherConnected.Set(0)
			metrics.CacherHealthcheck.WithLabelValues("false").Inc()
			metrics.Errors.WithLabelValues("cacher", "healthcheck").Inc()
			logger.With("status", isCacherAvailableTemp).Error(err)
		}
	}
}

package httpserver

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"net/http"
	"sync"
	"time"

	grpcserver "github.com/packethost/hegel/grpc-server"
	"github.com/packethost/hegel/xff"
	"github.com/packethost/pkg/env"
	"github.com/packethost/pkg/log"
	"github.com/pkg/errors"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

var (
	metricsPort = flag.Int("http_port", env.Int("HEGEL_HTTP_PORT", 50061),
		"Port to liten on http")
	customEndpoints     string
	gitRev              string = "undefind"
	gitRevJSON          []byte
	logger              log.Logger
	isCacherAvailableMu sync.RWMutex
	isCacherAvailable   bool
	StartTime           time.Time
)

func Serve(ctx context.Context, l log.Logger, gitRev string, time time.Time) error {
	StartTime = time
	logger = l

	mux := &http.ServeMux{}
	mux.Handle("/metrics", promhttp.Handler())
	mux.HandleFunc("/_packet/healthcheck", healthCheckHandler)
	mux.HandleFunc("/_packet/version", versionHandler)
	mux.HandleFunc("/2009-04-04", ec2Handler) // workaround for making trailing slash optional
	mux.HandleFunc("/2009-04-04/", ec2Handler)

	buildSubscriberHandlers(grpcserver.HegelServer)

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

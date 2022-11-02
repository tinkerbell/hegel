package http

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/packethost/pkg/log"
	"github.com/pkg/errors"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	"github.com/tinkerbell/hegel/datamodel"
	"github.com/tinkerbell/hegel/hardware"
	"github.com/tinkerbell/hegel/xff"
	"go.opentelemetry.io/contrib/instrumentation/net/http/otelhttp"
)

func Serve(
	ctx context.Context,
	logger log.Logger,
	client hardware.Client,
	port int,
	start time.Time,
	model datamodel.DataModel,
	customEndpoints string,
	unparsedProxies string,
	hegelAPI bool,
) error {
	logger.Info("in the http serve func")
	var mux http.ServeMux
	var httpHandler http.Handler

	mux.Handle("/metrics", promhttp.Handler())
	mux.Handle("/healthz", HealthCheckHandler(logger, client, start))
	mux.Handle("/versionz", VersionHandler(logger))

	if !hegelAPI {
		ec2MetadataHandler := otelhttp.WithRouteTag("/2009-04-04", EC2MetadataHandler(logger, client))
		mux.Handle("/2009-04-04/", ec2MetadataHandler)
		mux.Handle("/2009-04-04", ec2MetadataHandler)

		httpHandler = &mux
	} else {
		router := gin.Default()
		router.RedirectTrailingSlash = true
		v0 := router.Group("/v0")
		v0HegelMetadataHandler(logger, client, v0)

		httpHandler = router
	}

	err := registerCustomEndpoints(logger, client, &mux, model, customEndpoints)
	if err != nil {
		return fmt.Errorf("register custom endpoints: %w", err)
	}

	// Add an X-Forward-For middleware for proxies.
	proxies := xff.ParseTrustedProxies(unparsedProxies)
	handler, err := xff.HTTPHandler(httpHandler, proxies)
	if err != nil {
		return err
	}

	address := fmt.Sprintf(":%d", port)
	server := &http.Server{
		Addr:    address,
		Handler: handler,

		// Mitigate Slowloris attacks. 30 seconds is based on Apache's recommended 20-40
		// recommendation. Hegel doesn't really have many headers so 20s should be plenty of time.
		// https://en.wikipedia.org/wiki/Slowloris_(computer_security)
		ReadHeaderTimeout: 20 * time.Second,
	}
	go func() {
		<-ctx.Done()

		// todo(chrisdoherty4) Refactor server construction and 'listen' to be separate so we can more gracefully
		// shutdown and introduce a timeout before calling Close().
		server.Close()
	}()

	logger.With("address", address).Info("Starting http server")
	return server.ListenAndServe()
}

func registerCustomEndpoints(logger log.Logger, client hardware.Client, mux *http.ServeMux, model datamodel.DataModel, customEndpoints string) error {
	endpoints := make(map[string]string)
	err := json.Unmarshal([]byte(customEndpoints), &endpoints)
	if err != nil {
		return errors.Wrap(err, "error in parsing custom endpoints")
	}

	for endpoint, filter := range endpoints {
		handler := otelhttp.WithRouteTag(endpoint, GetMetadataHandler(logger, client, filter, model))
		mux.Handle(endpoint, handler)
	}

	return nil
}

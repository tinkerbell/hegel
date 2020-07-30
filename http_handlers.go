package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"os"
	"runtime"
	"strings"
	"time"

	"github.com/packethost/hegel/metrics"
)

func versionHandler(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	_, err := w.Write(gitRevJSON)
	if err != nil {
		logger.Error(err, " Failed to write gitRevJSON")
	}
}

func healthCheckHandler(w http.ResponseWriter, r *http.Request) {
	isCacherAvailableMu.RLock()
	isCacherAvailableTemp := isCacherAvailable
	isCacherAvailableMu.RUnlock()

	res := struct {
		GitRev          string  `json:"git_rev"`
		Uptime          float64 `json:"uptime"`
		Goroutines      int     `json:"goroutines"`
		CacherAvailable bool    `json:"cacher_status"`
	}{
		GitRev:          gitRev,
		Uptime:          time.Since(StartTime).Seconds(),
		Goroutines:      runtime.NumGoroutine(),
		CacherAvailable: isCacherAvailableTemp,
	}
	b, err := json.Marshal(&res)
	if err != nil {
		w.WriteHeader(http.StatusInternalServerError)
	}

	if !isCacherAvailableTemp {
		w.WriteHeader(http.StatusInternalServerError)
	}

	w.Header().Set("Content-Type", "application/json")
	_, err = w.Write(b)
	if err != nil {
		logger.Error(err, " Failed to write for healthChecker")
	}
}

func getMetadata(filter string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "GET" {
			return
		}

		logger.Debug("Calling getMetadata ")
		userIP := getIPFromRequest(r)
		if userIP == "" {
			return
		}

		metrics.MetadataRequests.Inc()
		logger.With("userIP", userIP).Info("Actual IP is: ")
		hw, err := getByIP(context.Background(), hegelServer, userIP) // returns hardware data as []byte
		if err != nil {
			metrics.Errors.WithLabelValues("metadata", "lookup").Inc()
			logger.Info("Error in finding hardware: ", err)
			w.WriteHeader(http.StatusInternalServerError)
			return
		}

		var resp []byte
		dataModelVersion := os.Getenv("DATA_MODEL_VERSION")
		switch dataModelVersion {
		case "":
			resp, err = exportHardware(hw) // in cacher mode, the "filter" is the exportedHardwareCacher type
			if err != nil {
				logger.Info("Error in exporting hardware: ", err)
			}
		case "1":
			resp, err = filterMetadata(hw, filter)
			if err != nil {
				logger.Info("Error in filtering metadata: ", err)
			}
		default:
			logger.Fatal(errors.New("unknown DATA_MODEL_VERSION"))

		}
		w.WriteHeader(http.StatusOK)
		w.Header().Set("Content-Type", "application/json")
		_, err = w.Write(resp)
		if err != nil {
			logger.Error(err, "failed to write Metadata")
		}
	}
}

func ec2Handler(w http.ResponseWriter, r *http.Request) {
	if r.Method != "GET" {
		return
	}

	logger.Debug("Calling ec2Handler ")
	userIP := getIPFromRequest(r)
	if userIP != "" {
		metrics.MetadataRequests.Inc()
		logger.With("userIP", userIP).Info("Actual IP is: ")
		hw, err := getByIP(context.Background(), hegelServer, userIP) // returns hardware data as []byte
		if err != nil {
			metrics.Errors.WithLabelValues("metadata", "lookup").Inc()
			logger.Info("Error in finding hardware: ", err)
			w.WriteHeader(http.StatusInternalServerError)
			return
		}

		filters = map[string]interface{}{
			"user-data": ".metadata.userdata",
			"meta-data": map[string]interface{}{
				"instance-id": ".metadata.instance.id",
				"hostname":    ".metadata.instance.hostname",
				"iqn":         ".metadata.instance.iqn",
				"plan":        ".metadata.instance.plan",
				"facility":    ".metadata.instance.facility",
				"tags":        ".metadata.instance.tags",
				"operating-system": map[string]interface{}{
					"slug":    ".metadata.instance.operating_system.slug",
					"distro":  ".metadata.instance.operating_system.distro",
					"version": ".metadata.instance.operating_system.version",
					"license_activation": map[string]interface{}{
						"state": ".metadata.instance.operating_system.license_activation.state",
					},
					"image_tag": ".metadata.instance.operating_system.image_tag",
				},
				"public-keys": ".metadata.instance.ssh_keys",
				"spot":        ".metadata.instance.spot.termination_time",
				"public-ipv4": `.metadata.instance.network.addresses.[] | select(.address_family == 4 and .public == true) | .address`,
				"public-ipv6": `.metadata.instance.network.addresses.[] | select(.address_family == 6 and .public == true) | .address`,
				"local-ipv4":  `.metadata.instance.network.addresses.[] | select(.address_family == 4 and .public == false) | .address`,
			},
		}

		query := strings.TrimRight(strings.TrimLeft(r.URL.Path, "/2009-04-04/"), "/") // remove base pattern and any trailing slashes
		accessors := strings.Split(query, "/")

		var res interface{} = filters
		for _, accessor := range accessors {
			item := res.(map[string]interface{})[accessor] // either a filter or another (sub) map of filters

			if filter, ok := item.(string); ok { // if is an actual filter
				res = filter
			} else if subfilters, ok := item.(map[string]interface{}); ok { // if is another map of filters
				res = subfilters
			} else {
				logger.Error(errors.New("invalid metadata item"))
				w.WriteHeader(http.StatusInternalServerError)
				w.Write([]byte(`{"status":500,"message":"Invalid metadata item"}`))
				return
			}
		}

		var resp []byte
		if filter, ok := res.(string); ok {
			resp, err = filterMetadata(hw, filter)
		} else if submenu, ok := res.(map[string]interface{}); ok {
			for item, _ := range submenu {
				if item == "spot" { /////// don't list if instance isn't a spot instance

				}
				resp = []byte(fmt.Sprintln(string(resp) + item))
			}
		}

		w.WriteHeader(http.StatusOK)
		w.Header().Set("Content-Type", "application/json")
		_, err = w.Write(resp)
		if err != nil {
			logger.Error(err, "failed to write Metadata")
		}
	}
}

func getIPFromRequest(r *http.Request) string {
	IPAddress := r.RemoteAddr
	if strings.ContainsRune(IPAddress, ':') {
		IPAddress, _, _ = net.SplitHostPort(IPAddress)
	}
	return IPAddress
}

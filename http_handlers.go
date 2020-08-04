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
	"sort"
	"strings"
	"time"

	"github.com/packethost/hegel/metrics"
)

var (
	ec2Filters = map[string]interface{}{
		"user-data": ".metadata.userdata",
		"meta-data": map[string]interface{}{
			"_base":       ".metadata.instance",
			"instance-id": ".id",
			"hostname":    ".hostname",
			"iqn":         ".iqn",
			"plan":        ".plan",
			"facility":    ".facility",
			"tags":        ".tags[]?",
			"operating-system": map[string]interface{}{
				"_base":   ".operating_system",
				"slug":    ".slug",
				"distro":  ".distro",
				"version": ".version",
				"license_activation": map[string]interface{}{
					"_base": ".license_activation",
					"state": ".state",
				},
				"image_tag": ".image_tag",
			},
			"public-keys": ".ssh_keys[]?",
			"spot": map[string]interface{}{ // TODO (kdeng3849) need to check actual structure
				"_base":            ".spot",
				"termination-time": ".termination_time",
			},
			"public-ipv4": `.network.addresses[]? | select(.address_family == 4 and .public == true) | .address`,
			"public-ipv6": `.network.addresses[]? | select(.address_family == 6 and .public == true) | .address`,
			"local-ipv4":  `.network.addresses[]? | select(.address_family == 4 and .public == false) | .address`,
		},
	}
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
			w.WriteHeader(http.StatusMethodNotAllowed)
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
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}

	logger.Debug("Calling ec2Handler ")
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

	res, err := processEC2Query(r.URL.Path)
	if err != nil {
		logger.Error(err)
		w.WriteHeader(http.StatusNotFound)
		_, err := w.Write([]byte("404 not found"))
		if err != nil {
			logger.Error(err, "failed to write error response")
		}
	}

	var resp []byte
	if filter, ok := res.(string); ok {
		resp, err = filterMetadata(hw, filter)
		if err != nil {
			logger.Error(err, "Error in filtering metadata: ")
		}
	} else if submenu, ok := res.(map[string]interface{}); ok {
		var keys []string
		for item := range submenu {
			switch item {
			case "_base": // _base is only used to keep track of the base filter, not a metadata item
				continue
			case "spot": // list only if instance is spot
				spotFilter := fmt.Sprint(submenu["_base"], submenu[item].(map[string]interface{})["_base"])
				resp, err := filterMetadata(hw, spotFilter) // ".metadata.instance.spot"
				if err != nil {
					logger.Info("Error in filtering metadata: ", err)
				}
				if string(resp) != "" {
					keys = append(keys, item)
				}
			default:
				keys = append(keys, item)
			}
		}

		sort.Strings(keys)

		for _, item := range keys {
			resp = []byte(fmt.Sprintln(string(resp) + item))
		}
	} else {
		logger.Error(err, "unexpected result from processEC2Query: result should just either be a string or a map")
		w.WriteHeader(http.StatusInternalServerError)
		return
	}

	w.WriteHeader(http.StatusOK)
	w.Header().Set("Content-Type", "application/json")
	_, err = w.Write(resp)
	if err != nil {
		logger.Error(err, "failed to write Metadata")
	}
}

// processEC2Query returns either a specific filter (used to parse hardware data for the value of a specific field),
// or a map of filters (used for printing out its keys)
func processEC2Query(query string) (interface{}, error) {
	var result interface{} = ec2Filters

	q := strings.Trim(strings.TrimPrefix(query, "/2009-04-04"), "/") // remove base pattern and extra slashes
	if q == "" {                                                     // if query was just the base pattern
		return result, nil
	}
	accessors := strings.Split(q, "/")

	var base string
	for _, accessor := range accessors {
		if accessor == "_base" {
			return nil, errors.New("invalid metadata item")
		}

		var item interface{}
		if filters, ok := result.(map[string]interface{}); ok {
			item = filters[accessor] // either a filter or another (sub) map of filters
		}

		if filter, ok := item.(string); ok { // if is an actual filter
			result = fmt.Sprint(base, filter)
		} else if subfilters, ok := item.(map[string]interface{}); ok { // if is another map of filters
			base = fmt.Sprint(base, subfilters["_base"].(string))
			result = subfilters
		} else {
			return nil, errors.New("invalid metadata item")
		}
	}
	return result, nil
}

func getIPFromRequest(r *http.Request) string {
	IPAddress := r.RemoteAddr
	if strings.ContainsRune(IPAddress, ':') {
		IPAddress, _, _ = net.SplitHostPort(IPAddress)
	}
	return IPAddress
}

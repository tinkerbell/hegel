package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"reflect"
	"testing"

	"github.com/tinkerbell/tink/protos/packet"
)

func TestGetMetadataCacher(t *testing.T) {
	for name, test := range cacherTests {
		t.Log(name)
		hegelServer.hardwareClient = hardwareGetterMock{test.json}

		os.Setenv("DATA_MODEL_VERSION", "")
		os.Unsetenv("CUSTOM_ENDPOINTS")

		req, err := http.NewRequest("GET", "/metadata", nil)
		if err != nil {
			t.Fatal(err)
		}
		req.RemoteAddr = test.remote
		resp := httptest.NewRecorder()
		http.HandleFunc("/metadata", getMetadata("")) // filter not used in cacher mode

		http.DefaultServeMux.ServeHTTP(resp, req)

		if status := resp.Code; status != http.StatusOK {
			t.Errorf("handler returned wrong status code: got %v want %v",
				status, http.StatusOK)
		}

		hw := exportedHardwareCacher{}
		err = json.Unmarshal(resp.Body.Bytes(), &hw)
		if err != nil {
			t.Error("Error in unmarshalling hardware:", err)
		}

		if hw.ID != test.id {
			t.Errorf("handler returned unexpected id: got %v want %v",
				hw.ID, test.id)
		}
		if hw.PlanSlug != test.planSlug {
			t.Errorf("handler returned unexpected plan slug: got %v want %v",
				hw.PlanSlug, test.planSlug)
		}
	}
}

// TestGetMetadataTinkerbell tests the default use case in tinkerbell mode
func TestGetMetadataTinkerbell(t *testing.T) {
	os.Setenv("DATA_MODEL_VERSION", "1")
	os.Unsetenv("CUSTOM_ENDPOINTS")

	for name, test := range tinkerbellTests {
		t.Log(name)
		hegelServer.hardwareClient = hardwareGetterMock{test.json}

		http.DefaultServeMux = &http.ServeMux{} // reset registered patterns

		err := registerCustomEndpoints()
		if err != nil {
			t.Fatal("Error registering custom endpoints", err)
		}

		req, err := http.NewRequest("GET", "/metadata", nil)
		if err != nil {
			t.Fatal(err)
		}
		req.RemoteAddr = test.remote
		resp := httptest.NewRecorder()

		http.DefaultServeMux.ServeHTTP(resp, req)

		if status := resp.Code; status != http.StatusOK {
			t.Errorf("handler returned wrong status code: got %v want %v",
				status, http.StatusOK)
		}

		if resp.Body.Bytes() == nil {
			return
		}

		var metadata packet.Metadata
		err = json.Unmarshal(resp.Body.Bytes(), &metadata)
		if err != nil {
			t.Error("Error in unmarshalling hardware metadata:", err)
		}

		if metadata.BondingMode != test.bondingMode {
			t.Errorf("handler returned unexpected bonding mode: got %v want %v",
				metadata.BondingMode, test.bondingMode)
		}
	}
}

// TestGetMetadataTinkerbellKant tests the kant specific use case in tinkerbell mode
func TestGetMetadataTinkerbellKant(t *testing.T) {
	os.Setenv("DATA_MODEL_VERSION", "1")
	os.Setenv("CUSTOM_ENDPOINTS", `{"/metadata":".metadata.instance","/components":".metadata.components","/userdata":".metadata.userdata"}`)

	for name, test := range tinkerbellKantTests {
		t.Log(name)
		hegelServer.hardwareClient = hardwareGetterMock{test.json}

		http.DefaultServeMux = &http.ServeMux{} // reset registered patterns

		err := registerCustomEndpoints()
		if err != nil {
			t.Fatal("Error registering custom endpoints", err)
		}

		req, err := http.NewRequest("GET", test.url, nil)
		if err != nil {
			t.Fatal(err)
		}
		req.RemoteAddr = test.remote
		resp := httptest.NewRecorder()

		http.DefaultServeMux.ServeHTTP(resp, req)

		if status := resp.Code; status != http.StatusOK {
			t.Errorf("handler returned wrong status code: got %v want %v",
				status, http.StatusOK)
		}

		if resp.Body.String() != test.response {
			t.Errorf("handler returned with unexpected body: got %v want %v",
				resp.Body.String(), test.response)
		}
	}
}

func TestRegisterEndpoints(t *testing.T) {
	os.Setenv("DATA_MODEL_VERSION", "1")

	for name, test := range registerEndpointTests {
		t.Log(name)
		hegelServer.hardwareClient = hardwareGetterMock{test.json}

		os.Unsetenv("CUSTOM_ENDPOINTS")
		if test.customEndpoints != "" {
			os.Setenv("CUSTOM_ENDPOINTS", test.customEndpoints)
		}

		http.DefaultServeMux = &http.ServeMux{} // reset registered patterns

		err := registerCustomEndpoints()
		if err != nil && err.Error() != test.error {
			t.Fatalf("unexpected error: got %v want %v", err, test.error)
		}

		req, err := http.NewRequest("GET", test.url, nil)
		if err != nil {
			t.Fatal(err)
		}
		req.RemoteAddr = test.remote
		resp := httptest.NewRecorder()

		http.DefaultServeMux.ServeHTTP(resp, req)

		if status := resp.Code; status != test.status {
			t.Errorf("handler returned wrong status code: got %v want %v",
				status, test.status)
		}

		t.Log("response:", resp.Body.String()) // logging response instead of explicitly checking content
		if resp.Body.String() == "" && !test.expectResponseEmpty {
			t.Errorf("handler should have returned a non-empty response")
		}
	}
}

func TestEC2Endpoint(t *testing.T) {
	os.Setenv("DATA_MODEL_VERSION", "1")

	for name, test := range tinkerbellEC2Tests {
		t.Run(name, func(t *testing.T) {
			hegelServer.hardwareClient = hardwareGetterMock{test.json}

			http.DefaultServeMux = &http.ServeMux{} // reset registered patterns

			http.HandleFunc("/2009-04-04", ec2Handler) // workaround for making trailing slash optional
			http.HandleFunc("/2009-04-04/", ec2Handler)

			req, err := http.NewRequest("GET", test.url, nil)
			if err != nil {
				t.Fatal(err)
			}
			req.RemoteAddr = test.remote
			resp := httptest.NewRecorder()

			http.DefaultServeMux.ServeHTTP(resp, req)

			if status := resp.Code; status != test.status {
				t.Errorf("handler returned wrong status code: got %v want %v",
					status, test.status)
			}

			if resp.Body.String() != test.response {
				t.Errorf("handler returned wrong body: got %v want %v", resp.Body.String(), test.response)
			}
		})
	}
}

func TestProcessEC2Query(t *testing.T) {
	os.Setenv("DATA_MODEL_VERSION", "1")

	for name, test := range processEC2QueryTests {
		t.Run(name, func(t *testing.T) {

			res, err := processEC2Query(test.query)
			if err != nil && err.Error() != test.error {
				t.Errorf("handler returned wrong error: got %v want %v", err, test.error)
			}

			if !reflect.DeepEqual(res, test.result) {
				t.Errorf("handler returned wrong result: got %v want %v", res, test.result)
			}
		})
	}
}

// test cases for TestGetMetadataCacher
var cacherTests = map[string]struct {
	id       string
	remote   string
	planSlug string
	json     string
}{
	"cacher": {
		id:       "8978e7d4-1a55-4845-8a66-a5259236b104",
		remote:   "192.168.1.5",
		planSlug: "t1.small.x86",
		json:     cacherDataModel,
	},
}

// test cases for TestGetMetadataTinkerbell
var tinkerbellTests = map[string]struct {
	id          string
	remote      string
	bondingMode int64
	json        string
}{
	"tinkerbell": {
		id:          "fde7c87c-d154-447e-9fce-7eb7bdec90c0",
		remote:      "192.168.1.5",
		bondingMode: 5,
		json:        tinkerbellDataModel,
	},
	"tinkerbell no metadata": {
		id:     "363115b0-f03d-4ce5-9a15-5514193d131a",
		remote: "192.168.1.5",
		json:   tinkerbellNoMetadata,
	},
}

// TestGetMetadataTinkerbellKant
var tinkerbellKantTests = map[string]struct {
	url      string
	remote   string
	status   int
	response string
	json     string
}{
	"metadata endpoint": {
		url:      "/metadata",
		remote:   "192.168.1.5",
		status:   200,
		response: `{"facility":"sjc1","hostname":"tink-provisioner","id":"f955e31a-cab6-44d6-872c-9614c2024bb4"}`,
		json:     tinkerbellKant,
	},
	"components endpoint": {
		url:      "/components",
		remote:   "192.168.1.5",
		status:   200,
		response: `{"id":"bc9ce39b-7f18-425b-bc7b-067914fa9786","type":"DiskComponent"}`,
		json:     tinkerbellKant,
	},
	"userdata endpoint": {
		url:    "/userdata",
		remote: "192.168.1.5",
		status: 200,
		response: `#!/bin/bash

echo "Hello world!"`,
		json: tinkerbellKant,
	},
	"no metadata": {
		url:      "/metadata",
		remote:   "192.168.1.5",
		status:   200,
		response: "",
		json:     tinkerbellNoMetadata,
	},
}

// test cases for TestRegisterEndpoints
var registerEndpointTests = map[string]struct {
	customEndpoints     string
	url                 string
	remote              string
	status              int
	expectResponseEmpty bool
	error               string
	json                string
}{
	"single custom endpoint": {
		customEndpoints: `{"/facility": ".metadata.facility"}`,
		url:             "/facility",
		remote:          "192.168.1.5",
		status:          200,
		json:            tinkerbellDataModel,
	},
	"single custom endpoint, non-metadata": {
		customEndpoints: `{"/id": ".id"}`,
		url:             "/id",
		remote:          "192.168.1.5",
		status:          200,
		json:            tinkerbellDataModel,
	},
	"single custom endpoint, invalid url call": {
		customEndpoints: `{"/userdata": ".metadata.userdata"}`,
		url:             "/metadata",
		remote:          "192.168.1.5",
		status:          404,
		json:            tinkerbellDataModel,
	},
	"multiple custom endpoints": {
		customEndpoints: `{"/metadata":".metadata.instance","/components":".metadata.components","/all":".","/userdata":".metadata.userdata"}`,
		url:             "/components",
		remote:          "192.168.1.5",
		status:          200,
		json:            tinkerbellDataModel,
	},
	"default endpoint": {
		url:    "/metadata",
		remote: "192.168.1.5",
		status: 200,
		json:   tinkerbellDataModel,
	},
	"custom endpoints invalid format (not a map)": {
		customEndpoints: `"/userdata":".metadata.userdata"`,
		url:             "/userdata",
		remote:          "192.168.1.5",
		status:          404,
		error:           "invalid character ':' after top-level value",
		json:            tinkerbellDataModel,
	},
	"custom endpoints invalid format (endpoint missing forward slash)": {
		customEndpoints: `{"userdata":".metadata.userdata"}`,
		url:             "/userdata",
		remote:          "192.168.1.5",
		status:          404,
		json:            tinkerbellDataModel,
	},
	"custom endpoints invalid format (invalid jq filter syntax)": {
		customEndpoints:     `{"/userdata":"invalid"}`,
		url:                 "/userdata",
		remote:              "192.168.1.5",
		status:              200,
		expectResponseEmpty: true,
		json:                tinkerbellDataModel,
	},
	"custom endpoints invalid format (valid jq filter syntax, nonexistent field)": {
		customEndpoints:     `{"/userdata":".nonexistent"}`,
		url:                 "/userdata",
		remote:              "192.168.1.5",
		status:              200,
		expectResponseEmpty: true,
		json:                tinkerbellDataModel,
	},
}

// test cases for TestEC2Endpoint
var tinkerbellEC2Tests = map[string]struct {
	url      string
	remote   string
	status   int
	response string
	json     string
}{
	"user-data": {
		url:    "/2009-04-04/user-data",
		remote: "192.168.1.5",
		status: 200,
		response: `#!/bin/bash

echo "Hello world!"`,
		json: tinkerbellKantEC2,
	},
	"meta-data": {
		url:    "/2009-04-04/meta-data",
		remote: "192.168.1.5",
		status: 200,
		response: `facility
hostname
instance-id
iqn
local-ipv4
operating-system
plan
public-ipv4
public-ipv6
public-keys
tags
`,
		json: tinkerbellKantEC2,
	},
	"instance-id": {
		url:      "/2009-04-04/meta-data/instance-id",
		remote:   "192.168.1.5",
		status:   200,
		response: "7c9a5711-aadd-4fa0-8e57-789431626a27",
		json:     tinkerbellKantEC2,
	},
	"public-ipv4": {
		url:      "/2009-04-04/meta-data/public-ipv4",
		remote:   "192.168.1.5",
		status:   200,
		response: "139.175.86.114",
		json:     tinkerbellKantEC2,
	},
	"tags": {
		url:    "/2009-04-04/meta-data/tags",
		remote: "192.168.1.5",
		status: 200,
		response: `hello
test`,
		json: tinkerbellKantEC2,
	},
	"operating-system slug": {
		url:      "/2009-04-04/meta-data/operating-system/slug",
		remote:   "192.168.1.5",
		status:   200,
		response: "ubuntu_18_04",
		json:     tinkerbellKantEC2,
	},
	"invalid metadata item": {
		url:      "/2009-04-04/meta-data/invalid",
		remote:   "192.168.1.5",
		status:   404,
		response: "404 not found",
		json:     tinkerbellKantEC2,
	},
	"valid metadata item, but not found": {
		url:      "/2009-04-04/meta-data/public-keys",
		remote:   "192.168.1.5",
		status:   200,
		response: "",
		json:     tinkerbellNoMetadata,
	},
	"with trailing slash": {
		url:      "/2009-04-04/meta-data/hostname/",
		remote:   "192.168.1.5",
		status:   200,
		response: "tink-provisioner",
		json:     tinkerbellKantEC2,
	},
	"base endpoint": {
		url:    "/2009-04-04",
		remote: "192.168.1.5",
		status: 200,
		response: `meta-data
user-data
`,
		json: tinkerbellKantEC2,
	},
	"base endpoint with trailing slash": {
		url:    "/2009-04-04/",
		remote: "192.168.1.5",
		status: 200,
		response: `meta-data
user-data
`,
		json: tinkerbellKantEC2,
	},
	"spot instance with empty spot field": {
		url:    "/2009-04-04/meta-data",
		remote: "192.168.1.5",
		status: 200,
		response: `facility
hostname
instance-id
iqn
local-ipv4
operating-system
plan
public-ipv4
public-ipv6
public-keys
spot
tags
`,
		json: tinkerbellKantEC2SpotEmpty,
	},
	"termination-time": {
		url:      "/2009-04-04/meta-data/spot/termination-time",
		remote:   "192.168.1.5",
		status:   200,
		response: "now",
		json:     tinkerbellKantEC2SpotWithTermination,
	},
}

// test cases for TestProcessEC2Query
var processEC2QueryTests = map[string]struct {
	query  string
	error  string
	result interface{}
}{
	"filter result (simple)": {
		query:  "/2009-04-04/user-data",
		result: ".metadata.userdata",
	},
	"filter result (multiple base filters)": {
		query:  "/2009-04-04/meta-data/operating-system/license_activation/state",
		result: ".metadata.instance.operating_system.license_activation.state",
	},
	"map result": {
		query: "/2009-04-04/meta-data/operating-system/license_activation",
		result: map[string]interface{}{
			"_base": ".license_activation",
			"state": ".state",
		},
	},
	"map result (base endpoint)": {
		query:  "/2009-04-04/",
		result: ec2Filters,
	},
	"invalid query ('_base' as metadata item)": {
		query: "/2009-04-04/_base",
		error: "invalid metadata item",
	},
	"invalid query (invalid metadata item)": {
		query: "/2009-04-04/invalid",
		error: "invalid metadata item",
	},
	"invalid query (no subfilters)": {
		query: "/2009-04-04/user-data/hostname",
		error: "invalid metadata item",
	},
}

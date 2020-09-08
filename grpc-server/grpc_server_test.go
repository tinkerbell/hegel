package grpcserver

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"reflect"
	"testing"

	"github.com/packethost/hegel/hardware-getter/mock"
	"github.com/tinkerbell/tink/protos/packet"
)

func TestGetByIPCacher(t *testing.T) {
	for name, test := range cacherGrpcTests {
		t.Log(name)

		dataModelVersion := os.Getenv("DATA_MODEL_VERSION")
		defer os.Setenv("DATA_MODEL_VERSION", dataModelVersion)
		os.Unsetenv("DATA_MODEL_VERSION")

		hegelTestServer := &Server{
			Log:            logger,
			HardwareClient: mock.HardwareGetterMock{test.json},
		}
		ehw, err := GetByIP(context.Background(), hegelTestServer, mock.UserIP) // returns hardware data as []byte
		if err != nil {
			t.Fatal("unexpected error while getting hardware by ip:", err)
		}
		hw := exportedHardwareCacher{}
		err = json.Unmarshal(ehw, &hw)
		if test.error != "" {
			if err == nil {
				t.Fatalf("unmarshal should have returned error: %v", test.error)
			} else if err.Error() != test.error {
				t.Fatalf("unmarshal returned wrong error, want: %v, got: %v\n", err, test.error)
			}
		}

		if hw.State != test.state {
			t.Fatalf("unexpected state, want: %v, got: %v\n", test.state, hw.State)
		}
		if hw.Facility != test.facility {
			t.Fatalf("unexpected facility, want: %v, got: %v\n", test.facility, hw.Facility)
		}
		if len(hw.NetworkPorts) > 0 && hw.NetworkPorts[0]["data"].(map[string]interface{})["mac"] != test.mac {
			t.Fatalf("unexpected mac, want: %v, got: %v\n", test.mac, hw.NetworkPorts[0]["data"].(map[string]interface{})["mac"])
		}
		if len(hw.Instance.Storage.Disks) > 0 {
			if hw.Instance.Storage.Disks[0].Device != test.diskDevice {
				t.Fatalf("unexpected storage disk device, want: %v, got: %v\n", test.diskDevice, hw.Instance.Storage.Disks[0].Device)
			}
			if hw.Instance.Storage.Disks[0].WipeTable != test.wipeTable {
				t.Fatalf("unexpected storage disk wipe table, want: %v, got: %v\n", test.wipeTable, hw.Instance.Storage.Disks[0].WipeTable)
			}
			t.Log("want:", test.partitionSize, " got:", hw.Instance.Storage.Disks[0].Paritions[0].Size)
			if fmt.Sprintf("%v", hw.Instance.Storage.Disks[0].Paritions[0].Size) != fmt.Sprintf("%v", test.partitionSize) {
				t.Fatalf("unexpected storage disk partition size, want: %v, got: %v\n", test.partitionSize, hw.Instance.Storage.Disks[0].Paritions[0].Size)
			}
		}
		if len(hw.Instance.Storage.Filesystems) > 0 {
			if hw.Instance.Storage.Filesystems[0].Mount.Device != test.filesystemDevice {
				t.Fatalf("unexpected storage filesystem mount device, want: %v, got: %v\n", test.filesystemDevice, hw.Instance.Storage.Filesystems[0].Mount.Device)
			}
			if hw.Instance.Storage.Filesystems[0].Mount.Format != test.filesystemFormat {
				t.Fatalf("unexpected storage filesystem mount format, want: %v, got: %v\n", test.filesystemFormat, hw.Instance.Storage.Filesystems[0].Mount.Format)
			}
		}
		if test.osSlug != "" && hw.Instance.OS.OsSlug != test.osSlug {
			t.Fatalf("unexpected os slug, want: %v, got: %v\n", test.osSlug, hw.Instance.OS.OsSlug)
		}
		if hw.PlanSlug != test.planSlug {
			t.Fatalf("unexpected plan slug, want: %v, got: %v\n", test.planSlug, hw.PlanSlug)
		}
	}
}

func TestGetByIPTinkerbell(t *testing.T) {
	dataModelVersion := os.Getenv("DATA_MODEL_VERSION")
	defer os.Setenv("DATA_MODEL_VERSION", dataModelVersion)
	os.Setenv("DATA_MODEL_VERSION", "1")

	for name, test := range tinkerbellGrpcTests {
		t.Log(name)

		hegelTestServer := &Server{
			Log:            logger,
			HardwareClient: mock.HardwareGetterMock{test.json},
		}
		ehw, err := GetByIP(context.Background(), hegelTestServer, mock.UserIP) // returns hardware data as []byte
		if test.error != "" {
			if err == nil {
				t.Fatalf("GetByIP should have returned error: %v", test.error)
			} else if err.Error() != test.error {
				t.Fatalf("GetByIP returned wrong error: got %v want %v", err, test.error)
			}
		}

		hw := struct {
			ID       string          `json:"id"`
			Metadata packet.Metadata `json:"metadata"`
		}{}
		err = json.Unmarshal(ehw, &hw)
		if err != nil {
			t.Error("error in unmarshalling hardware metadata", err)
		}

		if hw.ID != test.id {
			t.Errorf("handler returned unexpected id: got %v want %v",
				hw.ID, test.id)
		}

		if reflect.DeepEqual(hw.Metadata, packet.Metadata{}) { // return if metadata is empty
			return
		}

		if hw.Metadata.State != test.state {
			t.Fatalf("unexpected state, want: %v, got: %v\n", test.state, hw.Metadata.State)
		}
		if hw.Metadata.BondingMode != test.bondingMode {
			t.Fatalf("unexpected bonding mode, want: %v, got: %v\n", test.bondingMode, hw.Metadata.BondingMode)
		}
		if len(hw.Metadata.Instance.Storage.Disks) > 0 {
			if hw.Metadata.Instance.Storage.Disks[0].Device != test.diskDevice {
				t.Fatalf("unexpected disk device, want: %v, got: %v\n", test.diskDevice, hw.Metadata.Instance.Storage.Disks[0].Device)
			}
			if hw.Metadata.Instance.Storage.Disks[0].WipeTable != test.wipeTable {
				t.Fatalf("unexpected wipe table, want: %v, got: %v\n", test.wipeTable, hw.Metadata.Instance.Storage.Disks[0].WipeTable)
			}
			if fmt.Sprintf("%v", hw.Metadata.Instance.Storage.Disks[0].Partitions[0].Size) != fmt.Sprintf("%v", test.partitionSize) {
				t.Fatalf("unexpected partition size, want: %v, got: %v\n", test.partitionSize, hw.Metadata.Instance.Storage.Disks[0].Partitions[0].Size)
			}
		}
		if len(hw.Metadata.Instance.Storage.Filesystems) > 0 {
			if hw.Metadata.Instance.Storage.Filesystems[0].Mount.Device != test.filesystemDevice {
				t.Fatalf("unexpected filesystem mount device, want: %v, got: %v\n", test.filesystemDevice, hw.Metadata.Instance.Storage.Filesystems[0].Mount.Device)
			}
			if hw.Metadata.Instance.Storage.Filesystems[0].Mount.Format != test.filesystemFormat {
				t.Fatalf("unexpected filesystem mount format, want: %v, got: %v\n", test.filesystemFormat, hw.Metadata.Instance.Storage.Filesystems[0].Mount.Format)
			}
		}
		if hw.Metadata.Instance.OperatingSystemVersion.OsSlug != test.osSlug {
			t.Fatalf("unexpected os slug, want: %v, got: %v\n", test.osSlug, hw.Metadata.Instance.OperatingSystemVersion.OsSlug)
		}
		if hw.Metadata.Facility.PlanSlug != test.planSlug {
			t.Fatalf("unexpected os slug, want: %v, got: %v\n", test.planSlug, hw.Metadata.Facility.PlanSlug)
		}
	}
}

// test cases for TestGetByIPCacher
var cacherGrpcTests = map[string]struct {
	id               string
	state            string
	facility         string
	mac              string
	diskDevice       string
	wipeTable        bool
	partitionSize    interface{}
	filesystemDevice string
	filesystemFormat string
	osSlug           string
	planSlug         string
	json             string
	error            string
}{
	"cacher": {
		state:            "provisioning",
		facility:         "onprem",
		mac:              "98:03:9b:48:de:bc",
		diskDevice:       "/dev/sda",
		wipeTable:        true,
		partitionSize:    4096,
		filesystemDevice: "/dev/sda3",
		filesystemFormat: "ext4",
		osSlug:           "ubuntu_16_04",
		planSlug:         "t1.small.x86",
		json:             mock.CacherDataModel,
	},
	"cacher_partition_size_int": { // 4096
		partitionSize: 4096,
		json:          mock.CacherPartitionSizeInt,
	},
	"cacher_partition_size_string": { // "3333"
		partitionSize: 3333,
		json:          mock.CacherPartitionSizeString,
	},
	"cacher_partition_size_b_lower": { // "1000000b"
		partitionSize: "1000000b",
		json:          mock.CacherPartitionSizeBLower,
	},
}

// test cases for TestGetByIPTinkerbell
var tinkerbellGrpcTests = map[string]struct {
	id               string
	state            string
	bondingMode      int64
	diskDevice       string
	wipeTable        bool
	partitionSize    interface{}
	filesystemDevice string
	filesystemFormat string
	osSlug           string
	planSlug         string
	json             string
	error            string
}{
	"tinkerbell": {
		id:               "fde7c87c-d154-447e-9fce-7eb7bdec90c0",
		bondingMode:      5,
		diskDevice:       "/dev/sda",
		wipeTable:        true,
		partitionSize:    4096,
		filesystemDevice: "/dev/sda3",
		filesystemFormat: "ext4",
		osSlug:           "ubuntu_18_04",
		planSlug:         "c2.medium.x86",
		json:             mock.TinkerbellDataModel,
	},
	"tinkerbell no metadata": {
		id:   "363115b0-f03d-4ce5-9a15-5514193d131a",
		json: mock.TinkerbellNoMetadata,
	},
}

package grpcserver

import (
	"bytes"
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"net"
	"os"
	"sync"
	"time"

	grpc_middleware "github.com/grpc-ecosystem/go-grpc-middleware"
	grpc_prometheus "github.com/grpc-ecosystem/go-grpc-prometheus"
	"github.com/itchyny/gojq"
	cacherClient "github.com/packethost/cacher/client"
	"github.com/packethost/cacher/protos/cacher"
	"github.com/packethost/hegel/grpc/hegel"
	"github.com/packethost/hegel/metrics"
	"github.com/packethost/hegel/xff"
	"github.com/packethost/pkg/env"
	"github.com/packethost/pkg/log"
	"github.com/pkg/errors"
	"github.com/prometheus/client_golang/prometheus"
	tinkClient "github.com/tinkerbell/tink/client"
	tink "github.com/tinkerbell/tink/protos/hardware"
	"github.com/tinkerbell/tink/util"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/peer"
	"google.golang.org/grpc/status"
)

//go:generate protoc -I grpc/protos grpc/protos/hegel.proto --go_out=plugins=grpc:grpc/hegel

var (
	facility = flag.String("facility", env.Get("HEGEL_FACILITY", "onprem"),
		"The facility we are running in (mostly to connect to cacher)")
	tlsCertPath = flag.String("tls_cert", env.Get("HEGEL_TLS_CERT"),
		"Path of tls certificat to use.")
	tlsKeyPath = flag.String("tls_key", env.Get("HEGEL_TLS_KEY"),
		"Path of tls key to use.")
	useTLS = flag.Bool("use_tls", env.Bool("HEGEL_USE_TLS", true),
		"Whether we should use tls or not (should be disabled for traefik)")
	StartTime           = time.Now()
	HegelServer         *Server
	isCacherAvailableMu sync.RWMutex
	isCacherAvailable   bool
)

type Server struct {
	Log            log.Logger
	HardwareClient HardwareGetter

	SubLock       sync.RWMutex
	Subscriptions map[string]*subscription
}

type HardwareGetter interface {
	ByIP(ctx context.Context, in getRequest, opts ...grpc.CallOption) (hardware, error)
	Watch(ctx context.Context, in getRequest, opts ...grpc.CallOption) (watchClient, error)
}

type getRequest interface{}
type hardware interface{}
type watchClient interface{}

type subscription struct {
	ID           string        `json:"id"`
	IP           string        `json:"ip"`
	InitDuration time.Duration `json:"init_duration"`
	StartedAt    time.Time     `json:"started_at"`
	cancel       func()
	updateChan   chan []byte
}

type hardwareGetterCacher struct {
	client cacher.CacherClient
}

type hardwareGetterTinkerbell struct {
	client tink.HardwareServiceClient
}

func (hg hardwareGetterCacher) ByIP(ctx context.Context, in getRequest, opts ...grpc.CallOption) (hardware, error) {
	hw, err := hg.client.ByIP(ctx, in.(*cacher.GetRequest), opts...)
	if err != nil {
		return nil, err
	}
	return hw, nil
}

func (hg hardwareGetterCacher) Watch(ctx context.Context, in getRequest, opts ...grpc.CallOption) (watchClient, error) {
	w, err := hg.client.Watch(ctx, in.(*cacher.GetRequest), opts...)
	if err != nil {
		return nil, err
	}
	return w, nil
}

func (hg hardwareGetterTinkerbell) ByIP(ctx context.Context, in getRequest, opts ...grpc.CallOption) (hardware, error) {
	hw, err := hg.client.ByIP(ctx, in.(*tink.GetRequest), opts...)
	if err != nil {
		return nil, err
	}
	return hw, nil
}

func (hg hardwareGetterTinkerbell) Watch(ctx context.Context, in getRequest, opts ...grpc.CallOption) (watchClient, error) {
	w, err := hg.client.Watch(ctx, in.(*tink.GetRequest), opts...)
	if err != nil {
		return nil, err
	}
	return w, nil
}

// exportedHardwareCacher is the structure in which hegel returns to clients using the old cacher data model
// exposes only certain fields of the hardware data returned by cacher
type exportedHardwareCacher struct {
	ID                                 string                   `json:"id"`
	Arch                               string                   `json:"arch"`
	State                              string                   `json:"state"`
	EFIBoot                            bool                     `json:"efi_boot"`
	Instance                           instance                 `json:"instance,omitempty"`
	PreinstalledOperatingSystemVersion interface{}              `json:"preinstalled_operating_system_version"`
	NetworkPorts                       []map[string]interface{} `json:"network_ports"`
	PlanSlug                           string                   `json:"plan_slug"`
	Facility                           string                   `json:"facility_code"`
	Hostname                           string                   `json:"hostname"`
	BondingMode                        int                      `json:"bonding_mode"`
}

type instance struct {
	ID       string `json:"id,omitempty"`
	State    string `json:"state,omitempty"`
	Hostname string `json:"hostname,omitempty"`
	AllowPXE bool   `json:"allow_pxe,omitempty"`
	Rescue   bool   `json:"rescue,omitempty"`

	IPAddresses []map[string]interface{} `json:"ip_addresses,omitempty"`
	OS          *operatingSystem         `json:"operating_system_version,omitempty"`
	UserData    string                   `json:"userdata,omitempty"`

	CryptedRootPassword string `json:"crypted_root_password,omitempty"`

	Storage      *storage `json:"storage,omitempty"`
	SSHKeys      []string `json:"ssh_keys,omitempty"`
	NetworkReady bool     `json:"network_ready,omitempty"`
}

type operatingSystem struct {
	Slug     string `json:"slug"`
	Distro   string `json:"distro"`
	Version  string `json:"version"`
	ImageTag string `json:"image_tag"`
	OsSlug   string `json:"os_slug"`
}

type disk struct {
	Device    string       `json:"device"`
	WipeTable bool         `json:"wipeTable,omitempty"`
	Paritions []*partition `json:"partitions,omitempty"`
}

type file struct {
	Path     string `json:"path"`
	Contents string `json:"contents,omitempty"`
	Mode     int    `json:"mode,omitempty"`
	UID      int    `json:"uid,omitempty"`
	GID      int    `json:"gid,omitempty"`
}

type filesystem struct {
	Mount struct {
		Device string             `json:"device"`
		Format string             `json:"format"`
		Files  []*file            `json:"files,omitempty"`
		Create *filesystemOptions `json:"create,omitempty"`
		Point  string             `json:"point"`
	} `json:"mount"`
}

type filesystemOptions struct {
	Force   bool     `json:"force,omitempty"`
	Options []string `json:"options,omitempty"`
}

type partition struct {
	Label    string      `json:"label"`
	Number   int         `json:"number"`
	Size     interface{} `json:"size"`
	Start    int         `json:"start,omitempty"`
	TypeGUID string      `json:"typeGuid,omitempty"`
}

type raid struct {
	Name    string   `json:"name"`
	Level   string   `json:"level"`
	Devices []string `json:"devices"`
	Spares  int      `json:"spares,omitempty"`
}

type storage struct {
	Disks       []*disk       `json:"disks,omitempty"`
	RAID        []*raid       `json:"raid,omitempty"`
	Filesystems []*filesystem `json:"filesystems,omitempty"`
}

func Serve(ctx context.Context, l log.Logger) error {
	port := env.Int("GRPC_PORT", 42115)

	serverOpts := make([]grpc.ServerOption, 0)

	// setup tls credentials
	if *useTLS {
		creds, err := credentials.NewServerTLSFromFile(*tlsCertPath, *tlsKeyPath)
		if err != nil {
			l.Error(err, "failed to initialize server credentials")
			panic(err)
		}
		serverOpts = append(serverOpts, grpc.Creds(creds))
	}

	trustedProxies := xff.ParseTrustedProxies()
	xffStream, xffUnary := xff.GRPCMiddlewares(l, trustedProxies)
	streamLogger, unaryLogger := l.GRPCLoggers()
	serverOpts = append(serverOpts,
		grpc_middleware.WithUnaryServerChain(
			xffUnary,
			unaryLogger,
			grpc_prometheus.UnaryServerInterceptor,
		),
		grpc_middleware.WithStreamServerChain(
			xffStream,
			streamLogger,
			grpc_prometheus.StreamServerInterceptor,
		),
	)

	var hg HardwareGetter
	dataModelVersion := env.Get("DATA_MODEL_VERSION")
	switch dataModelVersion {
	case "1":
		tc, err := tinkClient.TinkHardwareClient()
		if err != nil {
			l.Fatal(err, "Failed to create the tink client")
		}
		hg = hardwareGetterTinkerbell{tc}
		// add health check for tink?
	default:
		cc, err := cacherClient.New(*facility)
		if err != nil {
			l.Fatal(err, "Failed to create the cacher client")
		}
		hg = hardwareGetterCacher{cc}
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

				isCacherAvailableMu.Lock()
				isCacherAvailable = isCacherAvailableTemp
				isCacherAvailableMu.Unlock()

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

	grpcServer := grpc.NewServer(serverOpts...)

	// Register grpc prometheus server
	grpc_prometheus.Register(grpcServer)

	HegelServer = &Server{
		Log:            l,
		HardwareClient: hg,
		Subscriptions:  make(map[string]*subscription),
	}

	hegel.RegisterHegelServer(grpcServer, HegelServer)

	lis, err := net.Listen("tcp", fmt.Sprintf(":%d", port))
	if err != nil {
		err = errors.Wrap(err, "failed to listen")
		l.Error(err)
		panic(err)
	}

	metrics.State.Set(metrics.Ready)
	//Serving GRPC
	l.Info("serving grpc")
	err = grpcServer.Serve(lis)
	if err != nil {
		l.Fatal(err, "Failed to serve  grpc")
	}

	return nil
}

// exportedHardware transforms hardware that is returned from cacher into what we want to expose to clients
func ExportHardware(hw []byte) ([]byte, error) {
	exported := &exportedHardwareCacher{}

	err := json.Unmarshal(hw, exported)
	if err != nil {
		return nil, err
	}
	return json.Marshal(exported)
}

func FilterMetadata(hw []byte, filter string) ([]byte, error) {
	var result bytes.Buffer
	query, err := gojq.Parse(filter)
	if err != nil {
		return nil, err
	}
	input := make(map[string]interface{})
	err = json.Unmarshal(hw, &input)
	if err != nil {
		return nil, err
	}
	iter := query.Run(input)
	for {
		v, ok := iter.Next()
		if !ok {
			break
		}

		if v == nil {
			continue
		}

		switch vv := v.(type) {
		case error:
			return nil, errors.Wrap(vv, "error while filtering with gojq")
		case string:
			result.WriteString(vv)
		default:
			marshalled, err := json.Marshal(vv)
			if err != nil {
				return nil, errors.Wrap(err, "error marshalling jq result")
			}
			result.Write(marshalled)
		}
		result.WriteRune('\n')
	}

	return bytes.TrimSuffix(result.Bytes(), []byte("\n")), nil
}

// UnmarshalJSON implements the json.Unmarshaler interface for custom unmarshalling of exportedHardwareCacher
func (eh *exportedHardwareCacher) UnmarshalJSON(b []byte) error {
	type ehj exportedHardwareCacher
	var tmp ehj
	err := json.Unmarshal(b, &tmp)
	if err != nil {
		return err
	}
	networkPorts := []map[string]interface{}{}
	for _, port := range tmp.NetworkPorts {
		if port["type"] == "data" {
			networkPorts = append(networkPorts, port)
		}
	}
	tmp.NetworkPorts = networkPorts
	*eh = exportedHardwareCacher(tmp)
	return nil
}

func (s *Server) Get(ctx context.Context, in *hegel.GetRequest) (*hegel.GetResponse, error) {
	p, ok := peer.FromContext(ctx)
	if !ok {
		return nil, errors.New("could not get peer info from client")
	}
	s.Log.With("client", p.Addr, "op", "get").Info()
	userIP := p.Addr.(*net.TCPAddr).IP.String()

	hw, err := GetByIP(ctx, s, userIP)
	if err != nil {
		return nil, err
	}
	ehw, err := ExportHardware(hw)
	if err != nil {
		return nil, err
	}
	return &hegel.GetResponse{
		JSON: string(ehw),
	}, nil
}

func (s *Server) Subscribe(in *hegel.SubscribeRequest, stream hegel.Hegel_SubscribeServer) error {
	startedAt := time.Now().UTC()
	metrics.TotalSubscriptions.Inc()
	metrics.Subscriptions.WithLabelValues("initializing").Inc()
	timer := prometheus.NewTimer(metrics.InitDuration)

	logger := s.Log.With("op", "subscribe")

	initError := func(err error) error {
		logger.Error(err)
		metrics.Subscriptions.WithLabelValues("initializing").Dec()
		metrics.Errors.WithLabelValues("subscribe", "initializing").Inc()
		timer.ObserveDuration()
		return err
	}

	p, ok := peer.FromContext(stream.Context())
	if !ok {
		return initError(errors.New("could not get peer info from client"))
	}

	var id string
	ip := p.Addr.(*net.TCPAddr).IP.String()
	logger = logger.With("ip", ip, "client", p.Addr)

	logger.Info()

	var watch watchClient
	var ctx context.Context
	var cancel context.CancelFunc
	dataModelVersion := os.Getenv("DATA_MODEL_VERSION")
	switch dataModelVersion {
	case "1":
		hw, err := s.HardwareClient.ByIP(stream.Context(), &tink.GetRequest{
			Ip: ip,
		})

		if err != nil {
			return initError(err)
		}

		id = hw.(*tink.Hardware).Id

		ctx, cancel = context.WithCancel(stream.Context())
		watch, err = s.HardwareClient.Watch(ctx, &tink.GetRequest{
			Id: id,
		})

		if err != nil {
			cancel()
			return initError(err)
		}
	default:
		hw, err := s.HardwareClient.ByIP(stream.Context(), &cacher.GetRequest{
			IP: ip,
		})

		if err != nil {
			return initError(err)
		}

		hwJSON := make(map[string]interface{})
		err = json.Unmarshal([]byte(hw.(*cacher.Hardware).JSON), &hwJSON)
		if err != nil {
			return initError(err)
		}

		hwID := hwJSON["id"]
		id = hwID.(string)

		ctx, cancel = context.WithCancel(stream.Context())
		watch, err = s.HardwareClient.Watch(ctx, &cacher.GetRequest{
			ID: id,
		})

		if err != nil {
			cancel()
			return initError(err)
		}
	}

	sub := &subscription{
		ID:           id,
		IP:           ip,
		StartedAt:    startedAt,
		InitDuration: time.Since(startedAt),
		cancel:       cancel,
		updateChan:   make(chan []byte, 1),
	}

	s.SubLock.Lock()
	old := s.Subscriptions[id]
	s.Subscriptions[id] = sub
	s.SubLock.Unlock()

	// Disconnect previous client if a client is already connected for this hardware id
	if old != nil {
		old.cancel()
	}

	defer func() {
		s.SubLock.Lock()
		defer s.SubLock.Unlock()
		// Check if subscription for hardware id exists.
		// If the subscriptions exists, make sure it has not been replaced by a new connection.
		if cSub := s.Subscriptions[id]; cSub == sub {
			delete(s.Subscriptions, id)
		}
	}()

	timer.ObserveDuration()
	metrics.Subscriptions.WithLabelValues("initializing").Dec()
	metrics.Subscriptions.WithLabelValues("active").Inc()

	activeError := func(err error) error {
		if err == nil {
			return err
		}
		logger.Error(err)
		metrics.Subscriptions.WithLabelValues("active").Dec()
		metrics.Errors.WithLabelValues("subscribe", "active").Inc()
		return err
	}

	errs := make(chan error, 1)
	go func() {
		for {
			var hw []byte
			dataModelVersion := os.Getenv("DATA_MODEL_VERSION")
			switch dataModelVersion {
			case "1":
				wt := watch.(tink.HardwareService_WatchClient)
				resp, err := wt.Recv()
				if err != nil {
					if err == io.EOF {
						err = status.Error(codes.OK, "stream ended")
					}
					errs <- err
					close(sub.updateChan)
					return
				}
				hw, err = json.Marshal(util.HardwareWrapper{Hardware: resp})
				if err != nil {
					errs <- errors.New("could not marshal hardware")
					close(sub.updateChan)
					return
				}
			default:
				wc := watch.(cacher.Cacher_WatchClient)
				resp, err := wc.Recv()
				if err != nil {
					if err == io.EOF {
						err = status.Error(codes.OK, "stream ended")
					}
					errs <- err
					close(sub.updateChan)
					return
				}
				hw = []byte(resp.JSON)
			}

			ehw, err := ExportHardware(hw)
			if err != nil {
				errs <- err
				close(sub.updateChan)
				return
			}

			sub.updateChan <- ehw
		}
	}()
	go func() {
		l := logger.With("op", "send")
		for ehw := range sub.updateChan {
			l.Info()
			err := stream.Send(&hegel.SubscribeResponse{
				JSON: string(ehw),
			})

			if err != nil {
				errs <- err
				cancel()
				return
			}
		}

	}()

	var retErr error
	if err := <-errs; status.Code(err) != codes.OK && retErr == nil {
		retErr = err
	}
	return activeError(retErr)
}

func GetByIP(ctx context.Context, s *Server, userIP string) ([]byte, error) {

	var hw []byte
	dataModelVersion := os.Getenv("DATA_MODEL_VERSION")
	switch dataModelVersion {
	case "1":
		req := &tink.GetRequest{
			Ip: userIP,
		}
		resp, err := s.HardwareClient.ByIP(ctx, req)

		if err != nil {
			return nil, err
		}

		hw, err = json.Marshal(util.HardwareWrapper{Hardware: resp.(*tink.Hardware)})
		if err != nil {
			return nil, errors.New("could not marshal hardware")
		}

		if string(hw) == "{}" {
			return nil, errors.New("could not find hardware")
		}

	default:
		req := &cacher.GetRequest{
			IP: userIP,
		}
		resp, err := s.HardwareClient.ByIP(ctx, req)

		if err != nil {
			return nil, err
		}

		hw = []byte(resp.(*cacher.Hardware).JSON)
		if string(hw) == "" {
			return nil, errors.New("could not find hardware")
		}
	}

	return hw, nil
}

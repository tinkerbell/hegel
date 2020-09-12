package hardware

import (
	"context"
	"flag"
	cacherClient "github.com/packethost/cacher/client"
	"github.com/packethost/pkg/env"
	"github.com/pkg/errors"
	tinkClient "github.com/tinkerbell/tink/client"

	"github.com/packethost/cacher/protos/cacher"
	tink "github.com/tinkerbell/tink/protos/hardware"
	"google.golang.org/grpc"
)

var (
	dataModelVersion = env.Get("DATA_MODEL_VERSION")
	facility = flag.String("facility", env.Get("HEGEL_FACILITY", "onprem"), "The facility we are running in (mostly to connect to cacher)")
)

type Client interface {
	ByIP(ctx context.Context, in GetRequest, opts ...grpc.CallOption) (Hardware, error)
	Watch(ctx context.Context, in GetRequest, opts ...grpc.CallOption) (Watcher, error)
}

type GetRequest interface{}
type Hardware interface{}
type Watcher interface{}

type CacherClient struct {
	Client cacher.CacherClient
}

type TinkerbellClient struct {
	Client tink.HardwareServiceClient
}

func New() (Client, error) {
	var hg Client

	switch dataModelVersion {
	case "1":
		tc, err := tinkClient.TinkHardwareClient()
		if err != nil {
			return nil, errors.Wrap(err, "failed to create the tink client")
		}
		hg = TinkerbellClient{Client: tc}
		// add health check for tink?
	default:
		cc, err := cacherClient.New(*facility)
		if err != nil {
			return nil, errors.Wrap(err, "failed to create the cacher client")
		}
		hg = CacherClient{Client: cc}
	}

	return hg, nil
}

func (hg CacherClient) ByIP(ctx context.Context, in GetRequest, opts ...grpc.CallOption) (Hardware, error) {
	hw, err := hg.Client.ByIP(ctx, in.(*cacher.GetRequest), opts...)
	if err != nil {
		return nil, err
	}
	return hw, nil
}

func (hg CacherClient) Watch(ctx context.Context, in GetRequest, opts ...grpc.CallOption) (Watcher, error) {
	w, err := hg.Client.Watch(ctx, in.(*cacher.GetRequest), opts...)
	if err != nil {
		return nil, err
	}
	return w, nil
}

func (hg TinkerbellClient) ByIP(ctx context.Context, in GetRequest, opts ...grpc.CallOption) (Hardware, error) {
	hw, err := hg.Client.ByIP(ctx, in.(*tink.GetRequest), opts...)
	if err != nil {
		return nil, err
	}
	return hw, nil
}

func (hg TinkerbellClient) Watch(ctx context.Context, in GetRequest, opts ...grpc.CallOption) (Watcher, error) {
	w, err := hg.Client.Watch(ctx, in.(*tink.GetRequest), opts...)
	if err != nil {
		return nil, err
	}
	return w, nil
}

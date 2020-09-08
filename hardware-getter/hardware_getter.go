package hardwaregetter

import (
	"context"
	"github.com/packethost/cacher/protos/cacher"
	tink "github.com/tinkerbell/tink/protos/hardware"

	"google.golang.org/grpc"
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
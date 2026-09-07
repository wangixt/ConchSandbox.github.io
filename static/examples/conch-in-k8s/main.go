package main

import (
	"context"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"time"

	dp "k8s.io/kubelet/pkg/apis/deviceplugin/v1beta1"
	"google.golang.org/grpc"
)

const resourceName = "conch.io/kvm"

type server struct{ dp.UnimplementedDevicePluginServer }

func (s *server) GetDevicePluginOptions(context.Context, *dp.Empty) (*dp.DevicePluginOptions, error) {
	return &dp.DevicePluginOptions{}, nil
}

func (s *server) ListAndWatch(_ *dp.Empty, stream dp.DevicePlugin_ListAndWatchServer) error {
	if err := stream.Send(&dp.ListAndWatchResponse{Devices: []*dp.Device{{ID: "kvm0", Health: dp.Healthy}}}); err != nil { return err }
	<-stream.Context().Done()
	return nil
}

func (s *server) Allocate(_ context.Context, req *dp.AllocateRequest) (*dp.AllocateResponse, error) {
	resp := &dp.AllocateResponse{}
	for range req.ContainerRequests {
		paths := []string{"/dev/kvm", "/dev/vhost-vsock", "/dev/vsock", "/dev/net/tun", "/dev/loop-control", "/dev/loop0", "/dev/loop1", "/dev/loop2", "/dev/loop3", "/dev/loop4", "/dev/loop5", "/dev/loop6", "/dev/loop7"}
		devices := make([]*dp.DeviceSpec, 0, len(paths))
		for _, path := range paths { devices = append(devices, &dp.DeviceSpec{HostPath: path, ContainerPath: path, Permissions: "rwm"}) }
		resp.ContainerResponses = append(resp.ContainerResponses, &dp.ContainerAllocateResponse{Devices: devices})
	}
	return resp, nil
}

func main() {
	if err := os.MkdirAll(dp.DevicePluginPath, 0755); err != nil { panic(err) }
	socket := filepath.Join(dp.DevicePluginPath, "conch-kvm.sock")
	_ = os.Remove(socket)
	listener, err := net.Listen("unix", socket)
	if err != nil { panic(err) }
	grpcServer := grpc.NewServer()
	dp.RegisterDevicePluginServer(grpcServer, &server{})
	go grpcServer.Serve(listener)

	kubelet, err := grpc.Dial("unix://"+dp.KubeletSocket, grpc.WithInsecure(), grpc.WithContextDialer(func(ctx context.Context, _ string) (net.Conn, error) { return (&net.Dialer{}).DialContext(ctx, "unix", dp.KubeletSocket) }))
	if err != nil { panic(err) }
	defer kubelet.Close()
	client := dp.NewRegistrationClient(kubelet)
	for i := 0; i < 60; i++ {
		_, err = client.Register(context.Background(), &dp.RegisterRequest{Version: dp.Version, Endpoint: filepath.Base(socket), ResourceName: resourceName})
		if err == nil { select {} }
		fmt.Fprintf(os.Stderr, "device plugin registration failed: %v\n", err)
		time.Sleep(time.Second)
	}
	panic(fmt.Errorf("failed to register device plugin: %w", err))
}

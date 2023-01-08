// Code generated by protoc-gen-go-grpc. DO NOT EDIT.
// versions:
// - protoc-gen-go-grpc v1.2.0
// - protoc             v3.21.9
// source: kcp_grpc_control.proto

package grpc_control

import (
	context "context"
	grpc "google.golang.org/grpc"
	codes "google.golang.org/grpc/codes"
	status "google.golang.org/grpc/status"
)

// This is a compile-time assertion to ensure that this generated file
// is compatible with the grpc package it is being compiled against.
// Requires gRPC-Go v1.32.0 or later.
const _ = grpc.SupportPackageIsVersion7

// KCPSessionCtlClient is the client API for KCPSessionCtl service.
//
// For semantics around ctx use and closing/ending streaming RPCs, please refer to https://pkg.go.dev/google.golang.org/grpc/?tab=doc#ClientConn.NewStream.
type KCPSessionCtlClient interface {
	GetSessions(ctx context.Context, in *GetSessionsRequest, opts ...grpc.CallOption) (*GetSessionsReply, error)
	RegsiterNewSession(ctx context.Context, in *RegsiterNewSessionRequest, opts ...grpc.CallOption) (*RegsiterNewSessionReply, error)
}

type kCPSessionCtlClient struct {
	cc grpc.ClientConnInterface
}

func NewKCPSessionCtlClient(cc grpc.ClientConnInterface) KCPSessionCtlClient {
	return &kCPSessionCtlClient{cc}
}

func (c *kCPSessionCtlClient) GetSessions(ctx context.Context, in *GetSessionsRequest, opts ...grpc.CallOption) (*GetSessionsReply, error) {
	out := new(GetSessionsReply)
	err := c.cc.Invoke(ctx, "/kcp_ctl.KCPSessionCtl/GetSessions", in, out, opts...)
	if err != nil {
		return nil, err
	}
	return out, nil
}

func (c *kCPSessionCtlClient) RegsiterNewSession(ctx context.Context, in *RegsiterNewSessionRequest, opts ...grpc.CallOption) (*RegsiterNewSessionReply, error) {
	out := new(RegsiterNewSessionReply)
	err := c.cc.Invoke(ctx, "/kcp_ctl.KCPSessionCtl/RegsiterNewSession", in, out, opts...)
	if err != nil {
		return nil, err
	}
	return out, nil
}

// KCPSessionCtlServer is the server API for KCPSessionCtl service.
// All implementations must embed UnimplementedKCPSessionCtlServer
// for forward compatibility
type KCPSessionCtlServer interface {
	GetSessions(context.Context, *GetSessionsRequest) (*GetSessionsReply, error)
	RegsiterNewSession(context.Context, *RegsiterNewSessionRequest) (*RegsiterNewSessionReply, error)
	mustEmbedUnimplementedKCPSessionCtlServer()
}

// UnimplementedKCPSessionCtlServer must be embedded to have forward compatible implementations.
type UnimplementedKCPSessionCtlServer struct {
}

func (UnimplementedKCPSessionCtlServer) GetSessions(context.Context, *GetSessionsRequest) (*GetSessionsReply, error) {
	return nil, status.Errorf(codes.Unimplemented, "method GetSessions not implemented")
}
func (UnimplementedKCPSessionCtlServer) RegsiterNewSession(context.Context, *RegsiterNewSessionRequest) (*RegsiterNewSessionReply, error) {
	return nil, status.Errorf(codes.Unimplemented, "method RegsiterNewSession not implemented")
}
func (UnimplementedKCPSessionCtlServer) mustEmbedUnimplementedKCPSessionCtlServer() {}

// UnsafeKCPSessionCtlServer may be embedded to opt out of forward compatibility for this service.
// Use of this interface is not recommended, as added methods to KCPSessionCtlServer will
// result in compilation errors.
type UnsafeKCPSessionCtlServer interface {
	mustEmbedUnimplementedKCPSessionCtlServer()
}

func RegisterKCPSessionCtlServer(s grpc.ServiceRegistrar, srv KCPSessionCtlServer) {
	s.RegisterService(&KCPSessionCtl_ServiceDesc, srv)
}

func _KCPSessionCtl_GetSessions_Handler(srv interface{}, ctx context.Context, dec func(interface{}) error, interceptor grpc.UnaryServerInterceptor) (interface{}, error) {
	in := new(GetSessionsRequest)
	if err := dec(in); err != nil {
		return nil, err
	}
	if interceptor == nil {
		return srv.(KCPSessionCtlServer).GetSessions(ctx, in)
	}
	info := &grpc.UnaryServerInfo{
		Server:     srv,
		FullMethod: "/kcp_ctl.KCPSessionCtl/GetSessions",
	}
	handler := func(ctx context.Context, req interface{}) (interface{}, error) {
		return srv.(KCPSessionCtlServer).GetSessions(ctx, req.(*GetSessionsRequest))
	}
	return interceptor(ctx, in, info, handler)
}

func _KCPSessionCtl_RegsiterNewSession_Handler(srv interface{}, ctx context.Context, dec func(interface{}) error, interceptor grpc.UnaryServerInterceptor) (interface{}, error) {
	in := new(RegsiterNewSessionRequest)
	if err := dec(in); err != nil {
		return nil, err
	}
	if interceptor == nil {
		return srv.(KCPSessionCtlServer).RegsiterNewSession(ctx, in)
	}
	info := &grpc.UnaryServerInfo{
		Server:     srv,
		FullMethod: "/kcp_ctl.KCPSessionCtl/RegsiterNewSession",
	}
	handler := func(ctx context.Context, req interface{}) (interface{}, error) {
		return srv.(KCPSessionCtlServer).RegsiterNewSession(ctx, req.(*RegsiterNewSessionRequest))
	}
	return interceptor(ctx, in, info, handler)
}

// KCPSessionCtl_ServiceDesc is the grpc.ServiceDesc for KCPSessionCtl service.
// It's only intended for direct use with grpc.RegisterService,
// and not to be introspected or modified (even as a copy)
var KCPSessionCtl_ServiceDesc = grpc.ServiceDesc{
	ServiceName: "kcp_ctl.KCPSessionCtl",
	HandlerType: (*KCPSessionCtlServer)(nil),
	Methods: []grpc.MethodDesc{
		{
			MethodName: "GetSessions",
			Handler:    _KCPSessionCtl_GetSessions_Handler,
		},
		{
			MethodName: "RegsiterNewSession",
			Handler:    _KCPSessionCtl_RegsiterNewSession_Handler,
		},
	},
	Streams:  []grpc.StreamDesc{},
	Metadata: "kcp_grpc_control.proto",
}

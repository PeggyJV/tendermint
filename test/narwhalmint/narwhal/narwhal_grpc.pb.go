// Code generated by protoc-gen-go-grpc. DO NOT EDIT.
// versions:
// - protoc-gen-go-grpc v1.2.0
// - protoc             v3.19.4
// source: narwhal.proto

package narwhal

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

// ValidatorClient is the client API for Validator service.
//
// For semantics around ctx use and closing/ending streaming RPCs, please refer to https://pkg.go.dev/google.golang.org/grpc/?tab=doc#ClientConn.NewStream.
type ValidatorClient interface {
	// Returns collection contents for each requested collection.
	GetCollections(ctx context.Context, in *GetCollectionsRequest, opts ...grpc.CallOption) (*GetCollectionsResponse, error)
	// Expunges collections from the mempool.
	RemoveCollections(ctx context.Context, in *RemoveCollectionsRequest, opts ...grpc.CallOption) (*Empty, error)
	// Returns collections along a DAG walk with a well-defined starting point.
	ReadCausal(ctx context.Context, in *ReadCausalRequest, opts ...grpc.CallOption) (*ReadCausalResponse, error)
}

type validatorClient struct {
	cc grpc.ClientConnInterface
}

func NewValidatorClient(cc grpc.ClientConnInterface) ValidatorClient {
	return &validatorClient{cc}
}

func (c *validatorClient) GetCollections(ctx context.Context, in *GetCollectionsRequest, opts ...grpc.CallOption) (*GetCollectionsResponse, error) {
	out := new(GetCollectionsResponse)
	err := c.cc.Invoke(ctx, "/narwhal.Validator/GetCollections", in, out, opts...)
	if err != nil {
		return nil, err
	}
	return out, nil
}

func (c *validatorClient) RemoveCollections(ctx context.Context, in *RemoveCollectionsRequest, opts ...grpc.CallOption) (*Empty, error) {
	out := new(Empty)
	err := c.cc.Invoke(ctx, "/narwhal.Validator/RemoveCollections", in, out, opts...)
	if err != nil {
		return nil, err
	}
	return out, nil
}

func (c *validatorClient) ReadCausal(ctx context.Context, in *ReadCausalRequest, opts ...grpc.CallOption) (*ReadCausalResponse, error) {
	out := new(ReadCausalResponse)
	err := c.cc.Invoke(ctx, "/narwhal.Validator/ReadCausal", in, out, opts...)
	if err != nil {
		return nil, err
	}
	return out, nil
}

// ValidatorServer is the server API for Validator service.
// All implementations must embed UnimplementedValidatorServer
// for forward compatibility
type ValidatorServer interface {
	// Returns collection contents for each requested collection.
	GetCollections(context.Context, *GetCollectionsRequest) (*GetCollectionsResponse, error)
	// Expunges collections from the mempool.
	RemoveCollections(context.Context, *RemoveCollectionsRequest) (*Empty, error)
	// Returns collections along a DAG walk with a well-defined starting point.
	ReadCausal(context.Context, *ReadCausalRequest) (*ReadCausalResponse, error)
	mustEmbedUnimplementedValidatorServer()
}

// UnimplementedValidatorServer must be embedded to have forward compatible implementations.
type UnimplementedValidatorServer struct {
}

func (UnimplementedValidatorServer) GetCollections(context.Context, *GetCollectionsRequest) (*GetCollectionsResponse, error) {
	return nil, status.Errorf(codes.Unimplemented, "method GetCollections not implemented")
}
func (UnimplementedValidatorServer) RemoveCollections(context.Context, *RemoveCollectionsRequest) (*Empty, error) {
	return nil, status.Errorf(codes.Unimplemented, "method RemoveCollections not implemented")
}
func (UnimplementedValidatorServer) ReadCausal(context.Context, *ReadCausalRequest) (*ReadCausalResponse, error) {
	return nil, status.Errorf(codes.Unimplemented, "method ReadCausal not implemented")
}
func (UnimplementedValidatorServer) mustEmbedUnimplementedValidatorServer() {}

// UnsafeValidatorServer may be embedded to opt out of forward compatibility for this service.
// Use of this interface is not recommended, as added methods to ValidatorServer will
// result in compilation errors.
type UnsafeValidatorServer interface {
	mustEmbedUnimplementedValidatorServer()
}

func RegisterValidatorServer(s grpc.ServiceRegistrar, srv ValidatorServer) {
	s.RegisterService(&Validator_ServiceDesc, srv)
}

func _Validator_GetCollections_Handler(srv interface{}, ctx context.Context, dec func(interface{}) error, interceptor grpc.UnaryServerInterceptor) (interface{}, error) {
	in := new(GetCollectionsRequest)
	if err := dec(in); err != nil {
		return nil, err
	}
	if interceptor == nil {
		return srv.(ValidatorServer).GetCollections(ctx, in)
	}
	info := &grpc.UnaryServerInfo{
		Server:     srv,
		FullMethod: "/narwhal.Validator/GetCollections",
	}
	handler := func(ctx context.Context, req interface{}) (interface{}, error) {
		return srv.(ValidatorServer).GetCollections(ctx, req.(*GetCollectionsRequest))
	}
	return interceptor(ctx, in, info, handler)
}

func _Validator_RemoveCollections_Handler(srv interface{}, ctx context.Context, dec func(interface{}) error, interceptor grpc.UnaryServerInterceptor) (interface{}, error) {
	in := new(RemoveCollectionsRequest)
	if err := dec(in); err != nil {
		return nil, err
	}
	if interceptor == nil {
		return srv.(ValidatorServer).RemoveCollections(ctx, in)
	}
	info := &grpc.UnaryServerInfo{
		Server:     srv,
		FullMethod: "/narwhal.Validator/RemoveCollections",
	}
	handler := func(ctx context.Context, req interface{}) (interface{}, error) {
		return srv.(ValidatorServer).RemoveCollections(ctx, req.(*RemoveCollectionsRequest))
	}
	return interceptor(ctx, in, info, handler)
}

func _Validator_ReadCausal_Handler(srv interface{}, ctx context.Context, dec func(interface{}) error, interceptor grpc.UnaryServerInterceptor) (interface{}, error) {
	in := new(ReadCausalRequest)
	if err := dec(in); err != nil {
		return nil, err
	}
	if interceptor == nil {
		return srv.(ValidatorServer).ReadCausal(ctx, in)
	}
	info := &grpc.UnaryServerInfo{
		Server:     srv,
		FullMethod: "/narwhal.Validator/ReadCausal",
	}
	handler := func(ctx context.Context, req interface{}) (interface{}, error) {
		return srv.(ValidatorServer).ReadCausal(ctx, req.(*ReadCausalRequest))
	}
	return interceptor(ctx, in, info, handler)
}

// Validator_ServiceDesc is the grpc.ServiceDesc for Validator service.
// It's only intended for direct use with grpc.RegisterService,
// and not to be introspected or modified (even as a copy)
var Validator_ServiceDesc = grpc.ServiceDesc{
	ServiceName: "narwhal.Validator",
	HandlerType: (*ValidatorServer)(nil),
	Methods: []grpc.MethodDesc{
		{
			MethodName: "GetCollections",
			Handler:    _Validator_GetCollections_Handler,
		},
		{
			MethodName: "RemoveCollections",
			Handler:    _Validator_RemoveCollections_Handler,
		},
		{
			MethodName: "ReadCausal",
			Handler:    _Validator_ReadCausal_Handler,
		},
	},
	Streams:  []grpc.StreamDesc{},
	Metadata: "narwhal.proto",
}

// ProposerClient is the client API for Proposer service.
//
// For semantics around ctx use and closing/ending streaming RPCs, please refer to https://pkg.go.dev/google.golang.org/grpc/?tab=doc#ClientConn.NewStream.
type ProposerClient interface {
	Rounds(ctx context.Context, in *RoundsRequest, opts ...grpc.CallOption) (*RoundsResponse, error)
	// Returns the read_causal obtained by starting the DAG walk at the collection
	// proposed by the input authority (as indicated by their public key) at the input round
	NodeReadCausal(ctx context.Context, in *NodeReadCausalRequest, opts ...grpc.CallOption) (*NodeReadCausalResponse, error)
}

type proposerClient struct {
	cc grpc.ClientConnInterface
}

func NewProposerClient(cc grpc.ClientConnInterface) ProposerClient {
	return &proposerClient{cc}
}

func (c *proposerClient) Rounds(ctx context.Context, in *RoundsRequest, opts ...grpc.CallOption) (*RoundsResponse, error) {
	out := new(RoundsResponse)
	err := c.cc.Invoke(ctx, "/narwhal.Proposer/Rounds", in, out, opts...)
	if err != nil {
		return nil, err
	}
	return out, nil
}

func (c *proposerClient) NodeReadCausal(ctx context.Context, in *NodeReadCausalRequest, opts ...grpc.CallOption) (*NodeReadCausalResponse, error) {
	out := new(NodeReadCausalResponse)
	err := c.cc.Invoke(ctx, "/narwhal.Proposer/NodeReadCausal", in, out, opts...)
	if err != nil {
		return nil, err
	}
	return out, nil
}

// ProposerServer is the server API for Proposer service.
// All implementations must embed UnimplementedProposerServer
// for forward compatibility
type ProposerServer interface {
	Rounds(context.Context, *RoundsRequest) (*RoundsResponse, error)
	// Returns the read_causal obtained by starting the DAG walk at the collection
	// proposed by the input authority (as indicated by their public key) at the input round
	NodeReadCausal(context.Context, *NodeReadCausalRequest) (*NodeReadCausalResponse, error)
	mustEmbedUnimplementedProposerServer()
}

// UnimplementedProposerServer must be embedded to have forward compatible implementations.
type UnimplementedProposerServer struct {
}

func (UnimplementedProposerServer) Rounds(context.Context, *RoundsRequest) (*RoundsResponse, error) {
	return nil, status.Errorf(codes.Unimplemented, "method Rounds not implemented")
}
func (UnimplementedProposerServer) NodeReadCausal(context.Context, *NodeReadCausalRequest) (*NodeReadCausalResponse, error) {
	return nil, status.Errorf(codes.Unimplemented, "method NodeReadCausal not implemented")
}
func (UnimplementedProposerServer) mustEmbedUnimplementedProposerServer() {}

// UnsafeProposerServer may be embedded to opt out of forward compatibility for this service.
// Use of this interface is not recommended, as added methods to ProposerServer will
// result in compilation errors.
type UnsafeProposerServer interface {
	mustEmbedUnimplementedProposerServer()
}

func RegisterProposerServer(s grpc.ServiceRegistrar, srv ProposerServer) {
	s.RegisterService(&Proposer_ServiceDesc, srv)
}

func _Proposer_Rounds_Handler(srv interface{}, ctx context.Context, dec func(interface{}) error, interceptor grpc.UnaryServerInterceptor) (interface{}, error) {
	in := new(RoundsRequest)
	if err := dec(in); err != nil {
		return nil, err
	}
	if interceptor == nil {
		return srv.(ProposerServer).Rounds(ctx, in)
	}
	info := &grpc.UnaryServerInfo{
		Server:     srv,
		FullMethod: "/narwhal.Proposer/Rounds",
	}
	handler := func(ctx context.Context, req interface{}) (interface{}, error) {
		return srv.(ProposerServer).Rounds(ctx, req.(*RoundsRequest))
	}
	return interceptor(ctx, in, info, handler)
}

func _Proposer_NodeReadCausal_Handler(srv interface{}, ctx context.Context, dec func(interface{}) error, interceptor grpc.UnaryServerInterceptor) (interface{}, error) {
	in := new(NodeReadCausalRequest)
	if err := dec(in); err != nil {
		return nil, err
	}
	if interceptor == nil {
		return srv.(ProposerServer).NodeReadCausal(ctx, in)
	}
	info := &grpc.UnaryServerInfo{
		Server:     srv,
		FullMethod: "/narwhal.Proposer/NodeReadCausal",
	}
	handler := func(ctx context.Context, req interface{}) (interface{}, error) {
		return srv.(ProposerServer).NodeReadCausal(ctx, req.(*NodeReadCausalRequest))
	}
	return interceptor(ctx, in, info, handler)
}

// Proposer_ServiceDesc is the grpc.ServiceDesc for Proposer service.
// It's only intended for direct use with grpc.RegisterService,
// and not to be introspected or modified (even as a copy)
var Proposer_ServiceDesc = grpc.ServiceDesc{
	ServiceName: "narwhal.Proposer",
	HandlerType: (*ProposerServer)(nil),
	Methods: []grpc.MethodDesc{
		{
			MethodName: "Rounds",
			Handler:    _Proposer_Rounds_Handler,
		},
		{
			MethodName: "NodeReadCausal",
			Handler:    _Proposer_NodeReadCausal_Handler,
		},
	},
	Streams:  []grpc.StreamDesc{},
	Metadata: "narwhal.proto",
}

// ConfigurationClient is the client API for Configuration service.
//
// For semantics around ctx use and closing/ending streaming RPCs, please refer to https://pkg.go.dev/google.golang.org/grpc/?tab=doc#ClientConn.NewStream.
type ConfigurationClient interface {
	// Signals a new epoch
	NewEpoch(ctx context.Context, in *NewEpochRequest, opts ...grpc.CallOption) (*Empty, error)
	// Signals a change in networking info
	NewNetworkInfo(ctx context.Context, in *NewNetworkInfoRequest, opts ...grpc.CallOption) (*Empty, error)
}

type configurationClient struct {
	cc grpc.ClientConnInterface
}

func NewConfigurationClient(cc grpc.ClientConnInterface) ConfigurationClient {
	return &configurationClient{cc}
}

func (c *configurationClient) NewEpoch(ctx context.Context, in *NewEpochRequest, opts ...grpc.CallOption) (*Empty, error) {
	out := new(Empty)
	err := c.cc.Invoke(ctx, "/narwhal.Configuration/NewEpoch", in, out, opts...)
	if err != nil {
		return nil, err
	}
	return out, nil
}

func (c *configurationClient) NewNetworkInfo(ctx context.Context, in *NewNetworkInfoRequest, opts ...grpc.CallOption) (*Empty, error) {
	out := new(Empty)
	err := c.cc.Invoke(ctx, "/narwhal.Configuration/NewNetworkInfo", in, out, opts...)
	if err != nil {
		return nil, err
	}
	return out, nil
}

// ConfigurationServer is the server API for Configuration service.
// All implementations must embed UnimplementedConfigurationServer
// for forward compatibility
type ConfigurationServer interface {
	// Signals a new epoch
	NewEpoch(context.Context, *NewEpochRequest) (*Empty, error)
	// Signals a change in networking info
	NewNetworkInfo(context.Context, *NewNetworkInfoRequest) (*Empty, error)
	mustEmbedUnimplementedConfigurationServer()
}

// UnimplementedConfigurationServer must be embedded to have forward compatible implementations.
type UnimplementedConfigurationServer struct {
}

func (UnimplementedConfigurationServer) NewEpoch(context.Context, *NewEpochRequest) (*Empty, error) {
	return nil, status.Errorf(codes.Unimplemented, "method NewEpoch not implemented")
}
func (UnimplementedConfigurationServer) NewNetworkInfo(context.Context, *NewNetworkInfoRequest) (*Empty, error) {
	return nil, status.Errorf(codes.Unimplemented, "method NewNetworkInfo not implemented")
}
func (UnimplementedConfigurationServer) mustEmbedUnimplementedConfigurationServer() {}

// UnsafeConfigurationServer may be embedded to opt out of forward compatibility for this service.
// Use of this interface is not recommended, as added methods to ConfigurationServer will
// result in compilation errors.
type UnsafeConfigurationServer interface {
	mustEmbedUnimplementedConfigurationServer()
}

func RegisterConfigurationServer(s grpc.ServiceRegistrar, srv ConfigurationServer) {
	s.RegisterService(&Configuration_ServiceDesc, srv)
}

func _Configuration_NewEpoch_Handler(srv interface{}, ctx context.Context, dec func(interface{}) error, interceptor grpc.UnaryServerInterceptor) (interface{}, error) {
	in := new(NewEpochRequest)
	if err := dec(in); err != nil {
		return nil, err
	}
	if interceptor == nil {
		return srv.(ConfigurationServer).NewEpoch(ctx, in)
	}
	info := &grpc.UnaryServerInfo{
		Server:     srv,
		FullMethod: "/narwhal.Configuration/NewEpoch",
	}
	handler := func(ctx context.Context, req interface{}) (interface{}, error) {
		return srv.(ConfigurationServer).NewEpoch(ctx, req.(*NewEpochRequest))
	}
	return interceptor(ctx, in, info, handler)
}

func _Configuration_NewNetworkInfo_Handler(srv interface{}, ctx context.Context, dec func(interface{}) error, interceptor grpc.UnaryServerInterceptor) (interface{}, error) {
	in := new(NewNetworkInfoRequest)
	if err := dec(in); err != nil {
		return nil, err
	}
	if interceptor == nil {
		return srv.(ConfigurationServer).NewNetworkInfo(ctx, in)
	}
	info := &grpc.UnaryServerInfo{
		Server:     srv,
		FullMethod: "/narwhal.Configuration/NewNetworkInfo",
	}
	handler := func(ctx context.Context, req interface{}) (interface{}, error) {
		return srv.(ConfigurationServer).NewNetworkInfo(ctx, req.(*NewNetworkInfoRequest))
	}
	return interceptor(ctx, in, info, handler)
}

// Configuration_ServiceDesc is the grpc.ServiceDesc for Configuration service.
// It's only intended for direct use with grpc.RegisterService,
// and not to be introspected or modified (even as a copy)
var Configuration_ServiceDesc = grpc.ServiceDesc{
	ServiceName: "narwhal.Configuration",
	HandlerType: (*ConfigurationServer)(nil),
	Methods: []grpc.MethodDesc{
		{
			MethodName: "NewEpoch",
			Handler:    _Configuration_NewEpoch_Handler,
		},
		{
			MethodName: "NewNetworkInfo",
			Handler:    _Configuration_NewNetworkInfo_Handler,
		},
	},
	Streams:  []grpc.StreamDesc{},
	Metadata: "narwhal.proto",
}

// PrimaryToPrimaryClient is the client API for PrimaryToPrimary service.
//
// For semantics around ctx use and closing/ending streaming RPCs, please refer to https://pkg.go.dev/google.golang.org/grpc/?tab=doc#ClientConn.NewStream.
type PrimaryToPrimaryClient interface {
	// Sends a message
	SendMessage(ctx context.Context, in *BincodeEncodedPayload, opts ...grpc.CallOption) (*Empty, error)
}

type primaryToPrimaryClient struct {
	cc grpc.ClientConnInterface
}

func NewPrimaryToPrimaryClient(cc grpc.ClientConnInterface) PrimaryToPrimaryClient {
	return &primaryToPrimaryClient{cc}
}

func (c *primaryToPrimaryClient) SendMessage(ctx context.Context, in *BincodeEncodedPayload, opts ...grpc.CallOption) (*Empty, error) {
	out := new(Empty)
	err := c.cc.Invoke(ctx, "/narwhal.PrimaryToPrimary/SendMessage", in, out, opts...)
	if err != nil {
		return nil, err
	}
	return out, nil
}

// PrimaryToPrimaryServer is the server API for PrimaryToPrimary service.
// All implementations must embed UnimplementedPrimaryToPrimaryServer
// for forward compatibility
type PrimaryToPrimaryServer interface {
	// Sends a message
	SendMessage(context.Context, *BincodeEncodedPayload) (*Empty, error)
	mustEmbedUnimplementedPrimaryToPrimaryServer()
}

// UnimplementedPrimaryToPrimaryServer must be embedded to have forward compatible implementations.
type UnimplementedPrimaryToPrimaryServer struct {
}

func (UnimplementedPrimaryToPrimaryServer) SendMessage(context.Context, *BincodeEncodedPayload) (*Empty, error) {
	return nil, status.Errorf(codes.Unimplemented, "method SendMessage not implemented")
}
func (UnimplementedPrimaryToPrimaryServer) mustEmbedUnimplementedPrimaryToPrimaryServer() {}

// UnsafePrimaryToPrimaryServer may be embedded to opt out of forward compatibility for this service.
// Use of this interface is not recommended, as added methods to PrimaryToPrimaryServer will
// result in compilation errors.
type UnsafePrimaryToPrimaryServer interface {
	mustEmbedUnimplementedPrimaryToPrimaryServer()
}

func RegisterPrimaryToPrimaryServer(s grpc.ServiceRegistrar, srv PrimaryToPrimaryServer) {
	s.RegisterService(&PrimaryToPrimary_ServiceDesc, srv)
}

func _PrimaryToPrimary_SendMessage_Handler(srv interface{}, ctx context.Context, dec func(interface{}) error, interceptor grpc.UnaryServerInterceptor) (interface{}, error) {
	in := new(BincodeEncodedPayload)
	if err := dec(in); err != nil {
		return nil, err
	}
	if interceptor == nil {
		return srv.(PrimaryToPrimaryServer).SendMessage(ctx, in)
	}
	info := &grpc.UnaryServerInfo{
		Server:     srv,
		FullMethod: "/narwhal.PrimaryToPrimary/SendMessage",
	}
	handler := func(ctx context.Context, req interface{}) (interface{}, error) {
		return srv.(PrimaryToPrimaryServer).SendMessage(ctx, req.(*BincodeEncodedPayload))
	}
	return interceptor(ctx, in, info, handler)
}

// PrimaryToPrimary_ServiceDesc is the grpc.ServiceDesc for PrimaryToPrimary service.
// It's only intended for direct use with grpc.RegisterService,
// and not to be introspected or modified (even as a copy)
var PrimaryToPrimary_ServiceDesc = grpc.ServiceDesc{
	ServiceName: "narwhal.PrimaryToPrimary",
	HandlerType: (*PrimaryToPrimaryServer)(nil),
	Methods: []grpc.MethodDesc{
		{
			MethodName: "SendMessage",
			Handler:    _PrimaryToPrimary_SendMessage_Handler,
		},
	},
	Streams:  []grpc.StreamDesc{},
	Metadata: "narwhal.proto",
}

// WorkerToWorkerClient is the client API for WorkerToWorker service.
//
// For semantics around ctx use and closing/ending streaming RPCs, please refer to https://pkg.go.dev/google.golang.org/grpc/?tab=doc#ClientConn.NewStream.
type WorkerToWorkerClient interface {
	// Sends a worker message
	SendMessage(ctx context.Context, in *BincodeEncodedPayload, opts ...grpc.CallOption) (*Empty, error)
	// requests a number of batches that the service then streams back to the client
	ClientBatchRequest(ctx context.Context, in *BincodeEncodedPayload, opts ...grpc.CallOption) (WorkerToWorker_ClientBatchRequestClient, error)
}

type workerToWorkerClient struct {
	cc grpc.ClientConnInterface
}

func NewWorkerToWorkerClient(cc grpc.ClientConnInterface) WorkerToWorkerClient {
	return &workerToWorkerClient{cc}
}

func (c *workerToWorkerClient) SendMessage(ctx context.Context, in *BincodeEncodedPayload, opts ...grpc.CallOption) (*Empty, error) {
	out := new(Empty)
	err := c.cc.Invoke(ctx, "/narwhal.WorkerToWorker/SendMessage", in, out, opts...)
	if err != nil {
		return nil, err
	}
	return out, nil
}

func (c *workerToWorkerClient) ClientBatchRequest(ctx context.Context, in *BincodeEncodedPayload, opts ...grpc.CallOption) (WorkerToWorker_ClientBatchRequestClient, error) {
	stream, err := c.cc.NewStream(ctx, &WorkerToWorker_ServiceDesc.Streams[0], "/narwhal.WorkerToWorker/ClientBatchRequest", opts...)
	if err != nil {
		return nil, err
	}
	x := &workerToWorkerClientBatchRequestClient{stream}
	if err := x.ClientStream.SendMsg(in); err != nil {
		return nil, err
	}
	if err := x.ClientStream.CloseSend(); err != nil {
		return nil, err
	}
	return x, nil
}

type WorkerToWorker_ClientBatchRequestClient interface {
	Recv() (*BincodeEncodedPayload, error)
	grpc.ClientStream
}

type workerToWorkerClientBatchRequestClient struct {
	grpc.ClientStream
}

func (x *workerToWorkerClientBatchRequestClient) Recv() (*BincodeEncodedPayload, error) {
	m := new(BincodeEncodedPayload)
	if err := x.ClientStream.RecvMsg(m); err != nil {
		return nil, err
	}
	return m, nil
}

// WorkerToWorkerServer is the server API for WorkerToWorker service.
// All implementations must embed UnimplementedWorkerToWorkerServer
// for forward compatibility
type WorkerToWorkerServer interface {
	// Sends a worker message
	SendMessage(context.Context, *BincodeEncodedPayload) (*Empty, error)
	// requests a number of batches that the service then streams back to the client
	ClientBatchRequest(*BincodeEncodedPayload, WorkerToWorker_ClientBatchRequestServer) error
	mustEmbedUnimplementedWorkerToWorkerServer()
}

// UnimplementedWorkerToWorkerServer must be embedded to have forward compatible implementations.
type UnimplementedWorkerToWorkerServer struct {
}

func (UnimplementedWorkerToWorkerServer) SendMessage(context.Context, *BincodeEncodedPayload) (*Empty, error) {
	return nil, status.Errorf(codes.Unimplemented, "method SendMessage not implemented")
}
func (UnimplementedWorkerToWorkerServer) ClientBatchRequest(*BincodeEncodedPayload, WorkerToWorker_ClientBatchRequestServer) error {
	return status.Errorf(codes.Unimplemented, "method ClientBatchRequest not implemented")
}
func (UnimplementedWorkerToWorkerServer) mustEmbedUnimplementedWorkerToWorkerServer() {}

// UnsafeWorkerToWorkerServer may be embedded to opt out of forward compatibility for this service.
// Use of this interface is not recommended, as added methods to WorkerToWorkerServer will
// result in compilation errors.
type UnsafeWorkerToWorkerServer interface {
	mustEmbedUnimplementedWorkerToWorkerServer()
}

func RegisterWorkerToWorkerServer(s grpc.ServiceRegistrar, srv WorkerToWorkerServer) {
	s.RegisterService(&WorkerToWorker_ServiceDesc, srv)
}

func _WorkerToWorker_SendMessage_Handler(srv interface{}, ctx context.Context, dec func(interface{}) error, interceptor grpc.UnaryServerInterceptor) (interface{}, error) {
	in := new(BincodeEncodedPayload)
	if err := dec(in); err != nil {
		return nil, err
	}
	if interceptor == nil {
		return srv.(WorkerToWorkerServer).SendMessage(ctx, in)
	}
	info := &grpc.UnaryServerInfo{
		Server:     srv,
		FullMethod: "/narwhal.WorkerToWorker/SendMessage",
	}
	handler := func(ctx context.Context, req interface{}) (interface{}, error) {
		return srv.(WorkerToWorkerServer).SendMessage(ctx, req.(*BincodeEncodedPayload))
	}
	return interceptor(ctx, in, info, handler)
}

func _WorkerToWorker_ClientBatchRequest_Handler(srv interface{}, stream grpc.ServerStream) error {
	m := new(BincodeEncodedPayload)
	if err := stream.RecvMsg(m); err != nil {
		return err
	}
	return srv.(WorkerToWorkerServer).ClientBatchRequest(m, &workerToWorkerClientBatchRequestServer{stream})
}

type WorkerToWorker_ClientBatchRequestServer interface {
	Send(*BincodeEncodedPayload) error
	grpc.ServerStream
}

type workerToWorkerClientBatchRequestServer struct {
	grpc.ServerStream
}

func (x *workerToWorkerClientBatchRequestServer) Send(m *BincodeEncodedPayload) error {
	return x.ServerStream.SendMsg(m)
}

// WorkerToWorker_ServiceDesc is the grpc.ServiceDesc for WorkerToWorker service.
// It's only intended for direct use with grpc.RegisterService,
// and not to be introspected or modified (even as a copy)
var WorkerToWorker_ServiceDesc = grpc.ServiceDesc{
	ServiceName: "narwhal.WorkerToWorker",
	HandlerType: (*WorkerToWorkerServer)(nil),
	Methods: []grpc.MethodDesc{
		{
			MethodName: "SendMessage",
			Handler:    _WorkerToWorker_SendMessage_Handler,
		},
	},
	Streams: []grpc.StreamDesc{
		{
			StreamName:    "ClientBatchRequest",
			Handler:       _WorkerToWorker_ClientBatchRequest_Handler,
			ServerStreams: true,
		},
	},
	Metadata: "narwhal.proto",
}

// WorkerToPrimaryClient is the client API for WorkerToPrimary service.
//
// For semantics around ctx use and closing/ending streaming RPCs, please refer to https://pkg.go.dev/google.golang.org/grpc/?tab=doc#ClientConn.NewStream.
type WorkerToPrimaryClient interface {
	// Sends a message
	SendMessage(ctx context.Context, in *BincodeEncodedPayload, opts ...grpc.CallOption) (*Empty, error)
}

type workerToPrimaryClient struct {
	cc grpc.ClientConnInterface
}

func NewWorkerToPrimaryClient(cc grpc.ClientConnInterface) WorkerToPrimaryClient {
	return &workerToPrimaryClient{cc}
}

func (c *workerToPrimaryClient) SendMessage(ctx context.Context, in *BincodeEncodedPayload, opts ...grpc.CallOption) (*Empty, error) {
	out := new(Empty)
	err := c.cc.Invoke(ctx, "/narwhal.WorkerToPrimary/SendMessage", in, out, opts...)
	if err != nil {
		return nil, err
	}
	return out, nil
}

// WorkerToPrimaryServer is the server API for WorkerToPrimary service.
// All implementations must embed UnimplementedWorkerToPrimaryServer
// for forward compatibility
type WorkerToPrimaryServer interface {
	// Sends a message
	SendMessage(context.Context, *BincodeEncodedPayload) (*Empty, error)
	mustEmbedUnimplementedWorkerToPrimaryServer()
}

// UnimplementedWorkerToPrimaryServer must be embedded to have forward compatible implementations.
type UnimplementedWorkerToPrimaryServer struct {
}

func (UnimplementedWorkerToPrimaryServer) SendMessage(context.Context, *BincodeEncodedPayload) (*Empty, error) {
	return nil, status.Errorf(codes.Unimplemented, "method SendMessage not implemented")
}
func (UnimplementedWorkerToPrimaryServer) mustEmbedUnimplementedWorkerToPrimaryServer() {}

// UnsafeWorkerToPrimaryServer may be embedded to opt out of forward compatibility for this service.
// Use of this interface is not recommended, as added methods to WorkerToPrimaryServer will
// result in compilation errors.
type UnsafeWorkerToPrimaryServer interface {
	mustEmbedUnimplementedWorkerToPrimaryServer()
}

func RegisterWorkerToPrimaryServer(s grpc.ServiceRegistrar, srv WorkerToPrimaryServer) {
	s.RegisterService(&WorkerToPrimary_ServiceDesc, srv)
}

func _WorkerToPrimary_SendMessage_Handler(srv interface{}, ctx context.Context, dec func(interface{}) error, interceptor grpc.UnaryServerInterceptor) (interface{}, error) {
	in := new(BincodeEncodedPayload)
	if err := dec(in); err != nil {
		return nil, err
	}
	if interceptor == nil {
		return srv.(WorkerToPrimaryServer).SendMessage(ctx, in)
	}
	info := &grpc.UnaryServerInfo{
		Server:     srv,
		FullMethod: "/narwhal.WorkerToPrimary/SendMessage",
	}
	handler := func(ctx context.Context, req interface{}) (interface{}, error) {
		return srv.(WorkerToPrimaryServer).SendMessage(ctx, req.(*BincodeEncodedPayload))
	}
	return interceptor(ctx, in, info, handler)
}

// WorkerToPrimary_ServiceDesc is the grpc.ServiceDesc for WorkerToPrimary service.
// It's only intended for direct use with grpc.RegisterService,
// and not to be introspected or modified (even as a copy)
var WorkerToPrimary_ServiceDesc = grpc.ServiceDesc{
	ServiceName: "narwhal.WorkerToPrimary",
	HandlerType: (*WorkerToPrimaryServer)(nil),
	Methods: []grpc.MethodDesc{
		{
			MethodName: "SendMessage",
			Handler:    _WorkerToPrimary_SendMessage_Handler,
		},
	},
	Streams:  []grpc.StreamDesc{},
	Metadata: "narwhal.proto",
}

// PrimaryToWorkerClient is the client API for PrimaryToWorker service.
//
// For semantics around ctx use and closing/ending streaming RPCs, please refer to https://pkg.go.dev/google.golang.org/grpc/?tab=doc#ClientConn.NewStream.
type PrimaryToWorkerClient interface {
	// Sends a message
	SendMessage(ctx context.Context, in *BincodeEncodedPayload, opts ...grpc.CallOption) (*Empty, error)
}

type primaryToWorkerClient struct {
	cc grpc.ClientConnInterface
}

func NewPrimaryToWorkerClient(cc grpc.ClientConnInterface) PrimaryToWorkerClient {
	return &primaryToWorkerClient{cc}
}

func (c *primaryToWorkerClient) SendMessage(ctx context.Context, in *BincodeEncodedPayload, opts ...grpc.CallOption) (*Empty, error) {
	out := new(Empty)
	err := c.cc.Invoke(ctx, "/narwhal.PrimaryToWorker/SendMessage", in, out, opts...)
	if err != nil {
		return nil, err
	}
	return out, nil
}

// PrimaryToWorkerServer is the server API for PrimaryToWorker service.
// All implementations must embed UnimplementedPrimaryToWorkerServer
// for forward compatibility
type PrimaryToWorkerServer interface {
	// Sends a message
	SendMessage(context.Context, *BincodeEncodedPayload) (*Empty, error)
	mustEmbedUnimplementedPrimaryToWorkerServer()
}

// UnimplementedPrimaryToWorkerServer must be embedded to have forward compatible implementations.
type UnimplementedPrimaryToWorkerServer struct {
}

func (UnimplementedPrimaryToWorkerServer) SendMessage(context.Context, *BincodeEncodedPayload) (*Empty, error) {
	return nil, status.Errorf(codes.Unimplemented, "method SendMessage not implemented")
}
func (UnimplementedPrimaryToWorkerServer) mustEmbedUnimplementedPrimaryToWorkerServer() {}

// UnsafePrimaryToWorkerServer may be embedded to opt out of forward compatibility for this service.
// Use of this interface is not recommended, as added methods to PrimaryToWorkerServer will
// result in compilation errors.
type UnsafePrimaryToWorkerServer interface {
	mustEmbedUnimplementedPrimaryToWorkerServer()
}

func RegisterPrimaryToWorkerServer(s grpc.ServiceRegistrar, srv PrimaryToWorkerServer) {
	s.RegisterService(&PrimaryToWorker_ServiceDesc, srv)
}

func _PrimaryToWorker_SendMessage_Handler(srv interface{}, ctx context.Context, dec func(interface{}) error, interceptor grpc.UnaryServerInterceptor) (interface{}, error) {
	in := new(BincodeEncodedPayload)
	if err := dec(in); err != nil {
		return nil, err
	}
	if interceptor == nil {
		return srv.(PrimaryToWorkerServer).SendMessage(ctx, in)
	}
	info := &grpc.UnaryServerInfo{
		Server:     srv,
		FullMethod: "/narwhal.PrimaryToWorker/SendMessage",
	}
	handler := func(ctx context.Context, req interface{}) (interface{}, error) {
		return srv.(PrimaryToWorkerServer).SendMessage(ctx, req.(*BincodeEncodedPayload))
	}
	return interceptor(ctx, in, info, handler)
}

// PrimaryToWorker_ServiceDesc is the grpc.ServiceDesc for PrimaryToWorker service.
// It's only intended for direct use with grpc.RegisterService,
// and not to be introspected or modified (even as a copy)
var PrimaryToWorker_ServiceDesc = grpc.ServiceDesc{
	ServiceName: "narwhal.PrimaryToWorker",
	HandlerType: (*PrimaryToWorkerServer)(nil),
	Methods: []grpc.MethodDesc{
		{
			MethodName: "SendMessage",
			Handler:    _PrimaryToWorker_SendMessage_Handler,
		},
	},
	Streams:  []grpc.StreamDesc{},
	Metadata: "narwhal.proto",
}

// TransactionsClient is the client API for Transactions service.
//
// For semantics around ctx use and closing/ending streaming RPCs, please refer to https://pkg.go.dev/google.golang.org/grpc/?tab=doc#ClientConn.NewStream.
type TransactionsClient interface {
	// Submit a Transactions
	SubmitTransaction(ctx context.Context, in *Transaction, opts ...grpc.CallOption) (*Empty, error)
	// Submit a Transactions
	SubmitTransactionStream(ctx context.Context, opts ...grpc.CallOption) (Transactions_SubmitTransactionStreamClient, error)
}

type transactionsClient struct {
	cc grpc.ClientConnInterface
}

func NewTransactionsClient(cc grpc.ClientConnInterface) TransactionsClient {
	return &transactionsClient{cc}
}

func (c *transactionsClient) SubmitTransaction(ctx context.Context, in *Transaction, opts ...grpc.CallOption) (*Empty, error) {
	out := new(Empty)
	err := c.cc.Invoke(ctx, "/narwhal.Transactions/SubmitTransaction", in, out, opts...)
	if err != nil {
		return nil, err
	}
	return out, nil
}

func (c *transactionsClient) SubmitTransactionStream(ctx context.Context, opts ...grpc.CallOption) (Transactions_SubmitTransactionStreamClient, error) {
	stream, err := c.cc.NewStream(ctx, &Transactions_ServiceDesc.Streams[0], "/narwhal.Transactions/SubmitTransactionStream", opts...)
	if err != nil {
		return nil, err
	}
	x := &transactionsSubmitTransactionStreamClient{stream}
	return x, nil
}

type Transactions_SubmitTransactionStreamClient interface {
	Send(*Transaction) error
	CloseAndRecv() (*Empty, error)
	grpc.ClientStream
}

type transactionsSubmitTransactionStreamClient struct {
	grpc.ClientStream
}

func (x *transactionsSubmitTransactionStreamClient) Send(m *Transaction) error {
	return x.ClientStream.SendMsg(m)
}

func (x *transactionsSubmitTransactionStreamClient) CloseAndRecv() (*Empty, error) {
	if err := x.ClientStream.CloseSend(); err != nil {
		return nil, err
	}
	m := new(Empty)
	if err := x.ClientStream.RecvMsg(m); err != nil {
		return nil, err
	}
	return m, nil
}

// TransactionsServer is the server API for Transactions service.
// All implementations must embed UnimplementedTransactionsServer
// for forward compatibility
type TransactionsServer interface {
	// Submit a Transactions
	SubmitTransaction(context.Context, *Transaction) (*Empty, error)
	// Submit a Transactions
	SubmitTransactionStream(Transactions_SubmitTransactionStreamServer) error
	mustEmbedUnimplementedTransactionsServer()
}

// UnimplementedTransactionsServer must be embedded to have forward compatible implementations.
type UnimplementedTransactionsServer struct {
}

func (UnimplementedTransactionsServer) SubmitTransaction(context.Context, *Transaction) (*Empty, error) {
	return nil, status.Errorf(codes.Unimplemented, "method SubmitTransaction not implemented")
}
func (UnimplementedTransactionsServer) SubmitTransactionStream(Transactions_SubmitTransactionStreamServer) error {
	return status.Errorf(codes.Unimplemented, "method SubmitTransactionStream not implemented")
}
func (UnimplementedTransactionsServer) mustEmbedUnimplementedTransactionsServer() {}

// UnsafeTransactionsServer may be embedded to opt out of forward compatibility for this service.
// Use of this interface is not recommended, as added methods to TransactionsServer will
// result in compilation errors.
type UnsafeTransactionsServer interface {
	mustEmbedUnimplementedTransactionsServer()
}

func RegisterTransactionsServer(s grpc.ServiceRegistrar, srv TransactionsServer) {
	s.RegisterService(&Transactions_ServiceDesc, srv)
}

func _Transactions_SubmitTransaction_Handler(srv interface{}, ctx context.Context, dec func(interface{}) error, interceptor grpc.UnaryServerInterceptor) (interface{}, error) {
	in := new(Transaction)
	if err := dec(in); err != nil {
		return nil, err
	}
	if interceptor == nil {
		return srv.(TransactionsServer).SubmitTransaction(ctx, in)
	}
	info := &grpc.UnaryServerInfo{
		Server:     srv,
		FullMethod: "/narwhal.Transactions/SubmitTransaction",
	}
	handler := func(ctx context.Context, req interface{}) (interface{}, error) {
		return srv.(TransactionsServer).SubmitTransaction(ctx, req.(*Transaction))
	}
	return interceptor(ctx, in, info, handler)
}

func _Transactions_SubmitTransactionStream_Handler(srv interface{}, stream grpc.ServerStream) error {
	return srv.(TransactionsServer).SubmitTransactionStream(&transactionsSubmitTransactionStreamServer{stream})
}

type Transactions_SubmitTransactionStreamServer interface {
	SendAndClose(*Empty) error
	Recv() (*Transaction, error)
	grpc.ServerStream
}

type transactionsSubmitTransactionStreamServer struct {
	grpc.ServerStream
}

func (x *transactionsSubmitTransactionStreamServer) SendAndClose(m *Empty) error {
	return x.ServerStream.SendMsg(m)
}

func (x *transactionsSubmitTransactionStreamServer) Recv() (*Transaction, error) {
	m := new(Transaction)
	if err := x.ServerStream.RecvMsg(m); err != nil {
		return nil, err
	}
	return m, nil
}

// Transactions_ServiceDesc is the grpc.ServiceDesc for Transactions service.
// It's only intended for direct use with grpc.RegisterService,
// and not to be introspected or modified (even as a copy)
var Transactions_ServiceDesc = grpc.ServiceDesc{
	ServiceName: "narwhal.Transactions",
	HandlerType: (*TransactionsServer)(nil),
	Methods: []grpc.MethodDesc{
		{
			MethodName: "SubmitTransaction",
			Handler:    _Transactions_SubmitTransaction_Handler,
		},
	},
	Streams: []grpc.StreamDesc{
		{
			StreamName:    "SubmitTransactionStream",
			Handler:       _Transactions_SubmitTransactionStream_Handler,
			ClientStreams: true,
		},
	},
	Metadata: "narwhal.proto",
}

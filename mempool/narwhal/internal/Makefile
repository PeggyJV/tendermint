#!/usr/bin/make -f

# Makefile targets for setting up dependencies/tooling for executing tests
# for narwhal/tendermint integrations.define

OS=$(uname)
NARWHAL_DIR=$(echo -n "$NARWHAL_DIR")

compile_protos: install_protoc
	protoc -I $(NARWHAL_DIR)/types/proto \
     	--go_out=. --go_opt=Mnarwhal.proto=./narwhalproto \
     	--go-grpc_out=. --go-grpc_opt=Mnarwhal.proto=./narwhalproto \
    	narwhal.proto


install_tools: install_protoc install_proto_gen_go install_proto_grpc_plugin

install_protoc:
	brew install protobuf
.PHONY: install_protoc

install_proto_gen_go:
	go install google.golang.org/protobuf/cmd/protoc-gen-go@v1.28
.PHONY: install_proto_gen_go

install_proto_grpc_plugin:
	go install google.golang.org/grpc/cmd/protoc-gen-go-grpc@v1.2
.PHONY: install_proto_grpc_plugin


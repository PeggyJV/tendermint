#!/usr/bin/make -f

# Makefile targets for setting up dependencies/tooling for executing tests
# for narwhal/tendermint integrations.define

OS=$(uname)
NARWHAL_DIR=$(echo -n "$NARWHAL_DIR")

build_docker_tm:
	GOOS=linux GOARCH=amd64 go build -v -o docker_run/build/tendermint ../../../cmd/tendermint
.PHONY: build_docker_tm

build_narwhalmint:
	go install ./narwhalmint/cmd/narwhalmint
.PHONY: build_narwhalmint

compile_protos: install_protoc
	protoc -I $(NARWHAL_DIR)/types/proto \
     	--go_out=. --go_opt=Mnarwhal.proto=./narwhalproto \
     	--go-grpc_out=. --go-grpc_opt=Mnarwhal.proto=./narwhalproto \
    	narwhal.proto
.PHONY: compile_protos

install_tools: install_protoc install_proto_gen_go install_proto_grpc_plugin
.PHONY: install_tools

install_protoc:
	brew install protobuf
.PHONY: install_protoc

install_proto_gen_go:
	go install google.golang.org/protobuf/cmd/protoc-gen-go@v1.28
.PHONY: install_proto_gen_go

install_proto_grpc_plugin:
	go install google.golang.org/grpc/cmd/protoc-gen-go-grpc@v1.2
.PHONY: install_proto_grpc_plugin

deploy_gce_instances:
	cd terraform && terraform apply $(ARGS)
.PHONY: deploy_gce_instances

deploy_gce_instances_confirmed: ARGS="-auto-approve"
deploy_gce_instances_confirmed: deploy_gce_instances
.PHONY: deploy_gce_instances_confirmed

setup_gce_instances: build_narwhalmint
	cd terraform && ./setup.sh
.PHONY: setup_gce_instances

destroy_gce_instances:
	cd terraform && terraform destroy $(ARGS)
.PHONY: destroy_gce_instances

destroy_gce_instances_confirmed: ARGS="-auto-approve"
destroy_gce_instances_confirmed: destroy_gce_instances
.PHONY: destroy_gce_instances_confirmed

upload_instance_files:
	gsutil cp terraform/start.sh gs://narwhalmint/start.sh
	gsutil cp terraform/start_services.sh gs://narwhalmint/start_services.sh
.PHONY: upload_instance_files

instance_group:
	@cd terraform && terraform output -json | jq -r '.group_name.value'
.PHONY: instance_group

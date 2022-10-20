#!/usr/bin/make -f

# Makefile targets for setting up dependencies/tooling for executing tests
# for narwhal/tendermint integrations.define

OS := $(shell uname)
TS := $(shell date +"%m-%d_%T")
export DIR ?= demo/${TS}
export DEPLOY_ARGS
export DESTROY_ARGS
export LOAD_ARGS ?= --concurrency 1 --txs 10000
export SETUP_DIR ?= 120
export WAIT_DUR ?= 300
export FOLLOW_ARGS ?= --for $(FOLLOW_DUR)
export STAT_ARGS ?= --json
FOLLOW_DUR ?= 20m

build_docker_tm:
	GOOS=linux GOARCH=amd64 go build -v -o docker_run/build/tendermint ../../../cmd/tendermint
.PHONY: build_docker_tm

build_narwhalmint:
	@go install ./narwhalmint/cmd/narwhalmint
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

deploy_gce_instances: destroy_gce_instances
	cd terraform && terraform apply $(DEPLOY_ARGS)
.PHONY: deploy_gce_instances

deploy_gce_instances_confirmed: DESTROY_ARGS="-auto-approve"
deploy_gce_instances_confirmed: DEPLOY_ARGS="-auto-approve"
deploy_gce_instances_confirmed: deploy_gce_instances
.PHONY: deploy_gce_instances_confirmed

destroy_gce_instances:
	cd terraform && terraform destroy $(DESTROY_ARGS)
.PHONY: destroy_gce_instances

destroy_gce_instances_confirmed: DESTROY_ARGS="-auto-approve"
destroy_gce_instances_confirmed: destroy_gce_instances
.PHONY: destroy_gce_instances_confirmed

setup_gce_instances: build_narwhalmint
	cd terraform && ./setup.sh
.PHONY: setup_gce_instances

run_new_instances: deploy_gce_instances
	sleep $(SETUP_DIR) && MAKE setup_gce_instances
.PHONY: run_new_instances

demo: run_new_instances
	echo "waiting $(WAIT_DUR)s for nodes to be ready" && \
	sleep $(WAIT_DUR) && \
	MAKE load-test && \
	MAKE follow-nodes && \
	MAKE zip_logs && \
	MAKE destroy_gce_instances
.PHONY: demo

demo_confirmed: DEPLOY_ARGS="-auto-approve"
demo_confirmed: DESTROY_ARGS="-auto-approve"
demo_confirmed: demo
.PHONY: demo_confirmed

load-test: build_narwhalmint
	@MAKE instance_ext_ips | narwhalmint load $(LOAD_ARGS)
.PHONY: load-test

follow-nodes: build_narwhalmint
	@MAKE instance_ext_ips | narwhalmint stats -f $(FOLLOW_ARGS)
.PHONY: follow-nodes

node-stats: build_narwhalmint
	@MAKE instance_ext_ips | narwhalmint stats $(STAT_ARGS)
.PHONY: node-stats

upload_instance_files:
	gsutil cp terraform/start.sh gs://narwhalmint/start.sh
	gsutil cp terraform/start_services.sh gs://narwhalmint/start_services.sh
.PHONY: upload_instance_files

kill_validator_processes:
	@ for ip in $$(MAKE instance_ext_ips | jq -r '.[]') ; do \
		ssh -q -o StrictHostKeyChecking=no -o UserKnownHostsFile=/dev/null jwberged@$$ip "sudo pkill -9 tendermint && echo \"$$ip tendermint process terminated\" || true" ; \
		ssh -q -o StrictHostKeyChecking=no -o UserKnownHostsFile=/dev/null jwberged@$$ip "sudo pkill -9 narwhal_node && echo \"$$ip narwhal_node processes terminated\" || true" ; \
	done
.PHONY: kill_validator_processes

zip_logs: build_narwhalmint rm_zipped_logs
	@mkdir -p $(DIR) && echo "created dir $(DIR)" && \
	MAKE node-stats | jq > "$(DIR)/summary.json" && echo "added $(DIR)/summary.json successfully\\n" && \
	echo "killing validator processes" && MAKE kill_validator_processes && \
	echo "all processes terminated...\\n" && \
	for ipset in $$(MAKE instance_ipset | jq -c '.[]') ; do \
		export ip="$$(echo $$ipset | jq -r '.external_ip')" ; \
		echo "zipping config and logs for $$ip (perm denied issues expected)..." && \
		ssh -q -o StrictHostKeyChecking=no -o UserKnownHostsFile=/dev/null jwberged@$$ip \
			'IP=$$(hostname -i) cd /usr/local/lib && zip -q -r "$$HOME/output.zip" narwhal/*.json "narwhal/nodes/$$IP" "tendermint/nodes/$$IP"' ; \
		echo "copying output.zip from $$ip to $(DIR)/$$ip.zip" && \
		scp -q -o StrictHostKeyChecking=no -o UserKnownHostsFile=/dev/null jwberged@$$ip:'$$HOME/output.zip' "$(DIR)/$$ip.zip" && \
		echo "unzipping $(DIR)/$$ip.zip in $(DIR)" && \
		unzip -q -n -d $(DIR) $(DIR)/$$ip.zip && rm $(DIR)/$$ip.zip ; \
		export log_dir="$(DIR)/tendermint/nodes/$$(echo $$ipset | jq -r '.internal_ip')/logs" ; \
		echo "creating trimmed consensus and mempool logs file at $$log_dir" && \
		cat "$$log_dir/log.jsonl" | \
		 jq -c '. | select(.module == "consensus" or .module == "mempool") | select(._msg != "Receive")' > \
			"$$log_dir/logs_consensuspool.jsonl" ; \
		echo "$$ip node zipped successfully\\n" ; \
	done && \
	export final_dir="$(DIR).height_$$(cat $(DIR)/summary.json | jq -r  '.node_status[0].status.sync_info.latest_block_height ?')" ; \
	mv $(DIR) $$final_dir && echo "all unzipped data located at $$final_dir"
.PHONY: zip_logs

rm_zipped_logs:
	@ for ip in $$(MAKE instance_ext_ips | jq -r '.[]') ; do \
		ssh -q -o StrictHostKeyChecking=no -o UserKnownHostsFile=/dev/null jwberged@$$ip 'rm -f $$HOME/output.zip && echo "$$HOME/output.zip removed successfully"' ; \
	done
.PHONY: rm_zipped_logs

instance_ipset:
	@gcloud compute instances list --format "json" --filter "metadata.items: *$$(MAKE instance_group)" | \
		jq '[.[].networkInterfaces[] | { "internal_ip": .networkIP, "external_ip": .accessConfigs[0].natIP}]'
.PHONY: instance_ipset

instance_ext_ips:
	@MAKE instance_ipset | jq 'map(.external_ip)'
.PHONY: instance_ext_ips

instance_group:
	@terraform output -json --state=terraform/terraform.tfstate | jq -r '.group_name.value'
.PHONY: instance_group

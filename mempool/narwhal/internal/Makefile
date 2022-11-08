#!/usr/bin/make -f

# Makefile targets for setting up dependencies/tooling for executing tests
# for narwhal/tendermint integrations.define

OS := $(shell uname)
TS := $(shell date +"%m-%d_%T")
PWD := $(shell pwd)
REMOTE_HOME_DIR := /home/jwberged
TMP :=$(shell mktemp -d)
TESTNET_RUN_FILE ?= demo/testnet_configs/larger_nets/concur_01_size022.json
export DIR ?= demo/${TS}
export TESTNET_CONFIG_FILE ?= $(DIR)/testnet_config.json
export DEPLOY_ARGS ?=
export DESTROY_ARGS ?=
export LOAD_ARGS ?= --concurrency 1 --txs 10000
export SETUP_DUR ?= 120
export WAIT_DUR ?= 30
export FOLLOW_DUR ?= 5m
FOLLOW_ARGS ?= --for $(FOLLOW_DUR)
export STAT_ARGS ?= --json
export METRIC_ARGS ?= --tm-metrics-port 26660 --narwhal-primary-metrics-port 54194 --narwhal-worker-metrics-port 54195
export SETUP_ARGS ?= --log-format "json" $(METRIC_ARGS)
SSH_ARGS :=-o ConnectTimeout=300 -o BatchMode=yes -o StrictHostKeyChecking=no -o UserKnownHostsFile=/dev/null
ZIP_EXCLUDE_ARGS :=-x "narwhal/nodes/*/dbs/*" -x "tendermint/nodes/*/data/*" -x "start.sh" -x "start_services.sh"

all: demo_confirmed
.PHONY: all

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
	cd terraform && terraform apply -auto-approve $(DEPLOY_ARGS)
.PHONY: deploy_gce_instances

destroy_gce_instances:
	cd terraform && terraform destroy $(DESTROY_ARGS)
.PHONY: destroy_gce_instances

destroy_gce_instances_confirmed: DESTROY_ARGS="-auto-approve"
destroy_gce_instances_confirmed: destroy_gce_instances
.PHONY: destroy_gce_instances_confirmed

setup_gce_instances: build_narwhalmint mk_test_dir
	@export ZIP_FILE="$$(MAKE instance_group).zip" ; \
	echo "creating $$ZIP_FILE for tm and narwhal configs" && \
	MAKE cluster_ipsets | \
        narwhalmint config-gen --output "$(TMP)" $(SETUP_ARGS) > $(TESTNET_CONFIG_FILE) && \
        cd $(TMP) && \
        zip -q -r "$$ZIP_FILE" ./* && \
        gsutil cp "$$ZIP_FILE"  "gs://narwhalmint/$$ZIP_FILE" && \
        rm -rf $(TMP) && \
        cd $(PWD) && \
    for ip in $$(MAKE cluster_ext_ips | jq -r '.[]'); do \
    	echo "\\nstarting $$ip TM and narwhal nodes..." && \
    	ssh -q $(SSH_ARGS) "jwberged@$$ip" "sudo bash /usr/local/lib/start.sh $$ZIP_FILE" ; \
    done
.PHONY: setup_gce_instances

run_new_instances: deploy_gce_instances
	@ echo "waiting $(SETUP_DUR)s for instances to ready" && \
		sleep $(SETUP_DUR) && \
 		MAKE start_observability && \
 		MAKE setup_gce_instances
.PHONY: run_new_instances

start_observability: stop_observability
start_observability: prom_config
	@docker compose up -d
.PHONY: start_observability

stop_observability:
	@docker compose down
.PHONY: stop_observability

prom_config:
	@MAKE instance_ext_ips | narwhalmint prom-config $(METRIC_ARGS) > demo/_observability/prom/prometheus.yml
.PHONY: prom_config

mk_test_dir:
	@echo "making test dir at $(DIR)" && mkdir -p $(DIR)
.PHONY: mk_test_dir

mk_observe_dir:
	@mkdir -p demo/_observability/grafana/datasource && mkdir -p demo/_observability/prom
.PHONY: mk_observe_dir

demo: run_new_instances
	echo "waiting $(WAIT_DUR)s for nodes to be ready" && \
	sleep $(WAIT_DUR) && \
	MAKE write_args_to_dir DIR:=$(DIR)  && \
	MAKE load-test LOAD_ARGS:="$(LOAD_ARGS) --chain-id $$(MAKE testnet_chain_id)" && \
	MAKE follow-nodes && \
	MAKE zip_logs DIR:=$(DIR) && \
	MAKE destroy_gce_instances
.PHONY: demo

demo_confirmed: DESTROY_ARGS=-auto-approve
demo_confirmed: demo
.PHONY: demo_confirmed

run_testnet_config:
	@export tmp=$$(cat $(TESTNET_RUN_FILE)) ; \
	echo "\\ntestnet config to be executed: $$(echo $$tmp | jq -c)\\n" && \
	MAKE demo_confirmed \
		LOAD_ARGS="$$(echo $$tmp | jq -r '.load_args')" \
		DEPLOY_ARGS="$$(echo $$tmp | jq -r '.deploy_args // ""')" \
		FOLLOW_DUR="$$(echo $$tmp | jq -r '.follow_dur // "10m"')"
.PHONY: run_testnet_config

load-test: build_narwhalmint
	@MAKE instance_ext_ips | narwhalmint load $(LOAD_ARGS)
.PHONY: load-test

follow-nodes: build_narwhalmint
	@MAKE instance_ext_ips | narwhalmint stats -f $(FOLLOW_ARGS)
.PHONY: follow-nodes

node-stats: build_narwhalmint
	@MAKE instance_ext_ips | narwhalmint stats $(STAT_ARGS)
.PHONY: node-stats

write_args_to_dir:
	@MAKE instance_ipset | \
		jq '{ "ipset": ., "load": "$(LOAD_ARGS)", "deploy": "$(DEPLOY_ARGS)", "follow": "$(FOLLOW_ARGS)" }' > $(DIR)/testrun_args.json
.PHONY: write_args_to_dir

upload_instance_files:
	gsutil cp terraform/start.sh gs://narwhalmint/start.sh
	gsutil cp terraform/start_services.sh gs://narwhalmint/start_services.sh
	gsutil cp terraform/start_seeds.sh gs://narwhalmint/start_seeds.sh
.PHONY: upload_instance_files

kill_validator_processes:
	@ for ip in $$(MAKE cluster_ext_ips | jq -r '.[]') ; do \
		ssh -q $(SSH_ARGS) jwberged@$$ip "sudo pkill -9 tendermint && echo \"$$ip tendermint process terminated\" || true" ; \
		ssh -q $(SSH_ARGS) jwberged@$$ip "sudo pkill -9 narwhal_node && echo \"$$ip narwhal_node processes terminated\" || true" ; \
	done
.PHONY: kill_validator_processes

zip_logs: build_narwhalmint mk_test_dir
	@MAKE node-stats | jq > "$(DIR)/summary.json" && echo "added $(DIR)/summary.json successfully\\n" && \
	echo "killing validator processes" && MAKE kill_validator_processes && \
	echo "all processes terminated...\\n" && \
	for ipset in $$(MAKE cluster_ipsets | jq -c '.[]') ; do \
		export ip="$$(echo $$ipset | jq -r '.external_ip')" ; \
		echo "zipping config and logs for $$ip (perm denied issues expected)..." && \
		ssh -q $(SSH_ARGS) jwberged@$$ip \
			'IP=$$(hostname -i) rm -f $(REMOTE_HOME_DIR)/output.zip && cd /usr/local/lib && zip "$(REMOTE_HOME_DIR)/output.zip" -q -r . -x "python3.9/*" $(ZIP_EXCLUDE_ARGS) narwhal/*.json "narwhal/nodes/$$IP" "tendermint/nodes/$$IP"' ; \
		echo "copying output.zip from $$ip to $(DIR)/$$ip.zip" && \
		scp -q $(SSH_ARGS) jwberged@$$ip:'$(REMOTE_HOME_DIR)/output.zip' "$(DIR)/$$ip.zip" && \
		echo "unzipping $(DIR)/$$ip.zip in $(DIR)" && \
		unzip -q -n -d $(DIR) $(DIR)/$$ip.zip && rm $(DIR)/$$ip.zip ; \
		export log_dir="$(DIR)/tendermint/nodes/$$(echo $$ipset | jq -r '.internal_ip')/logs" ; \
		echo "creating trimmed consensus and mempool logs file at $$log_dir" && \
		cat "$$log_dir/log.jsonl" | \
		 jq -c '. | select(.module == "consensus" or .module == "mempool") | select(._msg != "Receive")' > \
			"$$log_dir/logs_consensuspool.jsonl" ; \
		echo "$$ip node zipped successfully\\n" ; \
	done && \
	export final_dir="$(DIR).$$(make instance_group).height_$$(cat $(DIR)/summary.json | jq -r  '.node_status[0].status.sync_info.latest_block_height ?')" ; \
	mv $(DIR) $$final_dir && echo "all unzipped data located at $$final_dir"
.PHONY: zip_logs

testnet_chain_id:
	@cat $(TESTNET_CONFIG_FILE) | jq -r '.chain_id'
.PHONY: testnet_chain_id

instance_ipset:
	@gcloud compute instances list --format "json" --filter "metadata.items: *$$(MAKE instance_group)" | \
		jq '[.[].networkInterfaces[] | { "internal_ip": .networkIP, "external_ip": .accessConfigs[0].natIP, "node_type": "validator"}]'
.PHONY: instance_ipset

instance_ext_ips:
	@MAKE instance_ipset | jq 'map(.external_ip)'
.PHONY: instance_ext_ips

seed_ipset:
	@gcloud compute instances list --format "json" --filter "metadata.items: *$$(MAKE seed_group)" | \
		jq -c '[.[].networkInterfaces[] | { "internal_ip": .networkIP, "external_ip": .accessConfigs[0].natIP, "node_type": "seed"}]'
.PHONY: seed_ips

cluster_ipsets:
	@MAKE seed_ipset| jq --argjson inst "$$(MAKE instance_ipset)" '[$$inst[], .[]]'
.PHONY: cluster_ipsets

cluster_ext_ips:
	@MAKE cluster_ipsets | jq 'map(.external_ip)'
.PHONY: cluster_ext_ips

instance_metric_endpoints:
	@MAKE instance_ext_ips | jq -c 'map("http://" + .) | map(.+":26660")'
.PHONY: instance_metric_endpoints

instance_group:
	@terraform output -json --state=terraform/terraform.tfstate | jq -r '.group_name.value'
.PHONY: instance_group

seed_group:
	@terraform output -json --state=terraform/terraform.tfstate | jq -r '.seeds.value'
.PHONY:seed_group

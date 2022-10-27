#!/usr/bin/make -f

# Makefile targets for setting up dependencies/tooling for executing tests
# for narwhal/tendermint integrations.define

OS := $(shell uname)
TS := $(shell date +"%m-%d_%T")
PWD := $(shell pwd)
INFLUXDB :=influxdb
INFLUXDB_TOKEN ?="the-secret-auth-token"#don't actually care about it being secure....
TMP :=$(shell mktemp -d)
TESTNET_CONFIGURATIONS_FILE="demo/steps.jsonl"
TESTNET_CONFIG_DIR ?= demo/testnet_configs
export TESTNET_ID ?=$(shell echo "$$RANDOM")
export DIR ?= demo/${TS}
export DEPLOY_ARGS ?=
export DESTROY_ARGS ?=
export LOAD_ARGS ?= --concurrency 1 --txs 10000
export SETUP_DIR ?= 60
export WAIT_DUR ?= 30
export FOLLOW_DUR ?= 10m
FOLLOW_ARGS ?= --for $(FOLLOW_DUR)
export STAT_ARGS ?= --json
export SETUP_ARGS ?= --log-format "json" --log-level "debug" --metrics-port 26660

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

setup_gce_instances: build_narwhalmint
	@export ZIP_FILE="$$(MAKE instance_group).zip" ; \
	echo "creating $$ZIP_FILE for tm and narwhal configs" && \
	MAKE instance_ipset | \
        narwhalmint config-gen --output "$(TMP)" $(SETUP_ARGS) && \
        cd $(TMP) && \
        zip -q -r "$$ZIP_FILE" ./* && \
        gsutil cp "$$ZIP_FILE"  "gs://narwhalmint/$$ZIP_FILE" && \
        rm -rf $(TMP) && \
        cd $(PWD) && \
    for ip in $$(MAKE instance_ext_ips | jq -r '.[]'); do \
    	echo "\\nstarting $$ip TM and narwhal nodes..." && \
    	ssh -q -o StrictHostKeyChecking=no -o UserKnownHostsFile=/dev/null "jwberged@$$ip" "sudo bash /usr/local/lib/start.sh $$ZIP_FILE" ; \
    done
.PHONY: setup_gce_instances

run_new_instances: deploy_gce_instances
	sleep $(SETUP_DIR) && MAKE setup_gce_instances
.PHONY: run_new_instances

start_influxdb: stop_influxdb mk_observe_dir
	@docker run -d --name $(INFLUXDB) -p 8086:8086 \
	   -v $(PWD)/demo/_observability/data:/var/lib/influxdb2 \
	   -v $(PWD)/demo/_observability/config:/etc/influxdb2 \
	   -e DOCKER_INFLUXDB_INIT_MODE=setup \
	   -e DOCKER_INFLUXDB_INIT_USERNAME=narwhalmint \
	   -e DOCKER_INFLUXDB_INIT_PASSWORD=password123 \
	   -e DOCKER_INFLUXDB_INIT_ORG=peggy \
	   -e DOCKER_INFLUXDB_INIT_BUCKET=tm \
	   -e DOCKER_INFLUXDB_INIT_ADMIN_TOKEN=$(INFLUXDB_TOKEN) \
	   influxdb:2.4-alpine
.PHONY: start_influxdb

stop_influxdb:
	@docker stop influxdb && docker rm -f influxdb
.PHONY: stop_influxdb

start_telegraf: stop_telegraf telegraf_config
	@docker run -d --name telegraf \
		--net=container:$(INFLUXDB) \
		-e INFLUXDB_TOKEN=$(INFLUXDB_TOKEN) \
		-v $(PWD)/demo/_observability/telegraf.conf:/etc/telegraf/telegraf.conf:ro \
		telegraf:1.24
.PHONY: start_telegraf

stop_telegraf:
	@docker stop telegraf && docker rm -f telegraf || true
.PHONY: stop_telegraf

telegraf_config: mk_observe_dir build_narwhalmint
	@MAKE instance_ext_ips | \
		narwhalmint telegraf-config --metrics-port 26660 --testnet-id $(TESTNET_ID) > demo/_observability/telegraf.conf
.PHONY: telegraf_config

mk_observe_dir:
	@mkdir -p demo/_observability
.PHONY: mk_observe_dir

demo: run_new_instances start_influxdb
	MAKE start_telegraf && \
	echo "waiting $(WAIT_DUR)s for nodes to be ready" && \
	sleep $(WAIT_DUR) && \
	mkdir -p $(DIR) && \
	cp demo/_observability/telegraf.conf $(DIR)/telegraf.conf && \
	MAKE write_args_to_dir && \
	MAKE load-test && \
	MAKE follow-nodes && \
	MAKE zip_logs && \
	MAKE destroy_gce_instances
.PHONY: demo

write_args_to_dir:
	@MAKE instance_ipset | \
		jq '{ "ipset": ., "load": "$(LOAD_ARGS)", "deploy": "$(DEPLOY_ARGS)", "follow": "$(FOLLOW_ARGS)", "testnet_id": "$(TESTNET_ID)" }' > $(DIR)/testrun_args.json
.PHONY: write_args_to_dir

demo_confirmed: DESTROY_ARGS=-auto-approve
demo_confirmed: demo
.PHONY: demo_confirmed

run_testnet_configs:
	@for filename in $$(ls $(TESTNET_CONFIG_DIR)) ; do \
		export tmp=$$(cat $(TESTNET_CONFIG_DIR)/$$filename) ; \
		echo "\\ntestnet config to be executed: $$(echo $$tmp | jq -c)\\n" && \
		MAKE demo_confirmed \
			LOAD_ARGS="$$(echo $$tmp | jq -r '.load_args')" \
			DEPLOY_ARGS="$$(echo $$tmp | jq -r '.deploy_args // ""')" \
			FOLLOW_DUR="$$(echo $$tmp | jq -r '.follow_dur // "10m"')" ; \
	done
.PHONY: run_testnet_configs

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
	export final_dir="$(DIR).$$(make instance_group).height_$$(cat $(DIR)/summary.json | jq -r  '.node_status[0].status.sync_info.latest_block_height ?')" ; \
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

instance_metric_endpoints:
	@MAKE instance_ext_ips | jq -c 'map("http://" + .) | map(.+":26660")'
.PHONY: instance_metric_endpoints

instance_group:
	@terraform output -json --state=terraform/terraform.tfstate | jq -r '.group_name.value'
.PHONY: instance_group

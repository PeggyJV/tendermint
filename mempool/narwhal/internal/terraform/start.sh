#!/usr/bin/env bash
set -e

cd /usr/local/lib

if [ ! -d "tendermint" ]; then
	gsutil cp "gs://narwhalmint/$1" config.zip
	apt-get -qq install unzip && unzip -q config.zip && rm config.zip
fi

source start_services.sh
start_tendermint && start_narwhal_primary && start_narwhal_worker

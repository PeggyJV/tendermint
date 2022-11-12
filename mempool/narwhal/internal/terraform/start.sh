#!/usr/bin/env bash
set -e

cd /usr/local/lib

if [ ! -d "tendermint" ]; then
	gsutil cp "gs://narwhalmint/$1" config.zip
	apt-get -qq install unzip && unzip -q config.zip && rm config.zip
else
	printf "using existing configs for tendermint and narwhal\\n"
fi

source start_services.sh && start

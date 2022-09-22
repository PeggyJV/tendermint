#!/usr/bin/env bash

set -euo pipefail

export GROUP_NAME=$(terraform output -json | jq -r .group_name.value)
export INSTANCES_JSON=$(gcloud compute instances list --format "json" --filter "metadata.items: *$GROUP_NAME" | \
 													jq '[.[].networkInterfaces[] | { "internal_ip": .networkIP, "external_ip": .accessConfigs[0].natIP}]')
export TMP=$(mktemp -d)
export ZIP_FILE="$GROUP_NAME.zip"

echo $INSTANCES_JSON | \
    narwhalmint config-gen --output "$TMP" && \
    cd $TMP && \
    zip -q -r "$ZIP_FILE" ./* && \
    gsutil cp "$ZIP_FILE"  "gs://narwhalmint/$ZIP_FILE" && \
    rm -rf $TMP

for ip in $(echo $INSTANCES_JSON | jq -r '.[].external_ip'); do
	echo "starting $ip TM and narwhal nodes..."
	ssh -q -o StrictHostKeyChecking=no -o UserKnownHostsFile=/dev/null "jwberged@$ip" "sudo bash /usr/local/lib/start.sh $ZIP_FILE"
done

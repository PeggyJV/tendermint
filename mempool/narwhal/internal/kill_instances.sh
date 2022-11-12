#!/bin/bash

for ip in $(cat /dev/stdin | jq -r '.[]'); do
	ssh -q -o ConnectTimeout=300 -o BatchMode=yes -o StrictHostKeyChecking=no -o UserKnownHostsFile=/dev/null \
			jwberged@$ip "sudo pkill -9 tendermint && echo \"$ip tendermint process terminated\" || true" &
  ssh -q -o ConnectTimeout=300 -o BatchMode=yes -o StrictHostKeyChecking=no -o UserKnownHostsFile=/dev/null \
			jwberged@$ip "sudo pkill -9 narwhal_node && echo \"$ip narwhal_node processes terminated\" || true"&
done

wait



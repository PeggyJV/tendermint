#!/bin/bash

for ip in $(cat /dev/stdin | jq -r '.[]'); do
	echo "starting $ip TM and narwhal nodes..."
   	ssh -q -o ConnectTimeout=300 -o BatchMode=yes -o StrictHostKeyChecking=no -o UserKnownHostsFile=/dev/null "jwberged@$ip" "sudo bash /usr/local/lib/start.sh $ZIP"  &
done

wait

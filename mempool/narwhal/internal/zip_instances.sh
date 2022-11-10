#!/usr/bin/env bash

export ipsets=$(cat /dev/stdin)

for ipset in $(echo $ipsets | jq -c '.[]' ); do
	(
		export ip="$(echo $ipset | jq -r '.external_ip')"
		echo "zipping config and logs for $ip (perm denied issues expected)..."
		ssh -q -o ConnectTimeout=300 -o BatchMode=yes -o StrictHostKeyChecking=no -o UserKnownHostsFile=/dev/null jwberged@$ip \
			"IP=\$(hostname -i) rm -f $REMOTE_DIR/output.zip && cd /usr/local/lib && zip $REMOTE_DIR/output.zip -q -r . -x 'python*/*' -x 'narwhal/nodes/*/dbs/*' -x 'tendermint/nodes/*/data/*' -x start.sh -x start_services.sh narwhal/*.json narwhal/nodes/\$IP tendermint/nodes/\$IP"
		echo "copying output.zip from $ip to $DIR/$ip.zip"
		scp -q -o ConnectTimeout=300 -o BatchMode=yes -o StrictHostKeyChecking=no -o UserKnownHostsFile=/dev/null jwberged@"$ip:$REMOTE_DIR/output.zip" "$DIR/$ip.zip" &&
		echo "copied output.zip to $DIR/$ip.zip successfully"
	) &
done

wait

for ipset in $(echo $ipsets | jq -c '.[]'); do
	export ip="$(echo $ipset | jq -r '.external_ip')"
	echo "unzipping $DIR/$ip.zip in $DIR"
	unzip -q -n -d $DIR $DIR/$ip.zip && rm $DIR/$ip.zip
	export log_dir="$DIR/tendermint/nodes/$(echo $ipset | jq -r '.internal_ip')/logs"
	echo "creating trimmed consensus and mempool logs file at $log_dir"
	cat "$log_dir/log.jsonl" |
			jq -c '. | select(.module == "consensus" or .module == "mempool") | select(._msg != "Receive")' > "$log_dir/logs_consensuspool.jsonl"
	echo "$ip node unzipped successfully\n"
done

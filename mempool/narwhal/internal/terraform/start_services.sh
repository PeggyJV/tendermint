function start_tendermint() {
	set -o errtrace

	[ $(pidof tendermint | wc -w) == 1 ] && echo "aborting starting tendermint: already running..." && return;

	local IP="$(hostname -i)"
	local TMHOME="/usr/local/lib/tendermint/nodes/$IP"
	local TMLOGFILE="$TMHOME/logs/log.jsonl"
  touch $TMLOGFILE

  #  ___                ___
  # (o o)    Start     (o o)
  #(  V  ) TENDERMINT (  V  )
  #--m-m----------------m-m--
  tendermint start \
  	--home $TMHOME \
  	--p2p.laddr="tcp://$IP:26656" \
  	--rpc.laddr="tcp://$IP:26657" \
  	--proxy_app persistent_kvstore >> $TMLOGFILE 2>&1 &
  echo "tendermint running.... logfile at $TMLOGFILE"
}

function start_narwhal_primary() {
		set -o errtrace

	[ "$(ps aux | grep -c -e 'narwhal_node.*--consensus-disabled')" == 2 ] && echo "aborting narwhal primary: already running..." && return;

	local NARNODE="/usr/local/lib/narwhal/nodes/$(hostname -i)"
	local NAR_PRIM_LOGFILE="$NARNODE/logs/primary"
	touch $NAR_PRIM_LOGFILE

	#( \   Start   / )
	# ( ) Narwhal ( )
	#  (/ Primary \)
	#   (.·´¯`·.¸¸)
	LOG_LVL="-vv" start_narwhal_node --store "$NARNODE/dbs/primary" primary --consensus-disabled >> $NAR_PRIM_LOGFILE 2>&1 &
  echo "narwhal primary node running... logfile at $NAR_PRIM_LOGFILE"
}

function start_narwhal_worker() {
	set -o errtrace

	[ "$(ps aux | grep -c -e 'narwhal_node.*--id 0')" == 2 ] && echo "aborting narwhal worker: already running..." && return;

	local NARNODE="/usr/local/lib/narwhal/nodes/$(hostname -i)"
	local NAR_WORKER_LOGFILE="$NARNODE/logs/worker"
  touch $NAR_WORKER_LOGFILE

	#    #( \   Start   / )
	#    # ( ) Narwhal ( )
	#    #  (/ Worker  \)
	#    #   (.·´¯`·.¸¸)
  LOG_LVL="-vvv" start_narwhal_node --store "$NARNODE/dbs/worker" worker --id "0" >> $NAR_WORKER_LOGFILE 2>&1 &
	echo "narwhal worker node running... logfile at $NAR_WORKER_LOGFILE"
}

function start_narwhal_node() {
	set -o errtrace

	local NARHOME="/usr/local/lib/narwhal"
	local NARNODE_HOME="$NARHOME/nodes/$(hostname -i)"

	MYSTEN_DB_WAL_SIZE_MB=512 MYSTEN_DB_WRITE_BUFFER_SIZE_MB=256 narwhal_node $LOG_LVL \
		run \
			--primary-keys "$NARNODE_HOME/key.json" \
			--primary-network-keys "$NARNODE_HOME/primary_network_key.json" \
			--committee "$NARHOME/primaries.json" \
			--workers "$NARHOME/workers.json" \
			--worker-keys "$NARNODE_HOME/worker_0_network_key.json" \
			--parameters "$NARNODE_HOME/parameters.json" \
		"$@"
}

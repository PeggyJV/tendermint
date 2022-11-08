function start() {
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
		--p2p.seed_mode=true \
		--p2p.laddr="tcp://$IP:26656" \
		--rpc.laddr="tcp://$IP:26657" \
		--proxy_app persistent_kvstore >> $TMLOGFILE 2>&1 &
  echo "tendermint seed running.... logfile at $TMLOGFILE"
}

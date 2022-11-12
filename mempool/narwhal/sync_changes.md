# Block Sync & Narwhal: future of TM sync

With the introduction of Narwhal mempool into the TM project, we're introducing a new mechanism
for consensus. This new mechanism separates the part set that is communicated for consensus,
henceforth known as the consensus partset, and a full parset. The consensus partset is communicated
between peers. When the consensus partset is completed, the validator will then ask the block executor
to provide them the full block, which is used during commit. Now that we have a break in the partsets,
a consensus and full partset, we have to address the fallout when a validator is trying to sync up
with the current state of the world. This sync state falls into one of three categories:

* catching up from missing a handful of blocks, referred too as *catch up* sync henceforth
* established validator catching up from a larger block gap, referred to as *fast sync*
* validator catching up from genesis to latest block on an established chain, referred to as *state sync* (snapshot)

We will take a look at these closer and what new concerns Narwhal brings to the table as well as
establish how we can reuse the existing block sync mechanisms with the advent of Narwhal.

## Catch Up

When a validator is in catch up, they will receive updates via the consensus reactor when the
block store has a block available that a peer does not have. Currently, we read from the block
store for the next block height the peer is missing, and effectively push that block to the peer.
Atm, that code can be found [here][consensus-reactor-blockstore] in the consensus reactor. A
snippet from the excerpt below:


<details>
<summary>Code snippet consensus/reactor.go:519-539</summary>

```go
func (conR *Reactor) gossipDataRoutine(peer p2p.Peer, ps *PeerState) {
	logger := conR.Logger.With("peer", peer)

OUTER_LOOP:
	for {
	    // ...TRIM

		rs := conR.conS.GetRoundState()
		prs := ps.GetRoundState()

		// ...TRIM

		// If the peer is on a previous height that we have, help catch up.
		blockStoreBase := conR.conS.blockStore.Base()
		if blockStoreBase > 0 && 0 < prs.Height && prs.Height < rs.Height && prs.Height >= blockStoreBase {
			heightLogger := logger.With("height", prs.Height)

			// if we never received the commit message from the peer, the block parts wont be initialized
			if prs.ProposalBlockParts == nil {
				blockMeta := conR.conS.blockStore.LoadBlockMeta(prs.Height)
				if blockMeta == nil {
					heightLogger.Error("Failed to load block meta",
						"blockstoreBase", blockStoreBase, "blockstoreHeight", conR.conS.blockStore.Height())
					time.Sleep(conR.conS.config.PeerGossipSleepDuration)
				} else {
					ps.InitProposalBlockParts(blockMeta.BlockID.PartSetHeader)
				}
				// continue the loop since prs is a copy and not effected by this initialization
				continue OUTER_LOOP
			}
			conR.gossipDataForCatchup(heightLogger, rs, prs, ps, peer)
			continue OUTER_LOOP
		}

		// ...TRIM
    }
}
```
</details>

This works fine when validators were catching up with only the single part set. However, when
we start using consensus part set to help a validator catch up, we need to send the consensus
part set as that is what is communicating the proposed block. Here lies the rub, we'll need
to be able to pull the block from the meta store, determine which strategy to use for transmitting
the part set. Since we pull the block meta, always, we can leverage this and add the consensus
part set directly into the block meta when we're communicating for narwhal. When we are not using
narwhal, then we do not have to do this.

For _Catch Up_ the following is proposed:

1. make the negotiation of block part sets configurable
    * *note* - when in narwhal, we use 2 part set, and when in cList use the preexisting code paths
2. Both state and reactor agree on the configuration and adapt their code flow accordingly
3. When consensus state is configured for narwhal
    1. add the consensus part set header to the block meta in block store on save
        * Either this or we save the consensus block parts in addition to the full block parts and read that
    2. the consensus state speaks narwhal to obtain the header, evidence and last commit
        * when consensus part set received is complete, hydrated block data and create full block part set from it
        * *note* - do we need to have 2 part sets? perhaps we don't need the part set after hydrating the block now since we put the kibash on it in SetProposal
4. When consensus reactor is configured for narwhal
    1. should send peers the consensus part set when peer is on same round
    2. should hydrate the consensus part set for _Catch Up_ and allow lagging peers to catch up via their local narwhal
    * *note* - if the reason the node is lagging is b/c narwhal has become byzantine or the narwhal data sets have been removed since this block was communicated to the validator who has fallen behind, this will not work

## Fast Sync

When a validator is in Fast Sync mode, they are pulling down a larger bit of data to get their node
up to the current height of the chain. Either the snapshots from [State Sync](#state-sync) have been
exhausted and the remaining gap can be filled by Fast Sync, or [State Sync](#state-sync) was disabled
in the validator's configuration. The existing [blockchain/v0][blockchain-v0-impl] implementation does
not use the same consensus sync mechanism. It has its own reactor that manages the sharing of the blocks
between peers. When we are in narwhal mode, one thing that is missing from the blockchain/v0 fast sync,
is saving the consensus partset as part of the block meta, so we can effectively rehydrate for other
peers as they switch over to consensus to [Catch Up](#catch-up).

For _Fast Sync_ the following is proposed:

1. make the saving of consensus partset in block meta be configurable
2. When in blockchain/v0/reactor is configured for narwhal
    1. the consensus part set is added to the block meta in block store on save

Note: the existing workflow for applying a new block remains the same. When we call
[blockExecutor.ApplyBlock(L436)](../../blockchain/v0/reactor.go), we will take advantage
of the existing workflow for the mempool. Narwhal nodes will attempt to delete any
collections within the block header, in the same way the normal consensus workflow does.
The consensus partset in the block meta is for nodes who are using consensus to catch up.
That is necessary then.

## State Sync

When a validator is in State Sync mode, the validator is catching up to latest from a large gap in height. A
prime example of this is when a new validator joins the network and has to catch up. The new validator may
be configured to take advantage of State Sync, which uses snapshots to catch up to the point where [Fast
Sync](#fast-sync) may take over. This take over happens after the final snap shot. The [Fast Sync](#fast-sync)
mechanism will catch up the validator until it reaches a point where it can use the consensus
[catch up](#catch-up) mechanism. When the validator is configured for narwhal, State Sync operates as usual.

1. profit...


<!-- identifiers below -->

[consensus-reactor-blockstore]: https://github.com/PeggyJV/tendermint/blob/main/consensus/reactor.go#L519-L539
[blockchain-v0-impl]: https://github.com/PeggyJV/tendermint/tree/main/blockchain/v0

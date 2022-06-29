# Understanding v0.34.0's Existing Consensus Code and Narwhal

## Getting TXs into the mempool

When a transaction (TX) is submitted, it hits the existing mempool and runs the [Mempool.CheckTx](https://github.com/PeggyJV/tendermint/blob/v0.34.16/mempool/clist_mempool.go#L234)
method for the TX.  When a TX passes CheckTx, it is then added to the existing mem pool.

    * this workflow here likely remains the same with Narwhal as we don't want nonsense TXs
      in the narwhal DAG/cluster

The mempool's [CheckTx](https://github.com/PeggyJV/tendermint/blob/v0.34.16/mempool/clist_mempool.go#L234)
will check for basic stuff, like size of the tx being under the max allowed.

    * This remains the same I imagine

Additionally, the mempool has to check if it is full (its resident in the same process as TM)
and writes to the WAL to make sure the TX isn't lost on restart. Lastly, the app is checked
against.

    * All the memory capacity check and WAL stuff wil be offloaded to narwhal
    * we still make the call to the app

## Consensus

On [startup](https://github.com/PeggyJV/tendermint/blob/v0.34.16/consensus/state.go#L299),
State initiates the *State.receiveRoutine which fires up the listeners for the different
msg channels (timeout, internal msg, peer msg).

    * peer msgs are as they sound, from peers and initiate a state change
    * internal msg queue is the same node sending msgs to itself for, you guessed it, a state change
    * timeout ticker is set based on the interval for a round

*Note*: the State type starts up the shared event bus

The receiveRoutine method will listen for different events from each queueu. When a peer
sends a msg, it is processed as either a proposal, block part or a vote. This may in turn
generate additional internal events.

### [Proposal Msg](https://github.com/PeggyJV/tendermint/blob/v0.34.16/consensus/state.go#L814)

When a peer or internal msg is a proposal, then we validate the proposal is within the proof of
lock (POL) range. That range is either -1 or in range [0, proposal.Round). Then we obtain the
proposer's public key and validate the proposal's signature. They should match. Lastly, if the
ProposalBlockParts are not set, we create a new PartSet from the proposal's BlockID.PartSetHeader.

*Note*: all proposals are selected deterministically and knowledge of the proposer is shared
        amongst all validators.

* *TODO*: what is happening with assigning the [proposal.Signature to the protos signature](https://github.com/PeggyJV/tendermint/blob/v0.34.16/consensus/state.go#L1818)?
* *TODO*: dig in more on the proposal block bits. Is this a race condition?

### [Block Part Message](https://github.com/PeggyJV/tendermint/blob/v0.34.16/consensus/state.go#L819)

When a peer or internal msg is a block part message, then we verify its contents. First the
block height of the block parts is compared to the current block height, they must match. Then
the check for the State.ProposalBlockParts being nil is checked. This indicates we received an
unexpected block part.

Next we attempt to att the block part message's block part to the [State.ProposalBlockParts](https://github.com/PeggyJV/tendermint/blob/v0.34.16/consensus/state.go#L1857)
type. The part to be added is verified to be [within the PartSet](https://github.com/PeggyJV/tendermint/blob/v0.34.16/consensus/state.go#L1835-L1856)
and doesn't exist in the part set yet. Then the part is attempted to be added to the part set.
The [part set proof is used to verify](https://github.com/PeggyJV/tendermint/blob/v0.34.16/types/part_set.go#L284)
the part's bytes are valid for the part set. If all passes the part is added to the part set.

If the part was added to the block part set, and the [block part set is complete](https://github.com/PeggyJV/tendermint/blob/v0.34.16/consensus/state.go#L1866)
then we publish a CompleteProposal event to the event bus. Next we determine if the
round has [prevotes for the 2/3 majority](https://github.com/PeggyJV/tendermint/blob/v0.34.16/consensus/state.go#L1895).
If the majority has been satisfied and the State.ValidRound is before the State.Round, then
we check to see if the proposal block hashes to the blockID from the prevotes 2/3 majority.
If so, then we update the State.ValidRound, State.ValidBlock, and State.ValidBlockParts to
the current rounds matching pieces.

    * This is where the coupling between the different stages of concensus start to make
      things difficult for the narwhal integration. Its not immeidately obvious where to
      make the surgical cuts to rip out the prevote block parts with all the TXs and replace
      it with narwhal's collection bits.
    * At first glance it looks like our State needs to have the existing Proposal and a new
      type that is for pre-vote/pre-commit that makes use of the collections. The existing
      proposal would be used to transmit the actual TXs as part of the



<details>
<summary>State's Current Step is RoundStepPropose or earlier and proposal is complete</summary>

Lastly, we check the [State's current Step](https://github.com/PeggyJV/tendermint/blob/v0.34.16/consensus/state.go#L1914).
If the current step is either the Propose Step or any step before it ([RoundStepNewHeight or RoundStepNewRound](https://github.com/PeggyJV/tendermint/blob/v0.34.16/consensus/types/round_state.go#L20-L22)),
and the proposal is complete, then we enter PreVote.

[PreVote](https://github.com/PeggyJV/tendermint/blob/v0.34.16/consensus/state.go#L1210)
validates the height being voted on is the current height, or the block part's round is before
the current round, or the round is sound but the step has exceeded the Prevote step. If any of
these conditions are met we exit early b/c the pre vote was given invalid args. Next we Do the
vote. This uses the [defaultDoPrevote](https://github.com/PeggyJV/tendermint/blob/v0.34.16/consensus/state.go#L1236),
which first checks if we have a locked block, and if we do add a vote for the locked block.

*Note*: The locked block is set during the preCommit stage.

Then it checks if the State.ProposalBlock is nil, if so, we prevote nil. Then we validate the
block itself. Lastly, sign and add vote for the block as accepted. After prevote has completed,
we update the RoundStep to the block's round, and publish the newStep event to the event bus.

If the round has two thirds majority prevotes, then we enter [Precommit](https://github.com/PeggyJV/tendermint/blob/v0.34.16/consensus/state.go#L1306).
As with Prevote, the height, round, and step are all validated to match the expected values for
precommit. Then the prevotes 2/3 majority for the block's round is queried. If we don't have
2/3 prevotes, we then precommit nil for the block. If we do have 2/3 majority, then we publish
the EventPolka event to the event bus. Next we check to make sure the POL round matches the
block's round.

Once the metadata is agreed upon, and we have the correct [POL round](https://github.com/PeggyJV/tendermint/blob/v0.34.16/consensus/state.go#L1346)
and 2/3 majority, we then check the blockID.Hash value. If it is empty, and we have a
LockedBlock, then we unlock the block and publish the Unlock event to the eventbus.
Regardless the LockedBlock situation, we precommit nil for the block.

Now at [this point](https://github.com/PeggyJV/tendermint/blob/v0.34.16/consensus/state.go#L1373),
we have 2/3 majority for a valid block. We the State.LockedBlock hashes to the block hash,
then we set the State.LockedRound to the block's current round and publish a Relock
event to the event bus. Lastly, we precommit the block accepted. This basically meant we
locked on that block previously, but did not precommit it.

      * want to better understand why/how this LockedBlock was set but a precommit for it did not?
      * perhaps my understanding is off for htis workflow, does it precommit twice in the LockedBlock instance?

If the [State.ProposalBlock hashes to the block hash](https://github.com/PeggyJV/tendermint/blob/v0.34.16/consensus/state.go#L1386),
then we lock this block and round. Then we publish a Lock event to the eventbus and
precommit the block.

If the LockedBlock and ProposalBlock do not match up to the current block, and we have a polka
in htis round. Then there was a polka in this round for a block we don't have, then we fetch
that block, [unlock the LockedBlock](https://github.com/PeggyJV/tendermint/blob/v0.34.16/consensus/state.go#L1411)
(setting them to nil values...). Additionally, we check the State.ProposalBlockParts have the
part set headers matching the block. If it does not agree, then we also and set the
State.ProposalBlock, publishes an Unlock event to the eventbus, and finally precommit nil for
the block.

</details>

<details>
<summary>State's Current step is RoundStepCommit</summary>

In this instance, we will attempt to [finalize the commit](https://github.com/PeggyJV/tendermint/blob/v0.34.16/consensus/state.go#L1523).
Finalizing the commit requires the State.Height to match the new block's height. Additionally,
we require 2/3 majority precommits to finalize the commit. If the block is valid, we
[add it to the blockstore](https://github.com/PeggyJV/tendermint/blob/v0.34.16/consensus/state.go#L1595).

Next we add the [EndHeightMessage to the WAL](https://github.com/PeggyJV/tendermint/blob/v0.34.16/consensus/state.go#L1618-L1619).
Notes from the code itself regarding fail states here:

      // Write EndHeightMessage{} for this height, implying that the blockstore
	  // has saved the block.
	  //
	  // If we crash before writing this EndHeightMessage{}, we will recover by
      // running ApplyBlock during the ABCI handshake when we restart.  If we
      // didn't save the block to the blockstore before writing
      // EndHeightMessage{}, we'd have to change WAL replay -- currently it
      // complains about replaying for heights where an #ENDHEIGHT entry already
      // exists.
      //
      // Either way, the State should not be resumed until we
      // successfully call ApplyBlock (ie. later here, or in Handshake after
      // restart).

Then we grab a copy of the state for staging and use that to
[apply the block via the block executor](https://github.com/PeggyJV/tendermint/blob/v0.34.16/consensus/state.go#L1638-L1645).
Once the block is applied, we then prune blocks per the ABCI app's directions. Then we
make sure to udpate the validator pub key (in case it changed). Lastly, we schedule the
next round. At this point, we have set the following:

* State.Height has been incremented to height+1
* State.Step is set to RoundStepNewHeight
* State.StartTime is set to when we will start the new round

</details>

### [Vote Message](https://github.com/PeggyJV/tendermint/blob/v0.34.16/consensus/state.go#L836)

On the event we receive a Vote Message, we [try adding the vote](https://github.com/PeggyJV/tendermint/blob/v0.34.16/consensus/state.go#L1932).
The first thing we check is to see if the vote is from a previous height. If it is from a
previous height and the vote is for precommit and the State.Step is RoundStepNewHeight, we
attempt to add the vote to the State.LastCommit. We then publish a Vote event to the
eventbus. Additionally, we add the event to the event cache. Lastly, if we can skip
the timeout commit nad have all votes, we then enter [round 0](https://github.com/PeggyJV/tendermint/blob/v0.34.16/consensus/state.go#L2014).

    * not sure why we hard code the round to 0 here for the next round

If the State.Height does not equal vote.Height, then we skip the voting.

#### [Vote has type Prevote](https://github.com/PeggyJV/tendermint/blob/v0.34.16/consensus/state.go#L2040)

If the vote type is [Prevote](https://github.com/PeggyJV/tendermint/blob/v0.34.16/consensus/state.go#L2040),
then we obtain the prevotes for the votes round from State.Votes.

<details>
<summary>We have +2/3 prevotes for a block or nil for _any_ round</summary>

We check the State.Locked* to match the vote. If State.LockedRound is less than the
vote round and the vote.Round is less than or equal to the State.Round and the LockedBlock
does not match the block with 2/3 majority, then we unlock and publish an Unlock event to
the event bus. We then check to see if the State.ValidRound is before the vote.Round and the
vote.Round equals the State.Round. If this is the case, then we have received a valid block.
If the ProposalBlock hashes to the block, then we update teh State.ValidRound, State.ValidBlock,
State.ValidBlockParts to the proposal's datum and the vote.Round. If the ProposalBlock does
not match the block, then we know we're getting the wrong block and set the ProposalBlock to
nil. Regardless the ProposalBlock matching or not, we publish a ValidBlock event to the event
bus and throw it into the event cache.
</details>

<details>
<summary>Check that we have +2/3 prevotes for _anything_ for future round</summary>

Case 1: If we have +2/3 prevotes for any and we are in a future round, then we skip the round if there
is a polka ahead of us.

Case 2: If the vote.Round is the current round, and we're at the Prevote Step or before then we
again check the prevotes have 2/3 majority. If we have 2/3 majority accepted and the proposal
is complete then we enter precommit (see above section for enterCommit) at the height and round.
If we don't have 2/3 majority accepted, and we do have 2/3 prevotes any, then we enter the
prevote wait step.

Case 3: If the State.Proposal.POLRound equals the vote.Round and the proposal is complete,
then we enter Prevote (see above section regarding enterPrevote).

</details>

#### [Vote has type Precommit](https://github.com/PeggyJV/tendermint/blob/v0.34.16/consensus/state.go#L2119)

If we have 2/3 majority for precommit, then we enter a new round and enter precommit (see
above section for enterPrecommit).

Case 1: If the 2/3 majority block has a hash, the we [enter commit](https://github.com/PeggyJV/tendermint/blob/v0.34.16/consensus/state.go#L1460).

The height and current step are first validated to make sure we're in the right place. Then we
take the block of the 2/3's majority. If the State.LockedBlock matches the block, then we set
the State.ProposalBlock* to the State.LockedBlock* entries. We then validate the
State.ProposalBlock matches the block and the State.ProposalBlockParts contain the block
header. If it does not contain the header, then we set the State.ProposalBlock back to nil
b/c we're getting the wrong block and publish a ValidBlock event to the event bus and event
cache.

    * what is a locked block for?

Last up in enterCommit, we update the round step, save the commit round and commit time, advance
to a new step and finally try finalizing the commit (see above for finalize commit).

Case 2: The block does not have a hash, then we enter precommitWait

In the event we do not have 2/3 majority accepted, but we do have 2/3 majority for any, and
the State.Round preceeds or equals the vote.Round then we enter a new round (vote.Round) and
enter precommit wait.

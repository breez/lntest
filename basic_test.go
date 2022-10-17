package lntest

import (
	"testing"
)

func TestOpenChannel(t *testing.T) {
	harness := NewTestHarness(t)
	defer harness.TearDown()
	miner := NewMiner(harness)
	alice := NewCoreLightningNode(harness, miner, "TODO", "Alice")
	bob := NewCoreLightningNode(harness, miner, "TODO", "Bob")

	alice.Fund(100000000)
	channelOptions := &OpenChannelOptions{
		AmountSat: 1000000,
	}
	channel := alice.OpenChannel(bob, channelOptions)
	channel.WaitForChannelReady()
}

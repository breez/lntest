package lntest

import (
	"log"
	"testing"
	"time"
)

func TestOpenChannel(t *testing.T) {
	deadline := time.Now().Add(time.Minute)
	harness := NewTestHarness(t, deadline)
	defer harness.TearDown()

	log.Print("Initializing miner")
	miner := NewMiner(harness)

	log.Print("Initializing Alice")
	alice := NewCoreLightningNode(harness, miner, "Alice")

	log.Print("Initializing Bob")
	bob := NewCoreLightningNode(harness, miner, "Bob")

	log.Print("Funding alice")
	alice.Fund(10000000)

	channelOptions := &OpenChannelOptions{
		AmountSat: 1000000,
	}

	log.Print("Opening channel")
	channel := alice.OpenChannel(bob, channelOptions)
	miner.MineBlocks(6)

	log.Print("Waiting for channel ready")
	channel.WaitForChannelReady()
}

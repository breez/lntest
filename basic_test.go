package lntest

import (
	"log"
	"testing"
	"time"
)

func TestOpenChannel(t *testing.T) {
	harness := NewTestHarness(t)
	defer harness.TearDown()
	timeout := time.Now().Add(time.Minute)
	log.Print("Initializing miner")
	miner := NewMiner(harness)

	log.Print("Initializing Alice")
	alice := NewCoreLightningNode(harness, miner, "Alice", timeout)

	log.Print("Initializing Bob")
	bob := NewCoreLightningNode(harness, miner, "Bob", timeout)

	log.Print("Funding alice")
	alice.Fund(10000000, timeout)

	channelOptions := &OpenChannelOptions{
		AmountSat: 1000000,
	}

	log.Print("Opening channel")
	channel := alice.OpenChannel(bob, channelOptions)
	miner.MineBlocks(6)

	log.Print("Waiting for channel ready")
	channel.WaitForChannelReady(timeout)
}

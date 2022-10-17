package lntest

import (
	"time"

	"github.com/niftynei/glightning/glightning"
	"golang.org/x/exp/slices"
)

const defaultTimeout int = 10

type ChannelInfo struct {
	From        *LightningNode
	To          *LightningNode
	FundingTx   string
	FundingTxId string
	ChannelId   string
}

func (c *ChannelInfo) WaitForChannelReady() {
	timeout := time.Now().Add(time.Duration(defaultTimeout) * time.Second)
	for {
		info, err := c.To.rpc.GetInfo()
		CheckError(c.To.harness.T, err)

		peer, err := c.From.rpc.GetPeer(info.Id)
		CheckError(c.From.harness.T, err)

		if peer.Channels == nil {
			c.From.harness.T.Fatal("no channels for peer")
		}

		channelIndex := slices.IndexFunc(
			peer.Channels,
			func(pc *glightning.PeerChannel) bool {
				return pc.ChannelId == c.ChannelId
			},
		)

		if channelIndex >= 0 {
			if peer.Channels[channelIndex].State == "CHANNELD_NORMAL" {
				return
			}
		}

		if time.Now().After(timeout) {
			c.From.harness.T.Fatal("timed out waiting for channel normal")
		}

		time.Sleep(50 * time.Millisecond)
	}
}

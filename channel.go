package lntest

import (
	"time"

	"github.com/breez/lntest/core_lightning"
	"golang.org/x/exp/slices"
)

type ChannelInfo struct {
	From        *CoreLightningNode
	To          *CoreLightningNode
	FundingTx   string
	FundingTxId string
	ChannelId   string
}

func (c *ChannelInfo) WaitForChannelReady(timeout time.Time) {
	for {
		info, err := c.To.rpc.Getinfo(c.From.harness.Ctx, &core_lightning.GetinfoRequest{})
		CheckError(c.To.harness.T, err)

		peers, err := c.From.rpc.ListPeers(c.From.harness.Ctx, &core_lightning.ListpeersRequest{
			Id: info.Id,
		})
		CheckError(c.From.harness.T, err)

		if len(peers.Peers) == 0 {
			c.From.harness.T.Fatalf("Peer %s not found", string(info.Id))
		}

		peer := peers.Peers[0]
		if peer.Channels == nil {
			c.From.harness.T.Fatal("no channels for peer")
		}

		channelIndex := slices.IndexFunc(
			peer.Channels,
			func(pc *core_lightning.ListpeersPeersChannels) bool {
				return string(pc.ChannelId) == c.ChannelId
			},
		)

		if channelIndex >= 0 {
			peerChannel := peer.Channels[channelIndex]
			if peerChannel.State == core_lightning.ListpeersPeersChannels_CHANNELD_NORMAL {
				channelsResp, err := c.From.rpc.ListChannels(c.From.harness.Ctx, &core_lightning.ListchannelsRequest{
					ShortChannelId: peerChannel.ShortChannelId,
				})
				CheckError(c.From.harness.T, err)

				// Wait for the channel to end up in the listchannels response.
				if len(channelsResp.Channels) > 0 &&
					channelsResp.Channels[0].Active {
					c.ChannelId = channelsResp.Channels[0].ShortChannelId
					return
				}
			}
		}

		if time.Now().After(timeout) {
			c.From.harness.T.Fatal("timed out waiting for channel normal")
		}

		time.Sleep(waitSleepInterval)
	}
}

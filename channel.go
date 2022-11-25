package lntest

import (
	"bytes"
	"time"
)

type ChannelInfo struct {
	From            LightningNode
	To              LightningNode
	FundingTxId     []byte
	FundingTxOutnum uint32
}

func (c *ChannelInfo) WaitForChannelReady(timeout time.Time) ShortChannelID {
	c.From.WaitForChannelReady(c, timeout)
	return c.To.WaitForChannelReady(c, timeout)
}

func (c *ChannelInfo) GetPeer(this LightningNode) LightningNode {
	if bytes.Equal(this.NodeId(), c.From.NodeId()) {
		return c.To
	} else {
		return c.From
	}
}

package lntest

import (
	"fmt"
	"strconv"
	"strings"
	"time"
)

type LightningNode interface {
	NodeId() []byte
	Host() string
	Port() uint32
	TearDown() error
	PrivateKey() []byte

	WaitForSync(timeout time.Time)
	Fund(amountSat uint64, timeout time.Time)
	ConnectPeer(peer LightningNode)
	OpenChannel(peer LightningNode, options *OpenChannelOptions) *ChannelInfo
	OpenChannelAndWait(
		peer LightningNode,
		options *OpenChannelOptions,
		timeout time.Time) (*ChannelInfo, ShortChannelID)
	WaitForChannelReady(channel *ChannelInfo, timeout time.Time) ShortChannelID
	CreateBolt11Invoice(options *CreateInvoiceOptions) *CreateInvoiceResult
	SignMessage(message []byte) []byte
	Pay(bolt11 string, timeout time.Time) *PayResult
	GetRoute(destination []byte, amountMsat uint64) *Route
	PayViaRoute(
		amountMsat uint64,
		paymentHash []byte,
		paymentSecret []byte,
		route *Route,
		timeout time.Time) *PayResult
	GetInvoice(paymentHash []byte) *GetInvoiceResponse
}

type OpenChannelOptions struct {
	AmountSat uint64
}

type CreateInvoiceOptions struct {
	AmountMsat  uint64
	Description *string
	Preimage    *[]byte
}

type CreateInvoiceResult struct {
	Bolt11        string
	PaymentHash   []byte
	PaymentSecret []byte
}

type PayResult struct {
	PaymentHash     []byte
	AmountMsat      uint64
	Destination     []byte
	AmountSentMsat  uint64
	PaymentPreimage []byte
}

type Route struct {
	Hops []*Hop
}

type Hop struct {
	Id         []byte
	Channel    ShortChannelID
	AmountMsat uint64
	Delay      uint16
}

type PayViaRouteResponse struct {
	PartId uint32
}

type WaitPaymentCompleteResponse struct {
	PaymentHash     []byte
	AmountMsat      uint64
	Destination     []byte
	CreatedAt       uint64
	AmountSentMsat  uint64
	PaymentPreimage []byte
}

type InvoiceStatus int32

const (
	Invoice_UNPAID  InvoiceStatus = 0
	Invoice_PAID    InvoiceStatus = 1
	Invoice_EXPIRED InvoiceStatus = 2
)

type GetInvoiceResponse struct {
	Exists             bool
	AmountMsat         uint64
	AmountReceivedMsat uint64
	Bolt11             *string
	Description        *string
	ExpiresAt          uint64
	PaidAt             *uint64
	PayerNote          *string
	PaymentHash        []byte
	PaymentPreimage    []byte
	IsPaid             bool
	IsExpired          bool
}

type ShortChannelID struct {
	BlockHeight uint32
	TxIndex     uint32
	OutputIndex uint16
}

func NewShortChanIDFromInt(chanID uint64) ShortChannelID {
	return ShortChannelID{
		BlockHeight: uint32(chanID >> 40),
		TxIndex:     uint32(chanID>>16) & 0xFFFFFF,
		OutputIndex: uint16(chanID),
	}
}

func NewShortChanIDFromString(chanID string) ShortChannelID {
	split := strings.Split(chanID, "x")
	bh, _ := strconv.ParseUint(split[0], 10, 32)
	ti, _ := strconv.ParseUint(split[1], 10, 32)
	oi, _ := strconv.ParseUint(split[2], 10, 16)

	return ShortChannelID{
		BlockHeight: uint32(bh),
		TxIndex:     uint32(ti),
		OutputIndex: uint16(oi),
	}
}

func (c ShortChannelID) ToUint64() uint64 {
	return ((uint64(c.BlockHeight) << 40) | (uint64(c.TxIndex) << 16) |
		(uint64(c.OutputIndex)))
}

func (c ShortChannelID) String() string {
	return fmt.Sprintf("%dx%dx%d", c.BlockHeight, c.TxIndex, c.OutputIndex)
}

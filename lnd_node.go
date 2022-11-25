package lntest

import (
	"bufio"
	"encoding/hex"
	"fmt"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"time"

	"github.com/breez/lntest/lnd"
	"golang.org/x/exp/slices"
	"google.golang.org/grpc"
)

type LndNode struct {
	name          string
	nodeId        []byte
	harness       *TestHarness
	miner         *Miner
	cmd           *exec.Cmd
	dir           string
	rpc           lnd.LightningClient
	host          string
	port          uint32
	grpcHost      string
	grpcPort      uint32
	adminMacaroon []byte
	tlsCert       []byte
}

func NewLndNode(h *TestHarness, m *Miner, name string, timeout time.Time, extraArgs ...string) *LndNode {
	lndDir := h.GetDirectory(fmt.Sprintf("lnd-%s", name))
	host := "localhost"
	port, err := GetPort()
	CheckError(h.T, err)

	grpcPort, err := GetPort()
	CheckError(h.T, err)

	binary, err := GetLndBinary()
	CheckError(h.T, err)

	grpcAddress := fmt.Sprintf("%s:%d", host, grpcPort)
	args := []string{
		fmt.Sprintf("--lnddir=%s", lndDir),
		"--debuglevel=debug",
		"--nobootstrap",
		fmt.Sprintf("--rpclisten=%s", grpcAddress),
		fmt.Sprintf("--listen=%s:%d", host, port),
		fmt.Sprintf("--trickledelay=%d", 50),
		"--keep-failed-payment-attempts",
		"--bitcoin.active",
		"--bitcoin.node=bitcoind",
		"--bitcoin.regtest",
		fmt.Sprintf("--bitcoind.rpcuser=%s", m.rpcUser),
		fmt.Sprintf("--bitcoind.rpcpass=%s", m.rpcPass),
		"--bitcoind.rpcpolling",
		"--bitcoind.blockpollinginterval=10ms",
		"--bitcoind.txpollinginterval=10ms",
		"--gossip.channel-update-interval=10ms",
		"--db.batch-commit-interval=10ms",
	}

	cmd := exec.CommandContext(h.Ctx, binary, append(args, extraArgs...)...)
	stderr, err := cmd.StderrPipe()
	CheckError(h.T, err)

	stdout, err := cmd.StdoutPipe()
	CheckError(h.T, err)

	log.Printf("%s: starting %s on port %d in dir %s...", name, binary, port, lndDir)
	err = cmd.Start()
	CheckError(h.T, err)

	go func() {
		// print any stderr output to the test log
		scanner := bufio.NewScanner(stderr)
		for scanner.Scan() {
			log.Println(name + ": " + scanner.Text())
		}
	}()

	go func() {
		// print any stderr output to the test log
		scanner := bufio.NewScanner(stdout)
		for scanner.Scan() {
			log.Println(name + ": " + scanner.Text())
		}
	}()

	go func() {
		err := cmd.Wait()
		if err != nil && err.Error() != "signal: interrupt" {
			log.Printf(name+": lnd exited with error %s", err)
		} else {
			log.Printf(name + ": process exited normally")
		}
	}()

	conn, err := grpc.Dial(grpcAddress)
	CheckError(h.T, err)

	client := lnd.NewLightningClient(conn)
	info, err := client.GetInfo(h.Ctx, &lnd.GetInfoRequest{})
	CheckError(h.T, err)

	log.Printf("%s: Has node id %s", name, info.IdentityPubkey)

	nodeId, err := hex.DecodeString(info.IdentityPubkey)
	CheckError(h.T, err)

	adminMacaroon, err := os.ReadFile(filepath.Join(lndDir, "data", "bitcoin", "regtest", "admin.macaroon"))
	CheckError(h.T, err)

	tlsCert, err := os.ReadFile(filepath.Join(lndDir, "tls.cert"))
	CheckError(h.T, err)

	node := &LndNode{
		name:          name,
		nodeId:        nodeId,
		harness:       h,
		miner:         m,
		cmd:           cmd,
		dir:           lndDir,
		rpc:           client,
		port:          port,
		host:          host,
		grpcHost:      host,
		grpcPort:      grpcPort,
		adminMacaroon: adminMacaroon,
		tlsCert:       tlsCert,
	}

	h.AddStoppable(node)
	h.RegisterLogfile(filepath.Join(lndDir, "logs", "bitcoin", "regtest", "lnd.log"), fmt.Sprintf("lnd-%s", name))

	return node
}

func (n *LndNode) NodeId() []byte {
	return n.nodeId
}

func (n *LndNode) Host() string {
	return n.host
}

func (n *LndNode) Port() uint32 {
	return n.port
}

func (n *LndNode) PrivateKey() []byte {
	return n.nodeId
}

func (n *LndNode) WaitForSync(timeout time.Time) {
	for {
		info, _ := n.rpc.GetInfo(n.harness.Ctx, &lnd.GetInfoRequest{})

		blockHeight := n.miner.GetBlockHeight()

		if info.BlockHeight >= blockHeight {
			log.Printf("node %s is synced to blockheight %d", n.name, blockHeight)
			break
		}

		log.Printf(
			"Waiting for node %s to sync. Actual block height: %d, node block height: %d",
			n.name,
			blockHeight,
			info.BlockHeight,
		)

		if time.Now().After(timeout) {
			n.harness.T.Fatal("timed out waiting for channel normal")
		}

		time.Sleep(waitSleepInterval)
	}
}

func (n *LndNode) Fund(amountSat uint64, timeout time.Time) {
	addrResponse, err := n.rpc.NewAddress(
		n.harness.Ctx,
		&lnd.NewAddressRequest{
			Type: lnd.AddressType_UNUSED_TAPROOT_PUBKEY,
		},
	)
	CheckError(n.harness.T, err)

	n.miner.SendToAddress(addrResponse.Address, amountSat)
	n.WaitForSync(timeout)
}

func (n *LndNode) ConnectPeer(peer LightningNode) {
	_, err := n.rpc.ConnectPeer(n.harness.Ctx, &lnd.ConnectPeerRequest{
		Addr: &lnd.LightningAddress{
			Pubkey: hex.EncodeToString(peer.NodeId()),
			Host:   fmt.Sprintf("%s:%d", peer.Host(), peer.Port()),
		},
	})
	CheckError(n.harness.T, err)
}

func (n *LndNode) OpenChannel(peer LightningNode, options *OpenChannelOptions) *ChannelInfo {
	n.ConnectPeer(peer)

	// open a channel
	fundResult, err := n.rpc.OpenChannelSync(n.harness.Ctx, &lnd.OpenChannelRequest{
		NodePubkey:         peer.NodeId(),
		LocalFundingAmount: int64(options.AmountSat),
		Private:            false,
	})
	CheckError(n.harness.T, err)

	return &ChannelInfo{
		From:            n,
		To:              peer,
		FundingTxId:     fundResult.GetFundingTxidBytes(),
		FundingTxOutnum: fundResult.OutputIndex,
	}
}

func (n *LndNode) OpenChannelAndWait(
	peer LightningNode,
	options *OpenChannelOptions,
	timeout time.Time) (*ChannelInfo, ShortChannelID) {
	channel := n.OpenChannel(peer, options)
	n.miner.MineBlocks(6)
	cid := channel.WaitForChannelReady(timeout)
	return channel, cid
}

func (n *LndNode) WaitForChannelReady(channel *ChannelInfo, timeout time.Time) ShortChannelID {
	peerId := channel.GetPeer(n).NodeId()
	peerIdStr := hex.EncodeToString(peerId)
	txidStr := hex.EncodeToString(channel.FundingTxId)

	for {
		lc, err := n.rpc.ListChannels(n.harness.Ctx, &lnd.ListChannelsRequest{
			Peer: peerId,
		})
		CheckError(n.harness.T, err)

		index := slices.IndexFunc(lc.Channels, func(c *lnd.Channel) bool {
			s := strings.Split(c.ChannelPoint, ":")
			txid := s[0]
			out, err := strconv.ParseUint(s[1], 10, 32)
			CheckError(n.harness.T, err)

			return c.RemotePubkey == peerIdStr &&
				txid == txidStr &&
				out == uint64(channel.FundingTxOutnum)
		})

		if index >= 0 {
			c := lc.Channels[index]
			return NewShortChanIDFromInt(c.ChanId)
		}

		if time.Now().After(timeout) {
			n.harness.T.Fatal("timed out waiting for channel normal")
		}

		time.Sleep(waitSleepInterval)
	}
}
func (n *LndNode) CreateBolt11Invoice(options *CreateInvoiceOptions) *CreateInvoiceResult {
	req := &lnd.Invoice{
		ValueMsat: int64(options.AmountMsat),
	}

	if options.Description != nil {
		req.Memo = *options.Description
	}

	if options.Preimage != nil {
		req.RPreimage = *options.Preimage
	}

	resp, err := n.rpc.AddInvoice(n.harness.Ctx, req)
	CheckError(n.harness.T, err)

	return &CreateInvoiceResult{
		Bolt11:        resp.PaymentRequest,
		PaymentHash:   resp.RHash,
		PaymentSecret: resp.PaymentAddr,
	}
}

func (n *LndNode) SignMessage(message []byte) []byte {
	resp, err := n.rpc.SignMessage(n.harness.Ctx, &lnd.SignMessageRequest{
		Msg: message,
	})
	CheckError(n.harness.T, err)

	sig, err := hex.DecodeString(resp.Signature)
	CheckError(n.harness.T, err)

	return sig
}

func (n *LndNode) Pay(bolt11 string, timeout time.Time) *PayResult {
	// NOTE: Ignoring the timeout
	resp, err := n.rpc.SendPaymentSync(n.harness.Ctx, &lnd.SendRequest{
		PaymentRequest: bolt11,
	})
	CheckError(n.harness.T, err)

	lastHop := resp.PaymentRoute.Hops[len(resp.PaymentRoute.Hops)-1]
	dest, err := hex.DecodeString(lastHop.PubKey)
	CheckError(n.harness.T, err)

	return &PayResult{
		PaymentHash:     resp.PaymentHash,
		AmountMsat:      uint64(lastHop.AmtToForwardMsat),
		Destination:     dest,
		AmountSentMsat:  uint64(resp.PaymentRoute.TotalAmtMsat),
		PaymentPreimage: resp.PaymentPreimage,
	}
}

func (n *LndNode) GetRoute(destination []byte, amountMsat uint64) *Route {
	routes, err := n.rpc.QueryRoutes(n.harness.Ctx, &lnd.QueryRoutesRequest{
		PubKey:  hex.EncodeToString(destination),
		AmtMsat: int64(amountMsat),
	})
	CheckError(n.harness.T, err)

	if routes.Routes == nil || len(routes.Routes) == 0 {
		CheckError(n.harness.T, fmt.Errorf("no route found"))
	}

	route := routes.Routes[0]
	result := &Route{}
	for _, hop := range route.Hops {
		id, err := hex.DecodeString(hop.PubKey)
		CheckError(n.harness.T, err)

		result.Hops = append(result.Hops, &Hop{
			Id:         id,
			Channel:    NewShortChanIDFromInt(hop.ChanId),
			AmountMsat: uint64(hop.AmtToForwardMsat),
			Delay:      uint16(hop.Expiry),
		})
	}

	return result
}

func (n *LndNode) PayViaRoute(amountMsat uint64, paymentHash []byte, paymentSecret []byte, route *Route, timeout time.Time) *PayResult {
	r := &lnd.Route{}

	for _, hop := range route.Hops {
		r.Hops = append(r.Hops, &lnd.Hop{
			ChanId:           hop.Channel.ToUint64(),
			Expiry:           uint32(hop.Delay),
			PubKey:           hex.EncodeToString(hop.Id),
			AmtToForwardMsat: int64(hop.AmountMsat),
		})
	}

	lh := r.Hops[len(r.Hops)-1]
	lh.MppRecord = &lnd.MPPRecord{
		TotalAmtMsat: int64(amountMsat),
		PaymentAddr:  paymentSecret,
	}

	resp, err := n.rpc.SendToRouteSync(n.harness.Ctx, &lnd.SendToRouteRequest{
		PaymentHash: paymentHash,
		Route:       r,
	})
	CheckError(n.harness.T, err)

	lastHop := resp.PaymentRoute.Hops[len(resp.PaymentRoute.Hops)-1]
	dest, err := hex.DecodeString(lastHop.PubKey)
	CheckError(n.harness.T, err)

	return &PayResult{
		PaymentHash:     resp.PaymentHash,
		AmountMsat:      uint64(resp.PaymentRoute.TotalAmtMsat) - uint64(resp.PaymentRoute.TotalFeesMsat),
		Destination:     dest,
		AmountSentMsat:  uint64(resp.PaymentRoute.TotalAmtMsat),
		PaymentPreimage: resp.PaymentPreimage,
	}
}

func (n *LndNode) GetInvoice(paymentHash []byte) *GetInvoiceResponse {
	resp, err := n.rpc.LookupInvoice(n.harness.Ctx, &lnd.PaymentHash{
		RHash: paymentHash,
	})
	CheckError(n.harness.T, err)

	var paidAt *uint64
	if resp.SettleDate != 0 {
		p := uint64(resp.SettleDate)
		paidAt = &p
	}

	return &GetInvoiceResponse{
		Exists:             true,
		AmountMsat:         uint64(resp.ValueMsat),
		AmountReceivedMsat: uint64(resp.AmtPaidMsat),
		Bolt11:             &resp.PaymentRequest,
		Description:        &resp.Memo,
		ExpiresAt:          uint64(resp.Expiry),
		PaidAt:             paidAt,
		PaymentHash:        resp.RHash,
		PaymentPreimage:    resp.RPreimage,
		IsPaid:             resp.State == lnd.Invoice_SETTLED,
		IsExpired:          resp.State == lnd.Invoice_CANCELED,
	}
}

func (n *LndNode) TearDown() error {
	if n.cmd == nil || n.cmd.Process == nil {
		// return if not properly initialized
		// or error starting the process
		return nil
	}

	if runtime.GOOS == "windows" {
		return n.cmd.Process.Signal(os.Kill)
	}

	return n.cmd.Process.Signal(os.Interrupt)
}

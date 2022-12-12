package lntest

import (
	"bufio"
	"bytes"
	"crypto/tls"
	"crypto/x509"
	"encoding/hex"
	"fmt"
	"io"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"time"

	"github.com/breez/lntest/cln"
	"github.com/btcsuite/btcd/btcec/v2"
	"github.com/decred/dcrd/dcrec/secp256k1/v4"
	"golang.org/x/exp/slices"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials"
)

type CoreLightningNode struct {
	name     string
	nodeId   []byte
	harness  *TestHarness
	miner    *Miner
	cmd      *exec.Cmd
	dir      string
	rpc      cln.NodeClient
	conn     *grpc.ClientConn
	host     string
	port     uint32
	grpcHost string
	grpcPort uint32
	privkey  *secp256k1.PrivateKey
}

func NewCoreLightningNode(h *TestHarness, m *Miner, name string, extraArgs ...string) *CoreLightningNode {
	lightningdDir := h.GetDirectory(fmt.Sprintf("ld-%s", name))
	host := "localhost"
	port, err := GetPort()
	CheckError(h.T, err)

	grpcPort, err := GetPort()
	CheckError(h.T, err)

	binary, err := GetLightningdBinary()
	CheckError(h.T, err)

	bitcoinCliBinary, err := GetBitcoinCliBinary()
	CheckError(h.T, err)

	privKey, err := btcec.NewPrivateKey()
	CheckError(h.T, err)

	s := privKey.Serialize()
	args := []string{
		"--network=regtest",
		"--log-file=log",
		"--log-level=debug",
		"--bitcoin-rpcuser=btcuser",
		"--bitcoin-rpcpassword=btcpass",
		"--allow-deprecated-apis=false",
		"--dev-bitcoind-poll=1",
		"--dev-fast-gossip",
		fmt.Sprintf("--dev-force-privkey=%x", s),
		fmt.Sprintf("--lightning-dir=%s", lightningdDir),
		fmt.Sprintf("--bitcoin-datadir=%s", m.dir),
		fmt.Sprintf("--addr=%s:%d", host, port),
		fmt.Sprintf("--grpc-port=%d", grpcPort),
		fmt.Sprintf("--bitcoin-rpcport=%d", m.rpcPort),
		fmt.Sprintf("--bitcoin-cli=%s", bitcoinCliBinary),
	}

	cmd := exec.CommandContext(h.Ctx, binary, append(args, extraArgs...)...)
	stderr, err := cmd.StderrPipe()
	CheckError(h.T, err)

	stdout, err := cmd.StdoutPipe()
	CheckError(h.T, err)

	log.Printf("%s: starting %s on port %d in dir %s...", name, binary, port, lightningdDir)
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
			log.Printf(name+": "+"lightningd exited with error %s", err)
		} else {
			log.Printf(name + ": " + "process exited normally")
		}
	}()

	regtestDir := filepath.Join(lightningdDir, "regtest")
	waitForLog(h, filepath.Join(regtestDir, "log"), "Server started with public key")

	pemServerCA, err := os.ReadFile(filepath.Join(regtestDir, "ca.pem"))
	CheckError(h.T, err)

	certPool := x509.NewCertPool()
	if !certPool.AppendCertsFromPEM(pemServerCA) {
		h.T.Fatalf("failed to add server CA's certificate")
	}

	clientCert, err := tls.LoadX509KeyPair(filepath.Join(regtestDir, "client.pem"), filepath.Join(regtestDir, "client-key.pem"))
	CheckError(h.T, err)

	tlsConfig := &tls.Config{
		RootCAs:      certPool,
		Certificates: []tls.Certificate{clientCert},
	}

	tlsCredentials := credentials.NewTLS(tlsConfig)

	grpcAddress := fmt.Sprintf("%s:%d", host, grpcPort)
	conn, err := grpc.Dial(grpcAddress, grpc.WithTransportCredentials(tlsCredentials))
	CheckError(h.T, err)

	client := cln.NewNodeClient(conn)
	info, err := client.Getinfo(h.Ctx, &cln.GetinfoRequest{})
	CheckError(h.T, err)

	log.Printf("%s: Has node id %x", name, info.Id)

	node := &CoreLightningNode{
		name:     name,
		nodeId:   info.Id,
		harness:  h,
		miner:    m,
		cmd:      cmd,
		dir:      lightningdDir,
		conn:     conn,
		rpc:      client,
		port:     port,
		host:     host,
		grpcHost: host,
		grpcPort: grpcPort,
		privkey:  privKey,
	}

	h.AddStoppable(node)
	h.RegisterLogfile(filepath.Join(regtestDir, "log"), fmt.Sprintf("lightningd-%s", name))

	return node
}

func waitForLog(h *TestHarness, logfilePath string, phrase string) {
	// at startup we need to wait for the file to open
	for time.Now().Before(h.Deadline()) {
		if _, err := os.Stat(logfilePath); os.IsNotExist(err) {
			time.Sleep(waitSleepInterval)
			continue
		}
		break
	}
	logfile, _ := os.Open(logfilePath)
	defer logfile.Close()

	reader := bufio.NewReader(logfile)
	for time.Now().Before(h.Deadline()) {
		line, err := reader.ReadString('\n')
		if err != nil {
			if err == io.EOF {
				time.Sleep(waitSleepInterval)
			} else {
				CheckError(h.T, err)
			}
		}
		m, err := regexp.MatchString(phrase, line)
		CheckError(h.T, err)
		if m {
			return
		}
	}

	h.T.Fatalf("Unable to find \"%s\" in %s", phrase, logfilePath)
}

func (n *CoreLightningNode) NodeId() []byte {
	return n.nodeId
}

func (n *CoreLightningNode) Host() string {
	return n.host
}

func (n *CoreLightningNode) Port() uint32 {
	return n.port
}

func (n *CoreLightningNode) PrivateKey() *secp256k1.PrivateKey {
	return n.privkey
}

func (n *CoreLightningNode) WaitForSync() {
	for {
		info, _ := n.rpc.Getinfo(n.harness.Ctx, &cln.GetinfoRequest{})

		blockHeight := n.miner.GetBlockHeight()

		if (info.WarningLightningdSync == nil || *info.WarningLightningdSync == "") &&
			info.Blockheight >= blockHeight {
			log.Printf("%s: Synced to blockheight %d", n.name, blockHeight)
			break
		}

		log.Printf(
			"%s: Waiting to sync. Actual block height: %d, node block height: %d",
			n.name,
			blockHeight,
			info.Blockheight,
		)

		if time.Now().After(n.harness.Deadline()) {
			n.harness.T.Fatal("timed out waiting for channel normal")
		}

		time.Sleep(waitSleepInterval)
	}
}

func (n *CoreLightningNode) Fund(amountSat uint64) {
	addrResponse, err := n.rpc.NewAddr(
		n.harness.Ctx,
		&cln.NewaddrRequest{
			Addresstype: cln.NewaddrRequest_BECH32.Enum(),
		},
	)
	CheckError(n.harness.T, err)

	n.miner.SendToAddress(*addrResponse.Bech32, amountSat)
	n.WaitForSync()
}

func (n *CoreLightningNode) ConnectPeer(peer LightningNode) {
	host := peer.Host()
	port := peer.Port()
	_, err := n.rpc.ConnectPeer(n.harness.Ctx, &cln.ConnectRequest{
		Id:   hex.EncodeToString(peer.NodeId()),
		Host: &host,
		Port: &port,
	})
	CheckError(n.harness.T, err)
}

func (n *CoreLightningNode) OpenChannel(peer LightningNode, options *OpenChannelOptions) *ChannelInfo {
	n.ConnectPeer(peer)

	// open a channel
	announce := true
	fundResult, err := n.rpc.FundChannel(n.harness.Ctx, &cln.FundchannelRequest{
		Id: peer.NodeId(),
		Amount: &cln.AmountOrAll{
			Value: &cln.AmountOrAll_Amount{
				Amount: &cln.Amount{
					Msat: options.AmountSat * 1000,
				},
			},
		},
		Announce: &announce,
	})
	CheckError(n.harness.T, err)

	return &ChannelInfo{
		From:            n,
		To:              peer,
		FundingTxId:     fundResult.Txid,
		FundingTxOutnum: fundResult.Outnum,
	}
}

func (n *CoreLightningNode) WaitForChannelReady(channel *ChannelInfo) ShortChannelID {
	log.Printf("%s: Wait for channel ready.", n.name)
	peerId := channel.GetPeer(n).NodeId()

	for {
		peers, err := n.rpc.ListPeers(n.harness.Ctx, &cln.ListpeersRequest{
			Id: peerId,
		})
		CheckError(n.harness.T, err)

		if len(peers.Peers) == 0 {
			n.harness.T.Fatalf("Peer %x not found", peerId)
		}

		peer := peers.Peers[0]
		if peer.Channels == nil {
			n.harness.T.Fatal("no channels for peer")
		}

		channelIndex := slices.IndexFunc(
			peer.Channels,
			func(pc *cln.ListpeersPeersChannels) bool {
				return bytes.Equal(pc.FundingTxid, channel.FundingTxId) &&
					*pc.FundingOutnum == channel.FundingTxOutnum
			},
		)

		if channelIndex >= 0 {
			peerChannel := peer.Channels[channelIndex]
			if peerChannel.State == cln.ListpeersPeersChannels_CHANNELD_AWAITING_LOCKIN {
				log.Printf("%s: Channel state is CHANNELD_AWAITING_LOCKIN, mining some blocks.", n.name)
				n.miner.MineBlocks(6)
				n.WaitForSync()
			}

			if peerChannel.State == cln.ListpeersPeersChannels_CHANNELD_NORMAL {
				channelsResp, err := n.rpc.ListChannels(n.harness.Ctx, &cln.ListchannelsRequest{
					ShortChannelId: peerChannel.ShortChannelId,
				})
				CheckError(n.harness.T, err)

				// Wait for the channel to end up in the listchannels response.
				if len(channelsResp.Channels) > 0 &&
					channelsResp.Channels[0].Active {
					log.Printf("%s: Channel active with chan id: %s", n.name, channelsResp.Channels[0].ShortChannelId)
					return NewShortChanIDFromString(channelsResp.Channels[0].ShortChannelId)
				}
			}
		}

		if time.Now().After(n.harness.Deadline()) {
			n.harness.T.Fatal("timed out waiting for channel normal")
		}

		time.Sleep(waitSleepInterval)
	}
}

func (n *CoreLightningNode) CreateBolt11Invoice(options *CreateInvoiceOptions) *CreateInvoiceResult {
	label, err := GenerateRandomString()
	CheckError(n.harness.T, err)

	req := &cln.InvoiceRequest{
		Label: label,
		AmountMsat: &cln.AmountOrAny{
			Value: &cln.AmountOrAny_Amount{
				Amount: &cln.Amount{
					Msat: options.AmountMsat,
				},
			},
		},
	}

	if options.Description != nil {
		req.Description = *options.Description
	}

	if options.Preimage != nil {
		req.Preimage = *options.Preimage
	}

	resp, err := n.rpc.Invoice(n.harness.Ctx, req)
	CheckError(n.harness.T, err)

	return &CreateInvoiceResult{
		Bolt11:        resp.Bolt11,
		PaymentHash:   resp.PaymentHash,
		PaymentSecret: resp.PaymentSecret,
	}
}

func (n *CoreLightningNode) SignMessage(message []byte) []byte {
	resp, err := n.rpc.SignMessage(n.harness.Ctx, &cln.SignmessageRequest{
		Message: hex.EncodeToString(message),
	})
	CheckError(n.harness.T, err)

	return resp.Signature
}

func (n *CoreLightningNode) Pay(bolt11 string) *PayResult {
	rpcTimeout := getTimeoutSeconds(n.harness.T, n.harness.Deadline())
	resp, err := n.rpc.Pay(n.harness.Ctx, &cln.PayRequest{
		Bolt11:   bolt11,
		RetryFor: &rpcTimeout,
	})
	CheckError(n.harness.T, err)

	return &PayResult{
		PaymentHash:     resp.PaymentHash,
		AmountMsat:      resp.AmountMsat.Msat,
		Destination:     resp.Destination,
		AmountSentMsat:  resp.AmountSentMsat.Msat,
		PaymentPreimage: resp.PaymentPreimage,
	}
}

func (n *CoreLightningNode) GetRoute(destination []byte, amountMsat uint64) *Route {
	route, err := n.rpc.GetRoute(n.harness.Ctx, &cln.GetrouteRequest{
		Id: destination,
		AmountMsat: &cln.Amount{
			Msat: amountMsat,
		},
		Riskfactor: 0,
	})
	CheckError(n.harness.T, err)

	result := &Route{}
	for _, hop := range route.Route {
		result.Hops = append(result.Hops, &Hop{
			Id:         hop.Id,
			Channel:    NewShortChanIDFromString(hop.Channel),
			AmountMsat: hop.AmountMsat.Msat,
			Delay:      uint16(hop.Delay),
		})
	}
	return result
}

func (n *CoreLightningNode) GetChannels() []*ChannelDetails {
	peers, err := n.rpc.ListPeers(n.harness.Ctx, &cln.ListpeersRequest{})
	CheckError(n.harness.T, err)

	var result []*ChannelDetails
	for _, p := range peers.Peers {
		for _, c := range p.Channels {
			var s ShortChannelID = NewShortChanIDFromInt(0)
			if c.ShortChannelId != nil {
				s = NewShortChanIDFromString(*c.ShortChannelId)
			}
			result = append(result, &ChannelDetails{
				PeerId:              p.Id,
				ShortChannelID:      s,
				CapacityMsat:        c.TotalMsat.Msat,
				LocalReserveMsat:    c.OurReserveMsat.Msat,
				RemoteReserveMsat:   c.TheirReserveMsat.Msat,
				LocalSpendableMsat:  c.SpendableMsat.Msat,
				RemoteSpendableMsat: c.ReceivableMsat.Msat,
			})
		}
	}

	return result
}

func (n *CoreLightningNode) startPayViaRoute(amountMsat uint64, paymentHash []byte, paymentSecret []byte, route *Route) *cln.SendpayResponse {
	var sendPayRoute []*cln.SendpayRoute
	for _, hop := range route.Hops {
		sendPayRoute = append(sendPayRoute, &cln.SendpayRoute{
			AmountMsat: &cln.Amount{
				Msat: hop.AmountMsat,
			},
			Id:      hop.Id,
			Delay:   uint32(hop.Delay),
			Channel: hop.Channel.String(),
		})
	}

	resp, err := n.rpc.SendPay(n.harness.Ctx, &cln.SendpayRequest{
		Route:       sendPayRoute,
		PaymentHash: paymentHash,
		AmountMsat: &cln.Amount{
			Msat: amountMsat,
		},
		PaymentSecret: paymentSecret,
	})
	CheckError(n.harness.T, err)

	return resp
}

func (n *CoreLightningNode) PayViaRoute(
	amountMsat uint64,
	paymentHash []byte,
	paymentSecret []byte,
	route *Route,
) *PayResult {
	resp := n.startPayViaRoute(amountMsat, paymentHash, paymentSecret, route)
	t := getTimeoutSeconds(n.harness.T, n.harness.Deadline())
	w, err := n.rpc.WaitSendPay(n.harness.Ctx, &cln.WaitsendpayRequest{
		PaymentHash: resp.PaymentHash,
		Timeout:     &t,
		Partid:      resp.Partid,
		Groupid:     resp.Groupid,
	})
	CheckError(n.harness.T, err)

	return &PayResult{
		PaymentHash:     w.PaymentHash,
		AmountMsat:      w.AmountMsat.Msat,
		Destination:     w.Destination,
		AmountSentMsat:  w.AmountSentMsat.Msat,
		PaymentPreimage: w.PaymentPreimage,
	}
}

func (n *CoreLightningNode) GetInvoice(paymentHash []byte) *GetInvoiceResponse {
	resp, err := n.rpc.ListInvoices(n.harness.Ctx, &cln.ListinvoicesRequest{
		PaymentHash: paymentHash,
	})
	CheckError(n.harness.T, err)
	if resp.Invoices == nil || len(resp.Invoices) == 0 {
		return &GetInvoiceResponse{
			Exists: false,
		}
	}
	invoice := resp.Invoices[0]
	return &GetInvoiceResponse{
		Exists:             true,
		AmountMsat:         invoice.AmountMsat.Msat,
		AmountReceivedMsat: invoice.AmountReceivedMsat.Msat,
		Bolt11:             invoice.Bolt11,
		Description:        invoice.Description,
		ExpiresAt:          invoice.ExpiresAt,
		PaidAt:             invoice.PaidAt,
		PayerNote:          invoice.PayerNote,
		PaymentHash:        invoice.PaymentHash,
		PaymentPreimage:    invoice.PaymentPreimage,
		IsPaid:             invoice.Status == cln.ListinvoicesInvoices_PAID,
		IsExpired:          invoice.Status == cln.ListinvoicesInvoices_EXPIRED,
	}
}

func (n *CoreLightningNode) GetPeerFeatures(peerId []byte) map[uint32]string {
	resp, err := n.rpc.ListPeers(n.harness.Ctx, &cln.ListpeersRequest{
		Id: peerId,
	})
	CheckError(n.harness.T, err)

	r := make(map[uint32]string)
	if len(resp.Peers) == 0 {
		return r
	}
	node := resp.Peers[0]
	return n.mapFeatures(node.Features)
}

func (n *CoreLightningNode) GetRemoteNodeFeatures(nodeId []byte) map[uint32]string {
	resp, err := n.rpc.ListNodes(n.harness.Ctx, &cln.ListnodesRequest{
		Id: nodeId,
	})
	CheckError(n.harness.T, err)

	if len(resp.Nodes) == 0 {
		return make(map[uint32]string)
	}
	node := resp.Nodes[0]
	return n.mapFeatures(node.Features)
}

func (n *CoreLightningNode) mapFeatures(f []byte) map[uint32]string {
	r := make(map[uint32]string)
	for i := 0; i < len(f); i++ {
		b := f[i]
		for j := 0; j < 8; j++ {
			if ((b >> j) & 1) != 0 {
				r[uint32(i)*8+uint32(j)] = ""
			}
		}
	}
	return r
}

func (n *CoreLightningNode) TearDown() error {
	if n.conn != nil {
		err := n.conn.Close()
		if err != nil {
			log.Printf("Error closing client conn: %v", err)
		}
	}

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

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
	"sync"
	"time"

	"github.com/breez/lntest/cln"
	"github.com/btcsuite/btcd/btcec/v2"
	"github.com/decred/dcrd/dcrec/secp256k1/v4"
	"golang.org/x/exp/slices"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials"
)

type ClnNode struct {
	name        string
	binary      string
	args        []string
	regtestDir  string
	logfilePath string
	nodeId      []byte
	harness     *TestHarness
	miner       *Miner
	dir         string
	host        string
	port        uint32
	grpcHost    string
	grpcPort    uint32
	privkey     *secp256k1.PrivateKey
	runtime     *clnNodeRuntime
	mtx         sync.Mutex
}

type clnNodeRuntime struct {
	conn     *grpc.ClientConn
	rpc      cln.NodeClient
	cmd      *exec.Cmd
	cleanups []*Cleanup
}

func NewClnNode(h *TestHarness, m *Miner, name string, extraArgs ...string) *ClnNode {
	binary, err := GetLightningdBinary()
	CheckError(h.T, err)

	return NewClnNodeFromBinary(h, m, name, binary, extraArgs...)
}

func NewClnNodeFromBinary(h *TestHarness, m *Miner, name string, binary string, extraArgs ...string) *ClnNode {
	lightningdDir := h.GetDirectory(fmt.Sprintf("ld-%s", name))
	regtestDir := filepath.Join(lightningdDir, "regtest")
	logfilePath := filepath.Join(regtestDir, "log")
	host := "localhost"
	port, err := GetPort()
	CheckError(h.T, err)

	grpcPort, err := GetPort()
	CheckError(h.T, err)

	bitcoinCliBinary, err := GetBitcoinCliBinary()
	CheckError(h.T, err)

	privKey, err := btcec.NewPrivateKey()
	CheckError(h.T, err)

	s := privKey.Serialize()
	p := privKey.PubKey().SerializeCompressed()
	args := append([]string{
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
	}, extraArgs...)

	node := &ClnNode{
		name:        name,
		binary:      binary,
		args:        args,
		regtestDir:  regtestDir,
		logfilePath: logfilePath,
		nodeId:      p,
		harness:     h,
		miner:       m,
		dir:         lightningdDir,
		port:        port,
		host:        host,
		grpcHost:    host,
		grpcPort:    grpcPort,
		privkey:     privKey,
	}

	h.AddStoppable(node)
	h.RegisterLogfile(filepath.Join(regtestDir, "log"), fmt.Sprintf("lightningd-%s", name))

	return node
}

func (n *ClnNode) Start() {
	n.mtx.Lock()
	defer n.mtx.Unlock()

	if n.runtime != nil {
		log.Printf("%s: Start called, but was already started.", n.name)
		return
	}

	var cleanups []*Cleanup
	cmd := exec.CommandContext(n.harness.Ctx, n.binary, n.args...)
	stderr, err := cmd.StderrPipe()
	CheckError(n.harness.T, err)

	stdout, err := cmd.StdoutPipe()
	CheckError(n.harness.T, err)

	log.Printf("%s: starting %s on port %d in dir %s...", n.name, n.binary, n.port, n.dir)
	err = cmd.Start()
	CheckError(n.harness.T, err)

	cleanups = append(cleanups, &Cleanup{
		Name: "cmd",
		Fn: func() error {
			proc := cmd.Process
			if proc != nil {
				if runtime.GOOS == "windows" {
					return proc.Signal(os.Kill)
				}

				return proc.Signal(os.Interrupt)
			}

			return nil
		},
	})

	go func() {
		// print any stderr output to the test log
		scanner := bufio.NewScanner(stderr)
		for scanner.Scan() {
			log.Println(n.name + ": " + scanner.Text())
		}
	}()

	go func() {
		// print any stderr output to the test log
		scanner := bufio.NewScanner(stdout)
		for scanner.Scan() {
			log.Println(n.name + ": " + scanner.Text())
		}
	}()

	go func() {
		err := cmd.Wait()
		if err != nil && err.Error() != "signal: interrupt" {
			log.Printf("%s: lightningd exited with error %s", n.name, err)
		} else {
			log.Printf("%s: process exited normally", n.name)
		}
	}()

	err = n.waitForLog("Server started with public key")
	if err != nil {
		PerformCleanup(cleanups)
		n.harness.T.Fatalf("%s: Error waiting for cln to start: %v", n.name, err)
	}

	pemServerCA, err := os.ReadFile(filepath.Join(n.regtestDir, "ca.pem"))
	if err != nil {
		PerformCleanup(cleanups)
		n.harness.T.Fatalf("%s: Failed to read ca.pem: %v", n.name, err)
	}

	certPool := x509.NewCertPool()
	if !certPool.AppendCertsFromPEM(pemServerCA) {
		PerformCleanup(cleanups)
		n.harness.T.Fatalf("%s: failed to add server CA's certificate", n.name)
	}

	clientCert, err := tls.LoadX509KeyPair(
		filepath.Join(n.regtestDir, "client.pem"),
		filepath.Join(n.regtestDir, "client-key.pem"),
	)
	if err != nil {
		PerformCleanup(cleanups)
		n.harness.T.Fatalf("%s: Failed to load LoadX509KeyPair: %v", n.name, err)
	}

	tlsConfig := &tls.Config{
		RootCAs:      certPool,
		Certificates: []tls.Certificate{clientCert},
	}

	tlsCredentials := credentials.NewTLS(tlsConfig)

	grpcAddress := fmt.Sprintf("%s:%d", n.host, n.grpcPort)
	conn, err := grpc.Dial(
		grpcAddress,
		grpc.WithTransportCredentials(tlsCredentials),
	)
	if err != nil {
		PerformCleanup(cleanups)
		n.harness.T.Fatalf("%s: Failed to obtain grpc client: %v", n.name, err)
	}
	cleanups = append(cleanups, &Cleanup{
		Name: "grpc conn",
		Fn:   conn.Close,
	})

	client := cln.NewNodeClient(conn)
	info, err := client.Getinfo(n.harness.Ctx, &cln.GetinfoRequest{})
	if err != nil {
		PerformCleanup(cleanups)
		n.harness.T.Fatalf("%s: Failed to call Getinfo: %v", n.name, err)
	}
	log.Printf("%s: Has node id %x", n.name, info.Id)

	n.runtime = &clnNodeRuntime{
		conn:     conn,
		rpc:      client,
		cmd:      cmd,
		cleanups: cleanups,
	}
}

func (n *ClnNode) Stop() error {
	n.mtx.Lock()
	defer n.mtx.Unlock()

	if n.runtime == nil {
		log.Printf("%s: Stop called, but was already stopped.", n.name)
		return nil
	}

	PerformCleanup(n.runtime.cleanups)
	n.runtime = nil
	return nil
}

func (n *ClnNode) waitForLog(phrase string) error {
	logfilePath := filepath.Join(n.dir, "regtest", "log")
	// at startup we need to wait for the file to open
	for time.Now().Before(n.harness.Deadline()) {
		if _, err := os.Stat(logfilePath); os.IsNotExist(err) {
			<-time.After(waitSleepInterval)
			continue
		}
		break
	}
	logfile, _ := os.Open(logfilePath)
	defer logfile.Close()

	reader := bufio.NewReader(logfile)
	for time.Now().Before(n.harness.Deadline()) {
		line, err := reader.ReadString('\n')
		if err != nil {
			if err == io.EOF {
				<-time.After(waitSleepInterval)
			} else {
				return err
			}
		}
		m, err := regexp.MatchString(phrase, line)
		if err != nil {
			return err
		}

		if m {
			return nil
		}
	}

	return fmt.Errorf("unable to find \"%s\" in %s", phrase, logfilePath)
}

func (n *ClnNode) NodeId() []byte {
	return n.nodeId
}

func (n *ClnNode) Host() string {
	return n.host
}

func (n *ClnNode) Port() uint32 {
	return n.port
}

func (n *ClnNode) PrivateKey() *secp256k1.PrivateKey {
	return n.privkey
}

func (n *ClnNode) IsStarted() bool {
	return n.runtime != nil
}

func (n *ClnNode) WaitForSync() {
	for {
		info, _ := n.runtime.rpc.Getinfo(n.harness.Ctx, &cln.GetinfoRequest{})

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

		<-time.After(waitSleepInterval)
	}
}

func (n *ClnNode) Fund(amountSat uint64) {
	addrResponse, err := n.runtime.rpc.NewAddr(
		n.harness.Ctx,
		&cln.NewaddrRequest{
			Addresstype: cln.NewaddrRequest_BECH32.Enum(),
		},
	)
	CheckError(n.harness.T, err)

	n.miner.SendToAddress(*addrResponse.Bech32, amountSat)
	n.WaitForSync()
}

func (n *ClnNode) ConnectPeer(peer LightningNode) {
	host := peer.Host()
	port := peer.Port()
	_, err := n.runtime.rpc.ConnectPeer(n.harness.Ctx, &cln.ConnectRequest{
		Id:   hex.EncodeToString(peer.NodeId()),
		Host: &host,
		Port: &port,
	})
	CheckError(n.harness.T, err)
}

func (n *ClnNode) OpenChannel(peer LightningNode, options *OpenChannelOptions) *ChannelInfo {
	n.ConnectPeer(peer)

	// open a channel
	announce := true
	fundResult, err := n.runtime.rpc.FundChannel(n.harness.Ctx, &cln.FundchannelRequest{
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

func (n *ClnNode) WaitForChannelReady(channel *ChannelInfo) ShortChannelID {
	log.Printf("%s: Wait for channel ready.", n.name)
	peerId := channel.GetPeer(n).NodeId()

	for {
		peers, err := n.runtime.rpc.ListPeers(n.harness.Ctx, &cln.ListpeersRequest{
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
				channelsResp, err := n.runtime.rpc.ListChannels(n.harness.Ctx, &cln.ListchannelsRequest{
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

		<-time.After(waitSleepInterval)
	}
}

func (n *ClnNode) CreateBolt11Invoice(options *CreateInvoiceOptions) *CreateInvoiceResult {
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

	resp, err := n.runtime.rpc.Invoice(n.harness.Ctx, req)
	CheckError(n.harness.T, err)

	return &CreateInvoiceResult{
		Bolt11:        resp.Bolt11,
		PaymentHash:   resp.PaymentHash,
		PaymentSecret: resp.PaymentSecret,
	}
}

func (n *ClnNode) SignMessage(message []byte) []byte {
	resp, err := n.runtime.rpc.SignMessage(n.harness.Ctx, &cln.SignmessageRequest{
		Message: hex.EncodeToString(message),
	})
	CheckError(n.harness.T, err)

	return resp.Signature
}

func (n *ClnNode) Pay(bolt11 string) *PayResult {
	rpcTimeout := getTimeoutSeconds(n.harness.T, n.harness.Deadline())
	resp, err := n.runtime.rpc.Pay(n.harness.Ctx, &cln.PayRequest{
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

func (n *ClnNode) GetRoute(destination []byte, amountMsat uint64) *Route {
	route, err := n.runtime.rpc.GetRoute(n.harness.Ctx, &cln.GetrouteRequest{
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

func (n *ClnNode) GetChannels() []*ChannelDetails {
	peers, err := n.runtime.rpc.ListPeers(n.harness.Ctx, &cln.ListpeersRequest{})
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

func (n *ClnNode) startPayViaRoute(amountMsat uint64, paymentHash []byte, paymentSecret []byte, route *Route) *cln.SendpayResponse {
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

	resp, err := n.runtime.rpc.SendPay(n.harness.Ctx, &cln.SendpayRequest{
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

func (n *ClnNode) PayViaRoute(
	amountMsat uint64,
	paymentHash []byte,
	paymentSecret []byte,
	route *Route,
) *PayResult {
	resp := n.startPayViaRoute(amountMsat, paymentHash, paymentSecret, route)
	t := getTimeoutSeconds(n.harness.T, n.harness.Deadline())
	w, err := n.runtime.rpc.WaitSendPay(n.harness.Ctx, &cln.WaitsendpayRequest{
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

func (n *ClnNode) GetInvoice(paymentHash []byte) *GetInvoiceResponse {
	resp, err := n.runtime.rpc.ListInvoices(n.harness.Ctx, &cln.ListinvoicesRequest{
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

func (n *ClnNode) GetPeerFeatures(peerId []byte) map[uint32]string {
	resp, err := n.runtime.rpc.ListPeers(n.harness.Ctx, &cln.ListpeersRequest{
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

func (n *ClnNode) GetRemoteNodeFeatures(nodeId []byte) map[uint32]string {
	resp, err := n.runtime.rpc.ListNodes(n.harness.Ctx, &cln.ListnodesRequest{
		Id: nodeId,
	})
	CheckError(n.harness.T, err)

	if len(resp.Nodes) == 0 {
		return make(map[uint32]string)
	}
	node := resp.Nodes[0]
	return n.mapFeatures(node.Features)
}

func (n *ClnNode) mapFeatures(f []byte) map[uint32]string {
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

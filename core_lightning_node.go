package lntest

import (
	"bufio"
	"context"
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
	"testing"
	"time"

	"github.com/breez/lntest/core_lightning"
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
	rpc      core_lightning.NodeClient
	host     *string
	port     *uint32
	grpcHost *string
	grpcPort *uint32
}

func NewCoreLightningNode(h *TestHarness, m *Miner, name string, timeout time.Time, extraArgs ...string) *CoreLightningNode {
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

	args := []string{
		"--network=regtest",
		"--log-file=log",
		"--log-level=debug",
		"--bitcoin-rpcuser=btcuser",
		"--bitcoin-rpcpassword=btcpass",
		"--allow-deprecated-apis=false",
		"--dev-bitcoind-poll=1",
		"--dev-fast-gossip",
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
	waitForLog(h.T, filepath.Join(regtestDir, "log"), "Server started with public key", timeout)

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

	client := core_lightning.NewNodeClient(conn)
	info, err := client.Getinfo(h.Ctx, &core_lightning.GetinfoRequest{})
	CheckError(h.T, err)

	log.Printf("%s: Has node id %x", name, info.Id)

	node := &CoreLightningNode{
		name:     name,
		nodeId:   info.Id,
		harness:  h,
		miner:    m,
		cmd:      cmd,
		dir:      lightningdDir,
		rpc:      client,
		port:     &port,
		host:     &host,
		grpcHost: &host,
		grpcPort: &grpcPort,
	}

	h.AddStoppable(node)
	h.RegisterLogfile(filepath.Join(regtestDir, "log"), fmt.Sprintf("lightningd-%s", name))

	return node
}

func waitForLog(t *testing.T, logfilePath string, phrase string, timeout time.Time) {
	// at startup we need to wait for the file to open
	for time.Now().Before(timeout) {
		if _, err := os.Stat(logfilePath); os.IsNotExist(err) {
			time.Sleep(waitSleepInterval)
			continue
		}
		break
	}
	logfile, _ := os.Open(logfilePath)
	defer logfile.Close()

	reader := bufio.NewReader(logfile)
	for time.Now().Before(timeout) {
		line, err := reader.ReadString('\n')
		if err != nil {
			if err == io.EOF {
				time.Sleep(waitSleepInterval)
			} else {
				CheckError(t, err)
			}
		}
		m, err := regexp.MatchString(phrase, line)
		CheckError(t, err)
		if m {
			return
		}
	}

	t.Fatalf("Unable to find \"%s\" in %s", phrase, logfilePath)
}

func (n *CoreLightningNode) NodeId() []byte {
	return n.nodeId
}

func (n *CoreLightningNode) Host() *string {
	return n.host
}

func (n *CoreLightningNode) Port() *uint32 {
	return n.port
}

func (n *CoreLightningNode) PrivateKey() []byte {
	return n.nodeId
}

func (n *CoreLightningNode) WaitForSync(timeout time.Time) {
	for {
		info, _ := n.rpc.Getinfo(n.harness.Ctx, &core_lightning.GetinfoRequest{})

		blockHeight := n.miner.GetBlockHeight()

		if (info.WarningLightningdSync == nil || *info.WarningLightningdSync == "") &&
			info.Blockheight >= blockHeight {
			log.Printf("node %s is synced to blockheight %d", n.name, blockHeight)
			break
		}

		log.Printf(
			"Waiting for node %s to sync. Actual block height: %d, node block height: %d",
			n.name,
			blockHeight,
			info.Blockheight,
		)

		if time.Now().After(timeout) {
			n.harness.T.Fatal("timed out waiting for channel normal")
		}

		time.Sleep(waitSleepInterval)
	}
}

func (n *CoreLightningNode) Fund(amountSat uint64, timeout time.Time) {
	addrResponse, err := n.rpc.NewAddr(
		context.Background(),
		&core_lightning.NewaddrRequest{
			Addresstype: core_lightning.NewaddrRequest_BECH32.Enum(),
		},
	)
	CheckError(n.harness.T, err)

	n.miner.SendToAddress(*addrResponse.Bech32, amountSat)
	n.WaitForSync(timeout)
}

type OpenChannelOptions struct {
	AmountSat uint64
	FeePerKw  uint
}

func (n *CoreLightningNode) ConnectPeer(peer LightningNode) {
	_, err := n.rpc.ConnectPeer(n.harness.Ctx, &core_lightning.ConnectRequest{
		Id:   hex.EncodeToString(peer.NodeId()),
		Host: peer.Host(),
		Port: peer.Port(),
	})
	CheckError(n.harness.T, err)
}

func (n *CoreLightningNode) OpenChannel(peer *CoreLightningNode, options *OpenChannelOptions) *ChannelInfo {
	peerInfo, err := peer.rpc.Getinfo(n.harness.Ctx, &core_lightning.GetinfoRequest{})
	CheckError(n.harness.T, err)

	n.ConnectPeer(peer)

	feePerKw := options.FeePerKw
	if feePerKw == 0 {
		feePerKw = 253
	}

	// open a channel
	announce := true
	fundResult, err := n.rpc.FundChannel(n.harness.Ctx, &core_lightning.FundchannelRequest{
		Id: peerInfo.Id,
		Amount: &core_lightning.AmountOrAll{
			Value: &core_lightning.AmountOrAll_Amount{
				Amount: &core_lightning.Amount{
					Msat: options.AmountSat * 1000,
				},
			},
		},
		Feerate: &core_lightning.Feerate{
			Style: &core_lightning.Feerate_Perkw{
				Perkw: uint32(feePerKw),
			},
		},
		Announce: &announce,
	})
	CheckError(n.harness.T, err)

	return &ChannelInfo{
		From:        n,
		To:          peer,
		FundingTx:   string(fundResult.Tx),
		FundingTxId: string(fundResult.Txid),
		ChannelId:   string(fundResult.ChannelId),
	}
}

func (n *CoreLightningNode) OpenChannelAndWait(
	peer *CoreLightningNode,
	options *OpenChannelOptions,
	timeout time.Time) *ChannelInfo {
	channel := n.OpenChannel(peer, options)
	n.miner.MineBlocks(6)
	channel.WaitForChannelReady(timeout)
	return channel
}

type CreateInvoiceOptions struct {
	AmountMsat  uint64
	Description *string
	Preimage    *[]byte
	Label       *string
}

type CreateInvoiceResult struct {
	Bolt11        string
	PaymentHash   []byte
	PaymentSecret []byte
	ExpiresAt     uint64
}

func (n *CoreLightningNode) CreateBolt11Invoice(options *CreateInvoiceOptions) *CreateInvoiceResult {
	req := &core_lightning.InvoiceRequest{
		AmountMsat: &core_lightning.AmountOrAny{
			Value: &core_lightning.AmountOrAny_Amount{
				Amount: &core_lightning.Amount{
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

	if options.Label != nil {
		req.Label = *options.Label
	}

	resp, err := n.rpc.Invoice(n.harness.Ctx, req)
	CheckError(n.harness.T, err)

	return &CreateInvoiceResult{
		Bolt11:        resp.Bolt11,
		PaymentHash:   resp.PaymentHash,
		PaymentSecret: resp.PaymentSecret,
		ExpiresAt:     resp.ExpiresAt,
	}
}

func (n *CoreLightningNode) AddInvoice(bolt11 string, preimage []byte, label string) *CreateInvoiceResult {
	resp, err := n.rpc.CreateInvoice(n.harness.Ctx, &core_lightning.CreateinvoiceRequest{
		Invstring: bolt11,
		Preimage:  preimage,
		Label:     label,
	})
	CheckError(n.harness.T, err)

	return &CreateInvoiceResult{
		Bolt11:      *resp.Bolt11,
		PaymentHash: resp.PaymentHash,
		ExpiresAt:   resp.ExpiresAt,
	}
}

func (n *CoreLightningNode) SignMessage(message []byte) []byte {
	resp, err := n.rpc.SignMessage(n.harness.Ctx, &core_lightning.SignmessageRequest{
		Message: hex.EncodeToString(message),
	})
	CheckError(n.harness.T, err)

	return resp.Signature
}

type PayResult struct {
	PaymentHash     []byte
	Parts           uint32
	AmountMsat      uint64
	Destination     []byte
	CreatedAt       uint64
	AmountSentMsat  uint64
	PaymentPreimage []byte
}

func (n *CoreLightningNode) Pay(bolt11 string, timeout time.Time) *PayResult {
	rpcTimeout := getTimeoutSeconds(n.harness.T, timeout)
	resp, err := n.rpc.Pay(n.harness.Ctx, &core_lightning.PayRequest{
		Bolt11:   bolt11,
		RetryFor: &rpcTimeout,
	})
	CheckError(n.harness.T, err)

	return &PayResult{
		PaymentHash:     resp.PaymentHash,
		Parts:           resp.Parts,
		AmountMsat:      resp.AmountMsat.Msat,
		Destination:     resp.Destination,
		CreatedAt:       uint64(resp.CreatedAt),
		AmountSentMsat:  resp.AmountSentMsat.Msat,
		PaymentPreimage: resp.PaymentPreimage,
	}
}

type Route struct {
	Route []*Hop
}

type Hop struct {
	Id         []byte
	Channel    string
	AmountMsat uint64
	Delay      uint16
}

func (n *CoreLightningNode) GetRoute(destination []byte, amountMsat uint64) *Route {
	route, err := n.rpc.GetRoute(n.harness.Ctx, &core_lightning.GetrouteRequest{
		Id: destination,
		AmountMsat: &core_lightning.Amount{
			Msat: amountMsat,
		},
		Riskfactor: 0,
	})
	CheckError(n.harness.T, err)

	result := &Route{}
	for _, hop := range route.Route {
		result.Route = append(result.Route, &Hop{
			Id:         hop.Id,
			Channel:    hop.Channel,
			AmountMsat: hop.AmountMsat.Msat,
			Delay:      uint16(hop.Delay),
		})
	}
	return result
}

type PayViaRouteResponse struct {
	PartId uint32
}

func (n *CoreLightningNode) StartPayViaRoute(amountMsat uint64, paymentHash []byte, route *Route) *PayViaRouteResponse {
	var sendPayRoute []*core_lightning.SendpayRoute
	for _, hop := range route.Route {
		sendPayRoute = append(sendPayRoute, &core_lightning.SendpayRoute{
			AmountMsat: &core_lightning.Amount{
				Msat: hop.AmountMsat,
			},
			Id:      hop.Id,
			Delay:   uint32(hop.Delay),
			Channel: hop.Channel,
		})
	}

	resp, err := n.rpc.SendPay(n.harness.Ctx, &core_lightning.SendpayRequest{
		Route:       sendPayRoute,
		PaymentHash: paymentHash,
		AmountMsat: &core_lightning.Amount{
			Msat: amountMsat,
		},
	})
	CheckError(n.harness.T, err)

	return &PayViaRouteResponse{
		PartId: uint32(*resp.Partid),
	}
}

func (n *CoreLightningNode) PayViaRouteAndWait(
	amountMsat uint64,
	paymentHash []byte,
	route *Route,
	timeout time.Time) *PayPartResult {
	resp := n.StartPayViaRoute(amountMsat, paymentHash, route)
	return n.WaitForPaymentPart(paymentHash, timeout, resp.PartId)
}

func (n *CoreLightningNode) StartPayPartViaRoute(
	amountMsat uint64,
	paymentHash []byte,
	paymentSecret []byte,
	partId uint32,
	route *Route) {
	var sendPayRoute []*core_lightning.SendpayRoute
	for _, hop := range route.Route {
		sendPayRoute = append(sendPayRoute, &core_lightning.SendpayRoute{
			AmountMsat: &core_lightning.Amount{
				Msat: hop.AmountMsat,
			},
			Id:      hop.Id,
			Delay:   uint32(hop.Delay),
			Channel: hop.Channel,
		})
	}

	_, err := n.rpc.SendPay(n.harness.Ctx, &core_lightning.SendpayRequest{
		Route:       sendPayRoute,
		PaymentHash: paymentHash,
		AmountMsat: &core_lightning.Amount{
			Msat: amountMsat,
		},
		Partid:        &partId,
		PaymentSecret: paymentSecret,
	})
	CheckError(n.harness.T, err)
}

type PayPartResult struct {
	PaymentHash     []byte
	PartId          *uint32
	AmountMsat      uint64
	Destination     []byte
	CreatedAt       uint64
	AmountSentMsat  uint64
	PaymentPreimage []byte
}

func (n *CoreLightningNode) WaitForPaymentPart(paymentHash []byte, timeout time.Time, partId uint32) *PayPartResult {
	rpcTimeout := getTimeoutSeconds(n.harness.T, timeout)
	rpcPartId := uint64(partId)
	resp, err := n.rpc.WaitSendPay(n.harness.Ctx, &core_lightning.WaitsendpayRequest{
		PaymentHash: paymentHash,
		Timeout:     &rpcTimeout,
		Partid:      &rpcPartId,
	})
	CheckError(n.harness.T, err)
	var partid uint32
	if resp.Partid != nil {
		partid = uint32(*resp.Partid)
	}
	return &PayPartResult{
		PaymentHash:     resp.PaymentHash,
		PartId:          &partid,
		AmountMsat:      resp.AmountMsat.Msat,
		Destination:     resp.Destination,
		CreatedAt:       resp.CreatedAt,
		AmountSentMsat:  resp.AmountSentMsat.Msat,
		PaymentPreimage: resp.PaymentPreimage,
	}
}

func (n *CoreLightningNode) WaitForPaymentParts(paymentHash []byte, timeout time.Time, partIds ...uint32) []*PayPartResult {
	var result []*PayPartResult
	for _, partId := range partIds {
		part := n.WaitForPaymentPart(paymentHash, timeout, partId)
		result = append(result, part)
	}

	return result
}

type WaitPaymentCompleteResponse struct {
	PaymentHash     []byte
	AmountMsat      uint64
	Destination     []byte
	CreatedAt       uint64
	AmountSentMsat  uint64
	PaymentPreimage []byte
}

func (n *CoreLightningNode) WaitPaymentComplete(paymentHash []byte, parts uint32) *WaitPaymentCompleteResponse {
	partid := uint64(parts)
	resp, err := n.rpc.WaitSendPay(n.harness.Ctx, &core_lightning.WaitsendpayRequest{
		PaymentHash: paymentHash,
		Partid:      &partid,
	})
	CheckError(n.harness.T, err)

	return &WaitPaymentCompleteResponse{
		PaymentHash:     resp.PaymentHash,
		AmountMsat:      resp.AmountMsat.Msat,
		Destination:     resp.Destination,
		CreatedAt:       resp.CreatedAt,
		AmountSentMsat:  resp.AmountMsat.Msat,
		PaymentPreimage: resp.PaymentPreimage,
	}
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
	Status             InvoiceStatus
}

func (n *CoreLightningNode) GetInvoice(paymentHash []byte) *GetInvoiceResponse {
	resp, err := n.rpc.ListInvoices(n.harness.Ctx, &core_lightning.ListinvoicesRequest{
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
		Status:             InvoiceStatus(invoice.Status),
	}
}

func (n *CoreLightningNode) TearDown() error {
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

func getTimeoutSeconds(t *testing.T, timeout time.Time) uint32 {
	timeoutSeconds := time.Until(timeout).Seconds()
	if timeoutSeconds < 0 {
		CheckError(t, fmt.Errorf("timeout expired"))
	}

	return uint32(timeoutSeconds)
}

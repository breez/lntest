package lntest

import (
	"bufio"
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/hex"
	"fmt"
	"io"
	"io/ioutil"
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

func NewCoreLightningNode(h *TestHarness, m *Miner, name string, extraArgs ...string) *CoreLightningNode {
	lightningdDir, err := ioutil.TempDir(h.Dir, fmt.Sprintf("ld-%s", name))
	CheckError(h.T, err)

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
		log.Printf("Starting stderr scanner")
		scanner := bufio.NewScanner(stderr)
		for scanner.Scan() {
			log.Println(name + ": " + scanner.Text())
		}
	}()

	go func() {
		// print any stderr output to the test log
		log.Printf("Starting stdout scanner")
		scanner := bufio.NewScanner(stdout)
		for scanner.Scan() {
			log.Println(name + ": " + scanner.Text())
		}
	}()

	go func() {
		err := cmd.Wait()
		if err != nil && err.Error() != "signal: interrupt" {
			log.Printf(name+": "+"lightningd exited with error %s", err)
		}
		log.Printf(name + ": " + "process exited normally")
	}()

	regtestDir := filepath.Join(lightningdDir, "regtest")
	waitForLog(h.T, filepath.Join(regtestDir, "log"), "Server started with public key", 30)

	pemServerCA, err := ioutil.ReadFile(filepath.Join(regtestDir, "ca.pem"))
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
	h.AddCleanable(node)
	h.AddLogfile(filepath.Join(regtestDir, "log"))

	return node
}

func waitForLog(t *testing.T, logfilePath string, phrase string, timeoutSec int) {
	timeout := time.Now().Add(time.Duration(timeoutSec) * time.Second)

	// at startup we need to wait for the file to open
	for time.Now().Before(timeout) || timeoutSec == 0 {
		if _, err := os.Stat(logfilePath); os.IsNotExist(err) {
			time.Sleep(100 * time.Millisecond)
			continue
		}
		break
	}
	logfile, _ := os.Open(logfilePath)
	defer logfile.Close()

	reader := bufio.NewReader(logfile)
	for timeoutSec == 0 || time.Now().Before(timeout) {
		line, err := reader.ReadString('\n')
		if err != nil {
			if err == io.EOF {
				time.Sleep(100 * time.Millisecond)
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

func (n *CoreLightningNode) WaitForSync() {
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
		time.Sleep(100 * time.Millisecond)
	}
}

func (n *CoreLightningNode) Fund(amountSat uint64) {
	addrResponse, err := n.rpc.NewAddr(
		context.Background(),
		&core_lightning.NewaddrRequest{
			Addresstype: core_lightning.NewaddrRequest_BECH32.Enum(),
		},
	)
	CheckError(n.harness.T, err)

	n.miner.SendToAddress(*addrResponse.Bech32, amountSat)
}

type OpenChannelOptions struct {
	AmountSat uint64
	FeePerKw  uint
}

func (n *CoreLightningNode) ConnectPeer(nodeId []byte, host *string, port *uint32) {
	_, err := n.rpc.ConnectPeer(n.harness.Ctx, &core_lightning.ConnectRequest{
		Id:   hex.EncodeToString(nodeId),
		Host: host,
		Port: port,
	})
	CheckError(n.harness.T, err)
}

func (n *CoreLightningNode) OpenChannel(peer *CoreLightningNode, options *OpenChannelOptions) *ChannelInfo {
	peerInfo, err := peer.rpc.Getinfo(n.harness.Ctx, &core_lightning.GetinfoRequest{})
	CheckError(n.harness.T, err)

	n.ConnectPeer(peerInfo.Id, peer.host, peer.port)

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
	PaymentHash []byte
}

func (n *CoreLightningNode) Pay(bolt11 string) *PayResult {
	resp, err := n.rpc.Pay(n.harness.Ctx, &core_lightning.PayRequest{
		Bolt11: bolt11,
	})
	CheckError(n.harness.T, err)

	return &PayResult{
		PaymentHash: resp.PaymentHash,
	}
}

func (n *CoreLightningNode) WaitPaymentComplete(paymentHash []byte) {
	_, err := n.rpc.WaitSendPay(n.harness.Ctx, &core_lightning.WaitsendpayRequest{
		PaymentHash: paymentHash,
	})
	CheckError(n.harness.T, err)
}

func (n *CoreLightningNode) TearDown() error {
	if n.cmd == nil || n.cmd.Process == nil {
		// return if not properly initialized
		// or error starting the process
		return nil
	}

	defer n.cmd.Wait()
	if runtime.GOOS == "windows" {
		return n.cmd.Process.Signal(os.Kill)
	}

	return n.cmd.Process.Signal(os.Interrupt)
}

func (n *CoreLightningNode) Cleanup() error {
	return os.RemoveAll(n.dir)
}

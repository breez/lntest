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

type LightningNode struct {
	name     string
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

func NewCoreLightningNode(h *TestHarness, m *Miner, name string) *LightningNode {
	lightningdDir, err := ioutil.TempDir(h.dir, fmt.Sprintf("lightningd-%s", name))
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
		"--funding-confirms=3",
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

	cmd := exec.CommandContext(h.ctx, binary, args...)
	stderr, err := cmd.StderrPipe()
	CheckError(h.T, err)

	stdout, err := cmd.StdoutPipe()
	CheckError(h.T, err)

	log.Printf("starting %s on port %d in dir %s...", binary, port, lightningdDir)
	err = cmd.Start()
	CheckError(h.T, err)

	go func() {
		// print any stderr output to the test log
		log.Printf("Starting stderr scanner")
		scanner := bufio.NewScanner(stderr)
		for scanner.Scan() {
			log.Println(scanner.Text())
		}
	}()

	go func() {
		// print any stderr output to the test log
		log.Printf("Starting stdout scanner")
		scanner := bufio.NewScanner(stdout)
		for scanner.Scan() {
			log.Println(scanner.Text())
		}
	}()

	go func() {
		err := cmd.Wait()
		if err != nil && err.Error() != "signal: interrupt" {
			log.Printf("lightningd exited with error %s", err)
		}
		log.Printf("process exited normally")
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

	node := &LightningNode{
		name:     name,
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

	h.mtx.Lock()
	defer h.mtx.Unlock()
	h.nodes[name] = node

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

func (n *LightningNode) WaitForSync() {
	for {
		info, _ := n.rpc.Getinfo(n.harness.ctx, &core_lightning.GetinfoRequest{})

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

func (n *LightningNode) Fund(amountSat uint64) {
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

func (n *LightningNode) OpenChannel(peer *LightningNode, options *OpenChannelOptions) *ChannelInfo {
	peerInfo, err := peer.rpc.Getinfo(n.harness.ctx, &core_lightning.GetinfoRequest{})
	CheckError(n.harness.T, err)

	peerId, err := n.rpc.ConnectPeer(n.harness.ctx, &core_lightning.ConnectRequest{
		Id:   hex.EncodeToString(peerInfo.Id),
		Host: peer.host,
		Port: peer.port,
	})
	CheckError(n.harness.T, err)

	feePerKw := options.FeePerKw
	if feePerKw == 0 {
		feePerKw = 253
	}

	// open a channel
	announce := true
	fundResult, err := n.rpc.FundChannel(n.harness.ctx, &core_lightning.FundchannelRequest{
		Id: peerId.Id,
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

func (n *LightningNode) TearDown() error {
	if err := n.stop(); err != nil {
		return err
	}

	if err := n.cleanup(); err != nil {
		return err
	}

	return nil
}

func (n *LightningNode) stop() error {
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

func (n *LightningNode) cleanup() error {
	return os.RemoveAll(n.dir)
}

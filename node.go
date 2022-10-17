package lntest

import (
	"bufio"
	"fmt"
	"io/ioutil"
	"log"
	"os/exec"
	"time"

	"github.com/niftynei/glightning/glightning"
)

type LightningNode struct {
	name    string
	harness *TestHarness
	miner   *Miner
	cmd     *exec.Cmd
	rpc     *glightning.Lightning
	rpcPort int
}

func NewCoreLightningNode(h *TestHarness, m *Miner, binary string, name string) *LightningNode {
	lightningdDir, err := ioutil.TempDir(h.dir, fmt.Sprintf("lightningd-%s", name))
	CheckError(h.T, err)

	rpcPort, err := GetPort()
	CheckError(h.T, err)

	args := []string{
		"--network=regtest",
		"--funding-confirms=3",
		"--log-file=log",
		"--log-level=debug",
		"--bitcoin-rpcuser=btcuser",
		"--bitcoin-rpcpassword=btcpass",
		"--dev-fast-gossip",
		"--dev-bitcoind-poll=1",
		"--allow-deprecated-apis=false",
		fmt.Sprintf("--lightning-dir=%s", lightningdDir),
		fmt.Sprintf("--bitcoin-datadir=%s", m.dir),
		fmt.Sprintf("--addr=localhost:%d", rpcPort),
		fmt.Sprintf("--bitcoin-rpcport=%d", m.rpcPort),
	}

	cmd := exec.CommandContext(h.ctx, binary, args...)
	stderr, err := cmd.StderrPipe()
	CheckError(h.T, err)

	stdout, err := cmd.StdoutPipe()
	CheckError(h.T, err)

	log.Printf("starting %s on %d...", binary, rpcPort)
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
		if err != nil {
			h.T.Fatalf("lightningd exited with error %s", err)
		}
		log.Printf("process exited normally")
	}()

	rpc := glightning.NewLightning()
	rpc.StartUp("lightning-rpc", lightningdDir)

	node := &LightningNode{
		name:    name,
		miner:   m,
		cmd:     cmd,
		rpc:     rpc,
		rpcPort: rpcPort,
	}

	h.mtx.Lock()
	defer h.mtx.Unlock()
	h.nodes[name] = node

	return node
}

func (n *LightningNode) WaitForSync() {
	for {
		info, _ := n.rpc.GetInfo()
		if info.IsLightningdSync() {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
}

func (n *LightningNode) Fund(amountSat uint64) {
	addr, err := n.rpc.NewAddr()
	CheckError(n.harness.T, err)

	n.miner.SendToAddress(addr, amountSat)
}

type OpenChannelOptions struct {
	AmountSat uint64
	FeePerKw  uint
}

func (n *LightningNode) OpenChannel(peer *LightningNode, options *OpenChannelOptions) *ChannelInfo {
	peerInfo, err := peer.rpc.GetInfo()
	CheckError(n.harness.T, err)

	peerId, err := n.rpc.Connect(peerInfo.Id, "localhost", uint(peer.rpcPort))
	CheckError(n.harness.T, err)

	feePerKw := options.FeePerKw
	if feePerKw == 0 {
		feePerKw = 253
	}

	// open a channel
	amount := &glightning.Sat{Value: options.AmountSat}
	feerate := glightning.NewFeeRate(glightning.PerKw, feePerKw)
	fundResult, err := n.rpc.FundChannelExt(peerId, amount, feerate, true, nil, nil)
	CheckError(n.harness.T, err)

	return &ChannelInfo{
		From:        n,
		To:          peer,
		FundingTx:   fundResult.FundingTx,
		FundingTxId: fundResult.FundingTxId,
		ChannelId:   fundResult.ChannelId,
	}
}

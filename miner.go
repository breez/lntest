package lntest

import (
	"fmt"
	"io/ioutil"
	"log"
	"os"
	"os/exec"
	"runtime"
	"strconv"

	"github.com/niftynei/glightning/gbitcoin"
)

type Miner struct {
	harness *TestHarness
	dir     string
	rpc     *gbitcoin.Bitcoin
	rpcPort int
	cmd     *exec.Cmd
}

func NewMiner(h *TestHarness) *Miner {
	btcUser := "btcuser"
	btcPass := "btcpass"
	bitcoindDir, err := ioutil.TempDir(h.dir, "miner-")
	CheckError(h.T, err)

	rpcPort, err := GetPort()
	CheckError(h.T, err)

	binary, err := GetBitcoindBinary()
	CheckError(h.T, err)

	args := []string{
		"-regtest",
		"-server",
		"-logtimestamps",
		"-nolisten",
		"-addresstype=bech32",
		"-txindex",
		fmt.Sprintf("-datadir=%s", bitcoindDir),
		fmt.Sprintf("-rpcport=%d", rpcPort),
		fmt.Sprintf("-rpcpassword=%s", btcPass),
		fmt.Sprintf("-rpcuser=%s", btcUser),
	}

	log.Printf("starting %s on rpc port %d...", binary, rpcPort)
	cmd := exec.CommandContext(h.ctx, binary, args...)

	err = cmd.Start()
	CheckError(h.T, err)
	log.Printf("bitcoind started (%d)!", cmd.Process.Pid)

	rpc := gbitcoin.NewBitcoin(btcUser, btcPass)
	rpc.SetTimeout(uint(2))
	rpc.StartUp("", bitcoindDir, uint(rpcPort))

	// Go ahead and run 50 blocks
	addr, err := rpc.GetNewAddress(gbitcoin.Bech32)
	CheckError(h.T, err)
	_, err = rpc.GenerateToAddress(addr, 101)
	CheckError(h.T, err)

	miner := &Miner{
		harness: h,
		dir:     bitcoindDir,
		cmd:     cmd,
		rpc:     rpc,
		rpcPort: rpcPort,
	}

	h.mtx.Lock()
	defer h.mtx.Unlock()
	h.miners = append(h.miners, miner)

	return miner
}

func (m *Miner) MineBlocks(n uint) {
	addr, err := m.rpc.GetNewAddress(gbitcoin.Bech32)
	CheckError(m.harness.T, err)
	_, err = m.rpc.GenerateToAddress(addr, n)
	CheckError(m.harness.T, err)
}

func (m *Miner) SendToAddress(addr string, amountSat uint64) {
	_, err := m.rpc.SendToAddress(addr, strconv.FormatUint(amountSat, 10))
	CheckError(m.harness.T, err)

	m.MineBlocks(1)
}

func (m *Miner) TearDown() error {
	if err := m.stop(); err != nil {
		return err
	}

	if err := m.cleanup(); err != nil {
		return err
	}

	return nil
}

func (m *Miner) stop() error {
	if m.cmd == nil || m.cmd.Process == nil {
		// return if not properly initialized
		// or error starting the process
		return nil
	}

	defer m.cmd.Wait()
	if runtime.GOOS == "windows" {
		return m.cmd.Process.Signal(os.Kill)
	}

	return m.cmd.Process.Signal(os.Interrupt)
}

func (m *Miner) cleanup() error {
	return os.RemoveAll(m.dir)
}

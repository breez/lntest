package lntest

import (
	"flag"
	"net"
	"os/exec"
	"runtime/debug"
	"testing"
)

var (
	bitcoindExecutable = flag.String(
		"bitcoindexec", "", "full path to bitcoind binary",
	)
	bitcoinCliExecutable = flag.String(
		"bitcoincliexec", "", "full path to bitcoin-cli binary",
	)
	lightningdExecutable = flag.String(
		"lightningdexec", "", "full path to lightningd binary",
	)
)

func CheckError(t *testing.T, err error) {
	if err != nil {
		debug.PrintStack()
		t.Fatal(err)
	}
}

func GetPort() (uint32, error) {
	addr, err := net.ResolveTCPAddr("tcp", "localhost:0")
	if err != nil {
		return 0, err
	}
	l, err := net.ListenTCP("tcp", addr)
	if err != nil {
		return 0, err
	}
	defer l.Close()
	return uint32(l.Addr().(*net.TCPAddr).Port), nil
}

func GetBitcoindBinary() (string, error) {
	if bitcoindExecutable != nil {
		return *bitcoindExecutable, nil
	}

	return exec.LookPath("bitcoind")
}

func GetBitcoinCliBinary() (string, error) {
	if bitcoinCliExecutable != nil {
		return *bitcoinCliExecutable, nil
	}

	return exec.LookPath("bitcoin-cli")
}

func GetLightningdBinary() (string, error) {
	if lightningdExecutable != nil {
		return *lightningdExecutable, nil
	}

	return exec.LookPath("lightningd")
}

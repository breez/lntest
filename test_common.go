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
)

func CheckError(t *testing.T, err error) {
	if err != nil {
		debug.PrintStack()
		t.Fatal(err)
	}
}

func GetPort() (int, error) {
	addr, err := net.ResolveTCPAddr("tcp", "localhost:0")
	if err != nil {
		return 0, err
	}
	l, err := net.ListenTCP("tcp", addr)
	if err != nil {
		return 0, err
	}
	defer l.Close()
	return l.Addr().(*net.TCPAddr).Port, nil
}

func GetBitcoindBinary() (string, error) {
	if bitcoindExecutable != nil {
		return *bitcoindExecutable, nil
	}

	return exec.LookPath("bitcoind")
}

package lntest

import (
	"flag"
	"fmt"
	"io/ioutil"
	"net"
	"os"
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
	testDir = flag.String(
		"testdir", "", "full path to the root testing directory",
	)
	preserveLogs = flag.Bool(
		"preservelogs", false, "value indicating whether the logs of artifacts should be preserved",
	)
	preserveState = flag.Bool(
		"preservestate", false, "value indicating whether all artifact state should be preserved",
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

func GetTestRootDir() (*string, error) {
	dir := testDir
	if dir == nil || *dir == "" {
		pathDir, err := exec.LookPath("testdir")
		if err != nil {
			dir = &pathDir
		}
	}

	if dir == nil || *dir == "" {
		tempDir, err := ioutil.TempDir("", "lntest")
		return &tempDir, err
	}

	info, err := os.Stat(*dir)
	if err != nil {
		// Create the dir if it doesn't exist
		err = os.MkdirAll(*dir, os.ModePerm)
		if err != nil {
			return nil, err
		}

		return dir, nil
	}

	// dir exists, make sure it's a directory.
	if !info.IsDir() {
		return nil, fmt.Errorf("TestDir '%s' exists but is not a directory", *dir)
	}

	return dir, nil
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

func GetPreserveLogs() bool {
	return *preserveLogs
}

func GetPreserveState() bool {
	return *preserveState
}

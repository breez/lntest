package lntest

import (
	"context"
	"io/ioutil"
	"os"
	"sync"
	"testing"
)

type TestHarness struct {
	*testing.T
	ctx    context.Context
	cancel context.CancelFunc
	dir    string
	mtx    sync.RWMutex
	miners []*Miner
	nodes  map[string]*LightningNode
}

func NewTestHarness(t *testing.T) *TestHarness {
	testDir, err := ioutil.TempDir("", "lntest-")
	CheckError(t, err)

	ctx, cancel := context.WithCancel(context.Background())
	return &TestHarness{
		T:      t,
		ctx:    ctx,
		cancel: cancel,
		dir:    testDir,
		nodes:  make(map[string]*LightningNode),
	}
}

func (h *TestHarness) TearDown() error {
	h.mtx.Lock()
	defer h.mtx.Unlock()

	h.cancel()
	for _, miner := range h.miners {
		if err := miner.TearDown(); err != nil {
			return err
		}
	}

	if err := h.cleanup(); err != nil {
		return err
	}

	return nil
}

func (h *TestHarness) cleanup() error {
	return os.RemoveAll(h.dir)
}

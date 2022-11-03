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
	Ctx    context.Context
	cancel context.CancelFunc
	Dir    string
	mtx    sync.RWMutex
	miners []*Miner
	nodes  map[string]LightningNode
}

func NewTestHarness(t *testing.T) *TestHarness {
	testDir, err := ioutil.TempDir("", "lt-")
	CheckError(t, err)

	ctx, cancel := context.WithCancel(context.Background())
	return &TestHarness{
		T:      t,
		Ctx:    ctx,
		cancel: cancel,
		Dir:    testDir,
		nodes:  make(map[string]LightningNode),
	}
}

func (h *TestHarness) TearDown() error {
	h.mtx.Lock()
	defer h.mtx.Unlock()

	for _, node := range h.nodes {
		if err := node.TearDown(); err != nil {
			return err
		}
	}

	for _, miner := range h.miners {
		if err := miner.TearDown(); err != nil {
			return err
		}
	}

	if err := h.cleanup(); err != nil {
		return err
	}

	h.cancel()
	return nil
}

func (h *TestHarness) cleanup() error {
	return os.RemoveAll(h.Dir)
}

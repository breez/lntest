package lntest

import (
	"context"
	"io/ioutil"
	"log"
	"os"
	"sync"
	"testing"

	"go.uber.org/multierr"
)

type TestHarness struct {
	*testing.T
	Ctx        context.Context
	cancel     context.CancelFunc
	Dir        string
	mtx        sync.RWMutex
	stoppables []Stoppable
}

type Stoppable interface {
	TearDown() error
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
	}
}

func (h *TestHarness) AddStoppable(stoppable Stoppable) {
	h.mtx.Lock()
	defer h.mtx.Unlock()
	h.stoppables = append(h.stoppables, stoppable)
}

func (h *TestHarness) TearDown() error {
	h.mtx.Lock()
	defer h.mtx.Unlock()

	var err error = nil
	for _, stoppable := range h.stoppables {
		err = multierr.Append(err, stoppable.TearDown())
	}

	err = multierr.Append(err, h.cleanup())

	h.cancel()
	if err != nil {
		log.Printf("Harness teardown had errors: %+v", err)
	}
	return err
}

func (h *TestHarness) cleanup() error {
	return os.RemoveAll(h.Dir)
}

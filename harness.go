package lntest

import (
	"context"
	"io/ioutil"
	"log"
	"os"
	"strings"
	"sync"
	"testing"

	"go.uber.org/multierr"
	"golang.org/x/exp/slices"
)

type TestHarness struct {
	*testing.T
	Ctx        context.Context
	cancel     context.CancelFunc
	Dir        string
	mtx        sync.RWMutex
	stoppables []Stoppable
	cleanables []Cleanable
	logFiles   []string
	dumpLogs   bool
}

type Stoppable interface {
	TearDown() error
}

type Cleanable interface {
	Cleanup() error
}

type HarnessOption int

const (
	DumpLogs HarnessOption = 0
)

func NewTestHarness(t *testing.T, options ...HarnessOption) *TestHarness {
	testDir, err := ioutil.TempDir("", "lt-")
	CheckError(t, err)

	ctx, cancel := context.WithCancel(context.Background())
	return &TestHarness{
		T:        t,
		Ctx:      ctx,
		cancel:   cancel,
		Dir:      testDir,
		dumpLogs: slices.Contains(options, DumpLogs),
	}
}

func (h *TestHarness) AddStoppable(stoppable Stoppable) {
	h.mtx.Lock()
	defer h.mtx.Unlock()
	h.stoppables = append(h.stoppables, stoppable)
}

func (h *TestHarness) AddLogfile(logfile string) {
	h.mtx.Lock()
	defer h.mtx.Unlock()
	h.logFiles = append(h.logFiles, logfile)
}

func (h *TestHarness) AddCleanable(cleanable Cleanable) {
	h.mtx.Lock()
	defer h.mtx.Unlock()
	h.cleanables = append(h.cleanables, cleanable)
}

func (h *TestHarness) TearDown() error {
	h.mtx.Lock()
	defer h.mtx.Unlock()

	var err error = nil
	for _, stoppable := range h.stoppables {
		err = multierr.Append(err, stoppable.TearDown())
	}

	if h.dumpLogs {
		for _, logFile := range h.logFiles {
			var sb strings.Builder
			sb.WriteString("*********************************************************\n")
			sb.WriteString("Log dump for ")
			sb.WriteString(logFile)
			sb.WriteString("\n")
			sb.WriteString("*****************************************************************************\n")
			content, err := os.ReadFile(logFile)
			if err == nil {
				sb.Write(content)
			}
			sb.WriteString("\n")
			sb.WriteString("*****************************************************************************\n")
			sb.WriteString("End log dump for ")
			sb.WriteString(logFile)
			sb.WriteString("\n")
			sb.WriteString("*****************************************************************************\n")
			log.Print(sb.String())
		}
	}

	for _, cleanable := range h.cleanables {
		err = multierr.Append(err, cleanable.Cleanup())
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

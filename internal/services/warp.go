package services

import (
	"bytes"
	"fmt"
	"os/exec"
	"strings"
	"sync"
	"time"
)

type WarpStatus string

const (
	WarpConnected    WarpStatus = "Connected"
	WarpDisconnected WarpStatus = "Disconnected"
	WarpUnknown      WarpStatus = "Unknown"
)

type WarpService struct {
	mu sync.Mutex
}

func NewWarpService() *WarpService {
	return &WarpService{}
}

func (w *WarpService) Status() WarpStatus {
	out, err := warpRun("status")
	if err != nil {
		return WarpUnknown
	}
	if strings.Contains(out, "Connected") {
		return WarpConnected
	}
	return WarpDisconnected
}

func (w *WarpService) Disconnect() error {
	_, err := warpRun("disconnect")
	if err != nil {
		return fmt.Errorf("warp-cli disconnect: %w", err)
	}
	return w.waitForStatus(WarpDisconnected, 10*time.Second)
}

func (w *WarpService) Connect() error {
	_, err := warpRun("connect")
	if err != nil {
		return fmt.Errorf("warp-cli connect: %w", err)
	}
	return w.waitForStatus(WarpConnected, 15*time.Second)
}

// WithDisconnected disconnects WARP, runs fn, then always reconnects.
// Mutex ensures only one WARP toggle at a time.
func (w *WarpService) WithDisconnected(fn func() error) error {
	w.mu.Lock()
	defer w.mu.Unlock()

	if err := w.Disconnect(); err != nil {
		return fmt.Errorf("warp disconnect failed: %w", err)
	}

	fnErr := fn()

	if err := w.Connect(); err != nil {
		// Log but don't override fnErr
		fmt.Printf("[warp] reconnect failed: %v\n", err)
	}

	return fnErr
}

func (w *WarpService) waitForStatus(want WarpStatus, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if w.Status() == want {
			return nil
		}
		time.Sleep(500 * time.Millisecond)
	}
	return fmt.Errorf("timeout waiting for WARP status %q", want)
}

func warpRun(args ...string) (string, error) {
	cmd := exec.Command("warp-cli", args...)
	var out bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &out
	err := cmd.Run()
	return out.String(), err
}

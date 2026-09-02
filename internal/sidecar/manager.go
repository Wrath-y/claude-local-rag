package sidecar

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/exec"
	"runtime"
	"sync"
	"syscall"
	"time"
)

// Config holds sidecar manager configuration.
type Config struct {
	Provider        string // "local" means we need sidecar
	Port            int
	PythonPath      string // path to sidecar/main.py
	HealthInterval  time.Duration
	HealthRetries   int
	StartupTimeout  time.Duration
	ShutdownTimeout time.Duration // graceful-stop budget; defaults to 5s
}

// Manager manages the lifecycle of the Python sidecar process.
type Manager struct {
	cfg     Config
	cmd     *exec.Cmd
	mu      sync.Mutex
	running bool
	stopCh  chan struct{}
}

// New creates a new sidecar Manager.
func New(cfg Config) *Manager {
	return &Manager{
		cfg:    cfg,
		stopCh: make(chan struct{}),
	}
}

// Start launches the sidecar if provider=="local". No-op otherwise.
func (m *Manager) Start() error {
	if m.cfg.Provider != "local" {
		return nil
	}

	if err := m.launch(); err != nil {
		return fmt.Errorf("sidecar launch: %w", err)
	}

	// Wait for /health to respond 200.
	if err := m.waitHealthy(m.cfg.StartupTimeout); err != nil {
		m.killProcess()
		return fmt.Errorf("sidecar startup health check: %w", err)
	}

	m.mu.Lock()
	m.running = true
	m.stopCh = make(chan struct{})
	m.mu.Unlock()

	go m.healthLoop()
	return nil
}

// launch starts the subprocess.
func (m *Manager) launch() error {
	m.mu.Lock()
	defer m.mu.Unlock()

	pythonPath := m.cfg.PythonPath
	if pythonPath == "" {
		pythonPath = "sidecar/main.py"
	}

	cmd := exec.Command(pythonExecutable(runtime.GOOS), pythonPath, "--port", fmt.Sprintf("%d", m.cfg.Port))
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Start(); err != nil {
		return err
	}
	m.cmd = cmd
	return nil
}

func pythonExecutable(goos string) string {
	if goos == "windows" {
		return "python"
	}
	return "python3"
}

// waitHealthy polls /health until it returns 200 or the timeout elapses.
func (m *Manager) waitHealthy(timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	ticker := time.NewTicker(500 * time.Millisecond)
	defer ticker.Stop()

	for {
		select {
		case <-ticker.C:
			if m.checkHealth() {
				return nil
			}
			if time.Now().After(deadline) {
				return fmt.Errorf("timed out after %s waiting for sidecar health", timeout)
			}
		}
	}
}

// checkHealth performs a single /health GET, returns true on HTTP 200.
func (m *Manager) checkHealth() bool {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, m.URL()+"/health", nil)
	if err != nil {
		return false
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return false
	}
	resp.Body.Close()
	return resp.StatusCode == http.StatusOK
}

// healthLoop monitors the sidecar and restarts it on consecutive failures.
func (m *Manager) healthLoop() {
	interval := m.cfg.HealthInterval
	if interval == 0 {
		interval = 10 * time.Second
	}
	retries := m.cfg.HealthRetries
	if retries == 0 {
		retries = 3
	}

	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	failures := 0
	for {
		select {
		case <-m.stopCh:
			return
		case <-ticker.C:
			if m.checkHealth() {
				failures = 0
				continue
			}
			failures++
			slog.Warn("sidecar health check failed", "consecutive_failures", failures)
			if failures >= retries {
				slog.Error("sidecar unresponsive, restarting", "failures", failures)
				m.restart()
				failures = 0
			}
		}
	}
}

// restart kills the current process and launches a new one.
func (m *Manager) restart() {
	m.killProcess()

	if err := m.launch(); err != nil {
		slog.Error("sidecar restart failed", "err", err)
		m.mu.Lock()
		m.running = false
		m.mu.Unlock()
		return
	}

	timeout := m.cfg.StartupTimeout
	if timeout == 0 {
		timeout = 30 * time.Second
	}
	if err := m.waitHealthy(timeout); err != nil {
		slog.Error("sidecar restart health check failed", "err", err)
		m.killProcess()
		m.mu.Lock()
		m.running = false
		m.mu.Unlock()
		return
	}

	slog.Info("sidecar restarted successfully")
}

// killProcess sends SIGKILL to the subprocess if it is set.
// Used for restart() and startup-failure cleanup where immediate
// termination is intentional (no graceful-stop semantics needed).
func (m *Manager) killProcess() {
	m.mu.Lock()
	defer m.mu.Unlock()

	if m.cmd != nil && m.cmd.Process != nil {
		_ = m.cmd.Process.Kill()
		_ = m.cmd.Wait()
		m.cmd = nil
	}
	m.running = false
}

// stopGracefully sends SIGTERM and waits up to ShutdownTimeout for the
// process to exit on its own.  If the process has not exited within the
// timeout, SIGKILL is sent and we wait for the reap.
//
// The method acquires m.mu only to snapshot and clear m.cmd so that no
// concurrent caller (e.g. healthLoop→restart) can double-Wait the same Cmd.
func (m *Manager) stopGracefully() {
	timeout := m.cfg.ShutdownTimeout
	if timeout <= 0 {
		timeout = 5 * time.Second
	}

	// Snapshot and detach cmd under the lock so no other goroutine races
	// a Wait on the same Cmd.
	m.mu.Lock()
	cmd := m.cmd
	m.cmd = nil
	m.running = false
	m.mu.Unlock()

	if cmd == nil || cmd.Process == nil {
		return
	}

	// Send SIGTERM; ignore "process already finished" errors.
	_ = cmd.Process.Signal(syscall.SIGTERM)

	// Asynchronously wait; use a channel so we can select with a timer.
	waitDone := make(chan error, 1)
	go func() { waitDone <- cmd.Wait() }()

	select {
	case <-waitDone:
		// Process exited cooperatively within the timeout.
	case <-time.After(timeout):
		// Timeout: escalate to SIGKILL.
		slog.Warn("sidecar did not exit after SIGTERM; sending SIGKILL",
			"shutdown_timeout", timeout)
		_ = cmd.Process.Kill()
		<-waitDone // must reap to avoid zombie
	}
}

// Stop gracefully stops the sidecar and halts the health loop.
// It is idempotent: calling it on a non-running Manager is a no-op.
func (m *Manager) Stop() {
	m.mu.Lock()
	ch := m.stopCh
	m.mu.Unlock()

	// Signal health loop to stop (non-blocking in case it was never started).
	select {
	case <-ch:
		// already closed
	default:
		close(ch)
	}

	m.stopGracefully()
}

// Running returns whether the sidecar is considered alive.
func (m *Manager) Running() bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.running
}

// URL returns the sidecar base URL.
func (m *Manager) URL() string {
	return fmt.Sprintf("http://127.0.0.1:%d", m.cfg.Port)
}

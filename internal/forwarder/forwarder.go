// Package forwarder implements the TCP port forwarding engine.
//
// Each Forwarder binds to a local listen address and relays traffic to a remote target.
// The Manager orchestrates multiple forwarders, handles graceful shutdown,
// and supports configuration hot-reload.
package forwarder

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"sync"
	"sync/atomic"
	"syscall"
	"time"

	"go.uber.org/zap"

	"github.com/Hopetree/mini-forwarder/internal/config"
	"github.com/Hopetree/mini-forwarder/internal/logger"
)

// Forwarder manages a single TCP listen port and forwards all accepted
// connections to a configured target address.
type Forwarder struct {
	name  string
	listen string
	target string

	listener net.Listener
	cancel   context.CancelFunc
	connWG   sync.WaitGroup // tracks active client connections
	conns    sync.Map       // tracks active connections for forced close on shutdown

	activeConn atomic.Int64 // gauge: number of connections currently being relayed
	totalConn  atomic.Int64 // counter: total connections accepted since start

	// Mutable config accessed by handleConn goroutines.
	// Protected by cfgMu for safe concurrent reads/writes during Reload.
	cfgMu       sync.RWMutex
	dialTimeout time.Duration
	idleTimeout time.Duration
	maxRetries  int
	retryDelay  time.Duration
	keepAlive   time.Duration

	// stopOnce ensures stop() can be called multiple times safely
	// (e.g. during Reload when the same forwarder name is replaced).
	stopOnce sync.Once

	log *zap.Logger
}

// getDialTimeout returns the dial timeout, safe for concurrent reads.
func (f *Forwarder) getDialTimeout() time.Duration {
	f.cfgMu.RLock()
	defer f.cfgMu.RUnlock()
	return f.dialTimeout
}

func (f *Forwarder) getIdleTimeout() time.Duration {
	f.cfgMu.RLock()
	defer f.cfgMu.RUnlock()
	return f.idleTimeout
}

func (f *Forwarder) getMaxRetries() int {
	f.cfgMu.RLock()
	defer f.cfgMu.RUnlock()
	return f.maxRetries
}

func (f *Forwarder) getRetryDelay() time.Duration {
	f.cfgMu.RLock()
	defer f.cfgMu.RUnlock()
	return f.retryDelay
}

func (f *Forwarder) getKeepAlive() time.Duration {
	f.cfgMu.RLock()
	defer f.cfgMu.RUnlock()
	return f.keepAlive
}

// updateConfig atomically replaces the mutable config fields.
// Called during Reload with m.mu held; handleConn reads via RLock.
func (f *Forwarder) updateConfig(rule config.ForwardRule) {
	f.cfgMu.Lock()
	defer f.cfgMu.Unlock()
	f.dialTimeout = rule.DialTimeout
	f.idleTimeout = rule.GetIdleTimeout()
	f.maxRetries = rule.MaxRetries
	f.retryDelay = rule.RetryInterval
	f.keepAlive = rule.KeepAlive
}

// Manager manages the lifecycle of multiple Forwarder instances and
// supports hot-reload of forwarding rules.
type Manager struct {
	mu         sync.RWMutex
	forwarders map[string]*Forwarder
	config     *config.Config
	log        *zap.Logger
	ctx        context.Context
	cancel     context.CancelFunc
}

// NewManager creates a Manager with the given configuration.
func NewManager(cfg *config.Config) *Manager {
	return &Manager{
		forwarders: make(map[string]*Forwarder),
		config:     cfg,
		log:        logger.L.Named("manager"),
	}
}

// Start launches all forwarders defined in the configuration.
// It returns an error if any forwarder fails to bind.
func (m *Manager) Start(ctx context.Context) error {
	m.ctx, m.cancel = context.WithCancel(ctx)

	for _, rule := range m.config.Forwards {
		if err := m.startForwarder(rule); err != nil {
			// Roll back: stop any already-started forwarders.
			m.StopAll()
			return fmt.Errorf("start forwarder %q: %w", rule.Name, err)
		}
	}

	return nil
}

// startForwarder creates and starts a single Forwarder from the given rule.
// It is safe to call without holding m.mu; callers that already hold the lock
// should use addForwarderLocked instead.
func (m *Manager) startForwarder(rule config.ForwardRule) error {
	m.mu.Lock()
	err := m.addForwarderLocked(rule)
	m.mu.Unlock()
	return err
}

// addForwarderLocked creates and starts a Forwarder. Caller must hold m.mu.
func (m *Manager) addForwarderLocked(rule config.ForwardRule) error {
	listener, err := net.Listen("tcp", rule.Listen)
	if err != nil {
		return fmt.Errorf("listen on %s: %w", rule.Listen, err)
	}

	ctx, cancel := context.WithCancel(m.ctx)

	fwd := &Forwarder{
		name:        rule.Name,
		listen:      rule.Listen,
		target:      rule.Target,
		dialTimeout: rule.DialTimeout,
		idleTimeout: rule.GetIdleTimeout(),
		maxRetries:  rule.MaxRetries,
		retryDelay:  rule.RetryInterval,
		keepAlive:   rule.KeepAlive,
		listener:    listener,
		cancel:      cancel,
		log: logger.L.Named("fwd").With(
			zap.String("name", rule.Name),
			zap.String("listen", rule.Listen),
			zap.String("target", rule.Target),
		),
	}

	// If a forwarder with this name already exists, stop the old one first.
	// sync.Once in stop() makes this safe even if already stopping.
	if old, ok := m.forwarders[rule.Name]; ok {
		old.stop()
	}
	m.forwarders[rule.Name] = fwd

	go fwd.acceptLoop(ctx)

	fwd.log.Info("forwarder started")
	return nil
}

// stopForwarder gracefully stops a named forwarder.
func (m *Manager) stopForwarder(name string) {
	m.mu.Lock()
	fwd, ok := m.forwarders[name]
	if !ok {
		m.mu.Unlock()
		return
	}
	delete(m.forwarders, name)
	m.mu.Unlock()

	fwd.stop()
}

// StopAll gracefully shuts down all forwarders.
func (m *Manager) StopAll() {
	m.mu.RLock()
	names := make([]string, 0, len(m.forwarders))
	for name := range m.forwarders {
		names = append(names, name)
	}
	m.mu.RUnlock()

	for _, name := range names {
		m.stopForwarder(name)
	}

	if m.cancel != nil {
		m.cancel()
	}
}

// Reload applies a new configuration, stopping/starting forwarders as needed.
// Forwarders whose rules are unchanged are left untouched.
func (m *Manager) Reload(newCfg *config.Config) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if m.ctx == nil {
		return fmt.Errorf("manager not started")
	}

	// Build lookup of new rules.
	newRules := make(map[string]config.ForwardRule, len(newCfg.Forwards))
	for _, r := range newCfg.Forwards {
		newRules[r.Name] = r
	}

	// 1. Stop forwarders that are removed or whose listen/target changed.
	for name, fwd := range m.forwarders {
		rule, exists := newRules[name]
		if !exists || rule.Listen != fwd.listen || rule.Target != fwd.target {
			m.log.Info("stopping forwarder for reload",
				zap.String("name", name),
				zap.Bool("removed", !exists),
			)
			delete(m.forwarders, name)
			go fwd.stop() // non-blocking: drain in background (safe via sync.Once)
		} else {
			// Update runtime parameters without restart.
			fwd.updateConfig(rule)
			fwd.log.Info("forwarder config updated (no restart needed)",
				zap.String("name", name),
			)
		}
	}

	// 2. Start new forwarders.
	for _, rule := range newCfg.Forwards {
		if _, exists := m.forwarders[rule.Name]; !exists {
			if err := m.addForwarderLocked(rule); err != nil {
				m.log.Error("failed to start forwarder during reload",
					zap.String("name", rule.Name),
					zap.Error(err),
				)
				// Continue starting others; don't abort the entire reload.
			}
		}
	}

	m.config = newCfg
	return nil
}

// Status returns a snapshot of all forwarders and their metrics.
func (m *Manager) Status() []map[string]interface{} {
	m.mu.RLock()
	defer m.mu.RUnlock()

	result := make([]map[string]interface{}, 0, len(m.forwarders))
	for name, fwd := range m.forwarders {
		result = append(result, map[string]interface{}{
			"name":        name,
			"listen":      fwd.listen,
			"target":      fwd.target,
			"active_conn": fwd.activeConn.Load(),
			"total_conn":  fwd.totalConn.Load(),
		})
	}
	return result
}

const (
	// acceptMaxBackoff is the maximum sleep duration between accept retries.
	acceptMaxBackoff = 1 * time.Second
	// acceptErrorsBeforeWarn is the number of consecutive temporary accept errors
	// before we start logging at warn level (to avoid log spam under EMFILE).
	acceptErrorsBeforeWarn = 5
)

// acceptLoop accepts incoming connections until the context is cancelled.
func (f *Forwarder) acceptLoop(ctx context.Context) {
	backoff := 100 * time.Millisecond
	consecutiveErrors := 0

	for {
		conn, err := f.listener.Accept()
		if err != nil {
			select {
			case <-ctx.Done():
				f.log.Info("accept loop exited")
				return
			default:
			}

			if isTemporaryError(err) {
				consecutiveErrors++
				if consecutiveErrors >= acceptErrorsBeforeWarn {
					f.log.Warn("temporary accept error, backing off",
						zap.Error(err),
						zap.Int("consecutive", consecutiveErrors),
						zap.Duration("backoff", backoff),
					)
				}
				time.Sleep(backoff)
				// Exponential backoff, capped at 1s.
				backoff *= 2
				if backoff > acceptMaxBackoff {
					backoff = acceptMaxBackoff
				}
				continue
			}

			f.log.Error("fatal accept error", zap.Error(err))
			return
		}

		// Reset backoff on successful accept.
		consecutiveErrors = 0
		backoff = 100 * time.Millisecond

		f.connWG.Add(1)
		f.activeConn.Add(1)
		f.totalConn.Add(1)

		go f.handleConn(ctx, conn)
	}
}

// handleConn dials the target (with retries) and relays data bidirectionally.
func (f *Forwarder) handleConn(ctx context.Context, clientConn net.Conn) {
	defer func() {
		clientConn.Close()
		f.conns.Delete(clientConn)
		f.connWG.Done()
		f.activeConn.Add(-1)
	}()

	remoteAddr := clientConn.RemoteAddr().String()
	f.conns.Store(clientConn, struct{}{})

	// Apply TCP tuning on the raw connection (before any wrapping).
	keepAlive := f.getKeepAlive()
	setTCPParams(clientConn, keepAlive)

	f.log.Info("connection accepted",
		zap.String("remote", remoteAddr),
		zap.Int64("active", f.activeConn.Load()),
	)

	// Dial the target with exponential-backoff retries.
	targetConn, err := f.dialWithRetry(ctx, remoteAddr)
	if err != nil {
		f.log.Error("target dial failed after all retries",
			zap.String("remote", remoteAddr),
			zap.Error(err),
		)
		return
	}
	defer targetConn.Close()

	// Apply TCP tuning on the raw target connection.
	setTCPParams(targetConn, keepAlive)

	// Wrap with idle timeout enforcement (reads underlying TCP conn directly).
	idleTimeout := f.getIdleTimeout()
	left := wrapIdleTimeout(clientConn, idleTimeout)
	right := wrapIdleTimeout(targetConn, idleTimeout)

	start := time.Now()
	relay(left, right)

	f.log.Info("connection closed",
		zap.String("remote", remoteAddr),
		zap.Duration("duration", time.Since(start).Truncate(time.Millisecond)),
		zap.Int64("active", f.activeConn.Load()),
	)
}

// dialWithRetry attempts to connect to the target, retrying up to maxRetries
// with exponential backoff. Returns the connection or the last error.
func (f *Forwarder) dialWithRetry(ctx context.Context, remoteAddr string) (net.Conn, error) {
	var lastErr error
	delay := f.getRetryDelay()
	maxRetries := f.getMaxRetries()
	dialTimeout := f.getDialTimeout()

	for attempt := 0; attempt <= maxRetries; attempt++ {
		if attempt > 0 {
			select {
			case <-ctx.Done():
				return nil, ctx.Err()
			case <-time.After(delay):
				// Exponential backoff with a cap of 10s.
				delay = time.Duration(float64(delay) * 1.5)
				if delay > 10*time.Second {
					delay = 10 * time.Second
				}
			}
		}

		targetConn, err := net.DialTimeout("tcp", f.target, dialTimeout)
		if err == nil {
			return targetConn, nil
		}
		lastErr = err

		f.log.Warn("dial target failed",
			zap.String("remote", remoteAddr),
			zap.Int("attempt", attempt+1),
			zap.Int("max", maxRetries+1),
			zap.Duration("next_delay", delay),
			zap.Error(err),
		)
	}

	return nil, fmt.Errorf("after %d attempts: %w", maxRetries+1, lastErr)
}

// stop gracefully stops the forwarder. Safe to call multiple times via sync.Once.
func (f *Forwarder) stop() {
	f.stopOnce.Do(func() {
		f.log.Info("stopping forwarder")

		// Cancel the accept loop context, which unblocks Accept().
		f.cancel()

		// Close the listener so Accept() returns immediately.
		if err := f.listener.Close(); err != nil && !errors.Is(err, net.ErrClosed) {
			f.log.Warn("error closing listener", zap.Error(err))
		}

		// Force-close all active connections so relay goroutines unblock.
		f.conns.Range(func(key, _ any) bool {
			if conn, ok := key.(net.Conn); ok {
				conn.Close()
			}
			f.conns.Delete(key)
			return true
		})

		// Wait for all active connections to drain.
		done := make(chan struct{})
		go func() {
			f.connWG.Wait()
			close(done)
		}()

		select {
		case <-done:
			f.log.Info("forwarder stopped, all connections drained")
		case <-time.After(10 * time.Second):
			f.log.Warn("forwarder stopped with lingering connections (10s timeout)")
		}
	})
}

// relay copies data bidirectionally between two connections.
// When one direction encounters an error or EOF, it performs a TCP half-close
// (CloseWrite) on the destination, allowing the other direction to finish
// gracefully if the protocol supports it.
func relay(left, right net.Conn) {
	var wg sync.WaitGroup
	wg.Add(2)

	copyClose := func(dst, src net.Conn) {
		defer wg.Done()
		_, _ = io.Copy(dst, src)
		// Half-close the write side to signal EOF to the peer.
		// Unwrap idleTimeoutConn to reach the underlying *net.TCPConn.
		if tc, ok := unwrapConn(dst).(*net.TCPConn); ok {
			_ = tc.CloseWrite()
		}
	}

	go copyClose(right, left) // client -> target
	go copyClose(left, right) // target -> client

	wg.Wait()
}

// setTCPParams applies production-suitable TCP parameters to a raw connection.
// Must be called BEFORE wrapIdleTimeout, since this operates on the underlying *net.TCPConn.
func setTCPParams(conn net.Conn, keepAlive time.Duration) {
	tc, ok := unwrapConn(conn).(*net.TCPConn)
	if !ok {
		return
	}
	_ = tc.SetKeepAlive(true)
	_ = tc.SetKeepAlivePeriod(keepAlive)
	_ = tc.SetNoDelay(true) // Disable Nagle's write coalescing for low-latency forwarding.
	// Use the OS default linger behavior (block until data is sent or timeout).
	// Do NOT use SetLinger(0) — it sends RST and discards buffered data.
}

// idleTimeoutConn wraps a net.Conn and sets a read/write deadline before
// every Read/Write call, enforcing an idle timeout.
type idleTimeoutConn struct {
	net.Conn
	timeout time.Duration
}

func (c *idleTimeoutConn) Read(b []byte) (int, error) {
	if c.timeout > 0 {
		if err := c.Conn.SetDeadline(time.Now().Add(c.timeout)); err != nil {
			return 0, err
		}
	}
	return c.Conn.Read(b)
}

func (c *idleTimeoutConn) Write(b []byte) (int, error) {
	if c.timeout > 0 {
		if err := c.Conn.SetDeadline(time.Now().Add(c.timeout)); err != nil {
			return 0, err
		}
	}
	return c.Conn.Write(b)
}

// wrapIdleTimeout returns the original conn if timeout is 0, or wraps it
// with idle timeout enforcement.
func wrapIdleTimeout(conn net.Conn, timeout time.Duration) net.Conn {
	if timeout <= 0 {
		return conn
	}
	return &idleTimeoutConn{Conn: conn, timeout: timeout}
}

// unwrapConn extracts the underlying net.Conn from any wrapper (e.g. idleTimeoutConn).
func unwrapConn(c net.Conn) net.Conn {
	if wrapped, ok := c.(*idleTimeoutConn); ok {
		return wrapped.Conn
	}
	return c
}

// isTemporaryError returns true for transient network errors that should be retried.
// Instead of the deprecated net.Error.Temporary(), we check for common
// temporary error types and opError patterns.
func isTemporaryError(err error) bool {
	var opErr *net.OpError
	if errors.As(err, &opErr) {
		if syscallErr, ok := opErr.Err.(*os.SyscallError); ok {
			switch syscallErr.Err {
			case syscall.EAGAIN, syscall.ENOMEM,
				syscall.ECONNREFUSED, syscall.ECONNRESET, syscall.ETIMEDOUT:
				return true
			}
		}
		// DNS-level temporary errors.
		if _, ok := opErr.Err.(interface{ Temporary() bool }); ok {
			return true
		}
	}
	return false
}

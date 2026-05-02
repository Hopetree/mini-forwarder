package forwarder

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"net"
	"sync"
	"testing"
	"time"

	"go.uber.org/zap"

	"github.com/Hopetree/mini-forwarder/internal/config"
	"github.com/Hopetree/mini-forwarder/internal/logger"
)

func init() {
	_ = logger.Init("debug", "console")
	// Silence default logger output in tests.
	zap.ReplaceGlobals(zap.NewNop())
}

// findFreePort returns a free TCP port on localhost.
func findFreePort(t *testing.T) int {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("find free port: %v", err)
	}
	defer ln.Close()
	addr := ln.Addr().(*net.TCPAddr)
	return addr.Port
}

// echoServer starts a simple TCP echo server that returns all received data.
func echoServer(t *testing.T, listenAddr string) (closer func()) {
	t.Helper()
	ln, err := net.Listen("tcp", listenAddr)
	if err != nil {
		t.Fatalf("echo server listen: %v", err)
	}
	var wg sync.WaitGroup
	ctx, cancel := context.WithCancel(context.Background())
	wg.Add(1)
	go func() {
		defer wg.Done()
		for {
			conn, err := ln.Accept()
			if err != nil {
				select {
				case <-ctx.Done():
					return
				default:
					t.Logf("echo server accept error: %v", err)
					return
				}
			}
			wg.Add(1)
			go func(c net.Conn) {
				defer wg.Done()
				defer c.Close()
				io.Copy(c, c)
			}(conn)
		}
	}()
	return func() {
		cancel()
		ln.Close()
		wg.Wait()
	}
}

// discardServer accepts connections but discards all data and closes.
func discardServer(t *testing.T, listenAddr string) (closer func()) {
	t.Helper()
	ln, err := net.Listen("tcp", listenAddr)
	if err != nil {
		t.Fatalf("discard server listen: %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				select {
				case <-ctx.Done():
					return
				default:
					return
				}
			}
			conn.Close()
		}
	}()
	return func() {
		cancel()
		ln.Close()
	}
}

// --- relay tests ---

func TestRelay_Echo(t *testing.T) {
	port := findFreePort(t)
	closeSrv := echoServer(t, fmt.Sprintf("127.0.0.1:%d", port))
	defer closeSrv()

	client, err := net.DialTimeout("tcp", fmt.Sprintf("127.0.0.1:%d", port), time.Second)
	if err != nil {
		t.Fatalf("dial echo server: %v", err)
	}
	defer client.Close()

	msg := "hello mini-forwarder\n"
	if _, err := client.Write([]byte(msg)); err != nil {
		t.Fatalf("write: %v", err)
	}

	// Set a read deadline so test doesn't hang.
	client.SetReadDeadline(time.Now().Add(3 * time.Second))

	buf := make([]byte, len(msg))
	n, err := io.ReadFull(client, buf)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if string(buf[:n]) != msg {
		t.Errorf("echo mismatch: got %q, want %q", string(buf[:n]), msg)
	}
}

func TestRelay_OneSideCloses(t *testing.T) {
	port := findFreePort(t)
	closeSrv := echoServer(t, fmt.Sprintf("127.0.0.1:%d", port))
	defer closeSrv()

	client, err := net.DialTimeout("tcp", fmt.Sprintf("127.0.0.1:%d", port), time.Second)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer client.Close()

	// Close write side; server should see EOF.
	tc := client.(*net.TCPConn)
	_ = tc.CloseWrite()

	buf := make([]byte, 1024)
	client.SetReadDeadline(time.Now().Add(1 * time.Second))
	n, err := client.Read(buf)
	if err != nil && err != io.EOF {
		t.Logf("read after CloseWrite: %v (expected EOF or empty)", err)
	}
	// Echo server should have returned 0 bytes since we sent nothing.
	if n != 0 {
		t.Errorf("expected 0 bytes after CloseWrite, got %d", n)
	}
}

// --- Manager integration tests ---

func TestManager_StartStop(t *testing.T) {
	echoPort := findFreePort(t)
	closeEcho := echoServer(t, fmt.Sprintf("127.0.0.1:%d", echoPort))
	defer closeEcho()

	listenPort := findFreePort(t)

	cfg := &config.Config{
		Forwards: []config.ForwardRule{
			{
				Name:        "echo",
				Listen:      fmt.Sprintf("127.0.0.1:%d", listenPort),
				Target:      fmt.Sprintf("127.0.0.1:%d", echoPort),
				DialTimeout: 2 * time.Second,
				MaxRetries:  0,
				KeepAlive:   10 * time.Second,
			},
		},
	}

	mgr := NewManager(cfg)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	if err := mgr.Start(ctx); err != nil {
		t.Fatalf("Start() error = %v", err)
	}

	// Verify status.
	status := mgr.Status()
	if len(status) != 1 {
		t.Fatalf("expected 1 forwarder in status, got %d", len(status))
	}
	if status[0]["name"] != "echo" {
		t.Errorf("expected name 'echo', got %v", status[0]["name"])
	}

	// Test data relay through the forwarder.
	client, err := net.DialTimeout("tcp", fmt.Sprintf("127.0.0.1:%d", listenPort), 2*time.Second)
	if err != nil {
		t.Fatalf("dial forwarder: %v", err)
	}
	defer client.Close()

	msg := "integration test\n"
	if _, err := client.Write([]byte(msg)); err != nil {
		t.Fatalf("write to forwarder: %v", err)
	}

	client.SetReadDeadline(time.Now().Add(3 * time.Second))
	reader := bufio.NewReader(client)
	line, err := reader.ReadString('\n')
	if err != nil {
		t.Fatalf("read from forwarder: %v", err)
	}
	if line != msg {
		t.Errorf("echo mismatch: got %q, want %q", line, msg)
	}

	// Graceful stop.
	mgr.StopAll()
}

func TestManager_TargetUnreachable(t *testing.T) {
	// Use a port that nothing is listening on.
	listenPort := findFreePort(t)
	unusedPort := findFreePort(t)

	cfg := &config.Config{
		Forwards: []config.ForwardRule{
			{
				Name:        "dead",
				Listen:      fmt.Sprintf("127.0.0.1:%d", listenPort),
				Target:      fmt.Sprintf("127.0.0.1:%d", unusedPort),
				DialTimeout: 200 * time.Millisecond,
				MaxRetries:  1,
				RetryInterval: 100 * time.Millisecond,
				KeepAlive:   10 * time.Second,
			},
		},
	}

	mgr := NewManager(cfg)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	if err := mgr.Start(ctx); err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	defer mgr.StopAll()

	// Connect to forwarder. It should accept, fail to dial target, and close.
	client, err := net.DialTimeout("tcp", fmt.Sprintf("127.0.0.1:%d", listenPort), time.Second)
	if err != nil {
		t.Fatalf("dial forwarder: %v", err)
	}
	defer client.Close()

	client.SetReadDeadline(time.Now().Add(5 * time.Second))
	buf := make([]byte, 1024)
	n, err := client.Read(buf)
	// The connection should be closed by the forwarder after retries exhausted.
	if err == nil && n > 0 {
		t.Errorf("expected connection close, got %d bytes", n)
	}
}

func TestManager_MultipleForwarders(t *testing.T) {
	echoPort1 := findFreePort(t)
	echoPort2 := findFreePort(t)
	closeSrv1 := echoServer(t, fmt.Sprintf("127.0.0.1:%d", echoPort1))
	defer closeSrv1()
	closeSrv2 := echoServer(t, fmt.Sprintf("127.0.0.1:%d", echoPort2))
	defer closeSrv2()

	listenPort1 := findFreePort(t)
	listenPort2 := findFreePort(t)

	cfg := &config.Config{
		Forwards: []config.ForwardRule{
			{
				Name:        "echo1",
				Listen:      fmt.Sprintf("127.0.0.1:%d", listenPort1),
				Target:      fmt.Sprintf("127.0.0.1:%d", echoPort1),
				DialTimeout: 2 * time.Second,
				MaxRetries:  0,
				KeepAlive:   10 * time.Second,
			},
			{
				Name:        "echo2",
				Listen:      fmt.Sprintf("127.0.0.1:%d", listenPort2),
				Target:      fmt.Sprintf("127.0.0.1:%d", echoPort2),
				DialTimeout: 2 * time.Second,
				MaxRetries:  0,
				KeepAlive:   10 * time.Second,
			},
		},
	}

	mgr := NewManager(cfg)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	if err := mgr.Start(ctx); err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	defer mgr.StopAll()

	status := mgr.Status()
	if len(status) != 2 {
		t.Fatalf("expected 2 forwarders, got %d", len(status))
	}

	// Test both forwarders work.
	for i, port := range []int{listenPort1, listenPort2} {
		client, err := net.DialTimeout("tcp", fmt.Sprintf("127.0.0.1:%d", port), time.Second)
		if err != nil {
			t.Fatalf("forwarder %d: dial: %v", i, err)
		}
		msg := fmt.Sprintf("test-%d\n", i)
		if _, err := client.Write([]byte(msg)); err != nil {
			t.Fatalf("forwarder %d: write: %v", i, err)
		}
		client.SetReadDeadline(time.Now().Add(2 * time.Second))
		buf := make([]byte, len(msg))
		n, err := io.ReadFull(client, buf)
		if err != nil {
			t.Fatalf("forwarder %d: read: %v", i, err)
		}
		if string(buf[:n]) != msg {
			t.Errorf("forwarder %d: got %q, want %q", i, string(buf[:n]), msg)
		}
		client.Close()
	}
}

func TestManager_Reload(t *testing.T) {
	echoPort := findFreePort(t)
	closeEcho := echoServer(t, fmt.Sprintf("127.0.0.1:%d", echoPort))
	defer closeEcho()

	listenPort1 := findFreePort(t)
	listenPort2 := findFreePort(t)

	cfg := &config.Config{
		Forwards: []config.ForwardRule{
			{
				Name:        "echo",
				Listen:      fmt.Sprintf("127.0.0.1:%d", listenPort1),
				Target:      fmt.Sprintf("127.0.0.1:%d", echoPort),
				DialTimeout: 2 * time.Second,
				MaxRetries:  0,
				KeepAlive:   10 * time.Second,
			},
		},
	}

	mgr := NewManager(cfg)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	if err := mgr.Start(ctx); err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	defer mgr.StopAll()

	// Reload: add a new forwarder.
	newCfg := &config.Config{
		Forwards: []config.ForwardRule{
			{
				Name:        "echo",
				Listen:      fmt.Sprintf("127.0.0.1:%d", listenPort1),
				Target:      fmt.Sprintf("127.0.0.1:%d", echoPort),
				DialTimeout: 2 * time.Second,
				MaxRetries:  0,
				KeepAlive:   10 * time.Second,
			},
			{
				Name:        "echo2",
				Listen:      fmt.Sprintf("127.0.0.1:%d", listenPort2),
				Target:      fmt.Sprintf("127.0.0.1:%d", echoPort),
				DialTimeout: 2 * time.Second,
				MaxRetries:  0,
				KeepAlive:   10 * time.Second,
			},
		},
	}

	if err := mgr.Reload(newCfg); err != nil {
		t.Fatalf("Reload() error = %v", err)
	}

	// Give the new listener time to start.
	time.Sleep(100 * time.Millisecond)

	status := mgr.Status()
	if len(status) != 2 {
		t.Fatalf("expected 2 forwarders after reload, got %d", len(status))
	}

	// Test the new forwarder works.
	client, err := net.DialTimeout("tcp", fmt.Sprintf("127.0.0.1:%d", listenPort2), time.Second)
	if err != nil {
		t.Fatalf("dial new forwarder: %v", err)
	}
	client.Close()
}

func TestManager_Reload_RemoveForwarder(t *testing.T) {
	echoPort := findFreePort(t)
	closeEcho := echoServer(t, fmt.Sprintf("127.0.0.1:%d", echoPort))
	defer closeEcho()

	listenPort := findFreePort(t)

	cfg := &config.Config{
		Forwards: []config.ForwardRule{
			{
				Name:        "echo",
				Listen:      fmt.Sprintf("127.0.0.1:%d", listenPort),
				Target:      fmt.Sprintf("127.0.0.1:%d", echoPort),
				DialTimeout: 2 * time.Second,
				MaxRetries:  0,
				KeepAlive:   10 * time.Second,
			},
		},
	}

	mgr := NewManager(cfg)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	if err := mgr.Start(ctx); err != nil {
		t.Fatalf("Start() error = %v", err)
	}

	// Reload: remove all forwarders.
	emptyCfg := &config.Config{
		Forwards: []config.ForwardRule{},
	}

	if err := mgr.Reload(emptyCfg); err != nil {
		t.Fatalf("Reload() error = %v", err)
	}

	time.Sleep(100 * time.Millisecond)

	status := mgr.Status()
	if len(status) != 0 {
		t.Fatalf("expected 0 forwarders after reload, got %d", len(status))
	}

	// StopAll should be a no-op since no forwarders remain.
	mgr.StopAll()
}

// --- Idle timeout tests ---

func TestIdleTimeoutConn(t *testing.T) {
	port := findFreePort(t)
	closeSrv := echoServer(t, fmt.Sprintf("127.0.0.1:%d", port))
	defer closeSrv()

	client, err := net.DialTimeout("tcp", fmt.Sprintf("127.0.0.1:%d", port), time.Second)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer client.Close()

	// Wrap with a short idle timeout.
	wrapped := wrapIdleTimeout(client, 200*time.Millisecond)

	// First read should succeed if we write and read quickly.
	if _, err := wrapped.Write([]byte("ping\n")); err != nil {
		t.Fatalf("write: %v", err)
	}

	wrapped.SetReadDeadline(time.Now().Add(500 * time.Millisecond))
	buf := make([]byte, 5)
	if _, err := io.ReadFull(wrapped, buf); err != nil {
		t.Fatalf("read: %v", err)
	}

	// Now wait longer than idle timeout and try to read again.
	time.Sleep(300 * time.Millisecond)
	wrapped.SetReadDeadline(time.Now().Add(500 * time.Millisecond))
	_, err = wrapped.Read(buf)
	// The underlying connection is still alive (echo server), but the deadline
	// should have fired. Since wrapIdleTimeout sets a deadline before each read,
	// we expect a timeout error.
	if err == nil {
		t.Error("expected timeout error after idle period")
	}
}

// --- Helper tests ---

func TestIsTemporaryError(t *testing.T) {
	tests := []struct {
		name  string
		err   error
		want  bool
	}{
		{"nil", nil, false},
		{"context canceled", context.Canceled, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := isTemporaryError(tt.err)
			if got != tt.want {
				t.Errorf("isTemporaryError(%v) = %v, want %v", tt.err, got, tt.want)
			}
		})
	}
}


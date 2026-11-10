// Copyright (c) 2026 Aiven, Helsinki, Finland. https://aiven.io/

package main

import (
	"encoding/pem"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"runtime/debug"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/aiven-open/ai-gateway-cli/internal/cli"
	"github.com/aiven-open/ai-gateway-cli/internal/config"
	"github.com/aiven-open/ai-gateway-cli/internal/creds"
	"github.com/aiven-open/ai-gateway-cli/internal/fsutil"
)

func TestBuildFallsBackToModuleVersionAndVCSStamp(t *testing.T) {
	info := &debug.BuildInfo{
		Main:     debug.Module{Version: "v0.2.0"},
		Settings: []debug.BuildSetting{{Key: "vcs.revision", Value: "abc1234"}, {Key: "vcs.time", Value: "2026-10-28T12:00:00Z"}},
	}
	want := cli.Build{Version: "v0.2.0", Commit: "abc1234", Date: "2026-10-28T12:00:00Z"}
	if got := build(info); got != want {
		t.Errorf("build = %+v, want %+v", got, want)
	}
}

func TestBuildKeepsDevForLocalBuild(t *testing.T) {
	if got := build(&debug.BuildInfo{Main: debug.Module{Version: "(devel)"}}); got.Version != "dev" {
		t.Errorf("version = %q", got.Version)
	}
}

func TestBuildPrefersLdflags(t *testing.T) {
	version = "0.2.0"
	t.Cleanup(func() { version = "dev" })
	if got := build(&debug.BuildInfo{Main: debug.Module{Version: "v9.9.9"}}); got.Version != "0.2.0" {
		t.Errorf("version = %q", got.Version)
	}
}

func binary(t *testing.T) string {
	t.Helper()
	name := "aiven-ai-gateway"
	if runtime.GOOS == "windows" {
		name += ".exe"
	}
	bin := filepath.Join(t.TempDir(), name)
	if out, err := exec.Command("go", "build", "-o", bin, ".").CombinedOutput(); err != nil {
		t.Fatalf("build: %v\n%s", err, out)
	}
	return bin
}

func TestVersionAndHelpAnswerDespiteBrokenConfig(t *testing.T) {
	bin := binary(t)
	for _, base := range []string{"bogus", "http://127.0.0.1:1"} {
		for arg, want := range map[string]string{"--version": "aiven-ai-gateway ", "--help": "usage: aiven-ai-gateway "} {
			cmd := exec.Command(bin, arg)
			cmd.Env = []string{"PATH=" + os.Getenv("PATH"), "AIVEN_AI_GATEWAY_BASE_URL=" + base}
			out, err := cmd.Output()
			if err != nil || !strings.HasPrefix(string(out), want) {
				t.Fatalf("%s with %s: err %v, stdout %q", arg, base, err, out)
			}
		}
	}
}

func TestBinaryRejectsHTTPBeforeCredentialsOrNetwork(t *testing.T) {
	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		requests.Add(1)
		_, _ = w.Write([]byte(`{"data":[{"id":"synthetic-model"}]}`))
	}))
	defer server.Close()
	bin := binary(t)
	for _, credential := range []string{"key file", "unreadable key path"} {
		t.Run(credential, func(t *testing.T) {
			home := t.TempDir()
			if credential == "key file" {
				if _, err := creds.Write(home, server.URL, "sk-binary-synthetic", fsutil.WriteAtomic); err != nil {
					t.Fatal(err)
				}
			} else if err := os.MkdirAll(creds.Path(home, server.URL), 0o700); err != nil {
				t.Fatal(err)
			}
			cmd := exec.Command(bin, "models")
			cmd.Env = []string{
				"PATH=" + os.Getenv("PATH"), config.HomeVar + "=" + home,
				"AIVEN_AI_GATEWAY_BASE_URL=" + server.URL, creds.EnvVar + "=sk-ambient-synthetic",
			}
			out, err := cmd.CombinedOutput()
			if err == nil || cmd.ProcessState.ExitCode() != 1 || !strings.Contains(string(out), "HTTPS") ||
				!strings.Contains(string(out), "bearer") || strings.Contains(string(out), "read the key file") {
				t.Fatalf("err %v, output %q", err, out)
			}
			if requests.Load() != 0 {
				t.Fatal("HTTP server received a request")
			}
		})
	}
}

type recordingListener struct {
	net.Listener
	mu     sync.Mutex
	firsts []byte
}

func (l *recordingListener) Accept() (net.Conn, error) {
	conn, err := l.Listener.Accept()
	if err != nil {
		return nil, err
	}
	return &recordingConn{Conn: conn, listener: l}, nil
}

type recordingConn struct {
	net.Conn
	listener *recordingListener
	once     sync.Once
}

func (c *recordingConn) Read(b []byte) (int, error) {
	n, err := c.Conn.Read(b)
	if n > 0 {
		c.once.Do(func() {
			c.listener.mu.Lock()
			defer c.listener.mu.Unlock()
			c.listener.firsts = append(c.listener.firsts, b[0])
		})
	}
	return n, err
}

func TestBinaryVerifiesTLSWithoutDowngrading(t *testing.T) {
	var requests atomic.Int32
	server := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		if r.Header.Get("Authorization") != "Bearer sk-binary-synthetic" {
			t.Error("unexpected credential")
		}
		_, _ = w.Write([]byte(`{"data":[{"id":"synthetic-model"}]}`))
	}))
	listener := &recordingListener{Listener: server.Listener}
	server.Listener = listener
	server.StartTLS()
	defer server.Close()
	bin := binary(t)
	home, trust := t.TempDir(), t.TempDir()
	if _, err := creds.Write(home, server.URL, "sk-binary-synthetic", fsutil.WriteAtomic); err != nil {
		t.Fatal(err)
	}
	certFile := filepath.Join(trust, "roots.pem")
	if err := os.WriteFile(certFile, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	run := func() ([]byte, error) {
		cmd := exec.Command(bin, "models")
		cmd.Env = []string{
			"PATH=" + os.Getenv("PATH"), config.HomeVar + "=" + home,
			"AIVEN_AI_GATEWAY_BASE_URL=" + server.URL,
			"SSL_CERT_FILE=" + certFile, "SSL_CERT_DIR=" + trust,
		}
		return cmd.CombinedOutput()
	}
	out, err := run()
	if err == nil || !strings.Contains(string(out), "certificate signed by unknown authority") || requests.Load() != 0 {
		t.Fatalf("untrusted TLS: err %v, output %q, requests %d", err, out, requests.Load())
	}
	listener.mu.Lock()
	firsts := append([]byte(nil), listener.firsts...)
	listener.mu.Unlock()
	if len(firsts) == 0 {
		t.Fatal("the binary did not attempt a TLS connection")
	}
	for _, first := range firsts {
		if first != 0x16 {
			t.Fatalf("a connection began with %#x instead of a TLS handshake", first)
		}
	}
	certificate := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: server.Certificate().Raw})
	if err := os.WriteFile(certFile, certificate, 0o600); err != nil {
		t.Fatal(err)
	}
	if out, err := run(); err != nil || !strings.Contains(string(out), "synthetic-model") || requests.Load() != 1 {
		t.Fatalf("trusted TLS: err %v, output %q, requests %d", err, out, requests.Load())
	}
}

// Without $HOME the key file's path would be relative, naming a file in the working directory.
func TestCommandsRefuseToRunWithoutAHome(t *testing.T) {
	bin := binary(t)
	dir := t.TempDir()
	key := filepath.Join(dir, ".config/aiven/ai-gateway-key")
	if err := os.MkdirAll(filepath.Dir(key), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(key, []byte("sk-test\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(bin, "logout")
	cmd.Dir, cmd.Env = dir, []string{"PATH=" + os.Getenv("PATH")}
	out, err := cmd.CombinedOutput()
	if cmd.ProcessState.ExitCode() != 1 || string(out) != "aiven-ai-gateway: "+config.ErrNoHome.Error()+"\n" {
		t.Fatalf("err %v, output %q", err, out)
	}
	if _, err := os.Stat(key); err != nil {
		t.Errorf("logout without a home touched the working directory: %v", err)
	}
}

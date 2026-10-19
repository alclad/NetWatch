package sshpower

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/pem"
	"errors"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"

	"golang.org/x/crypto/ssh"
	"golang.org/x/crypto/ssh/knownhosts"
)

// testServer — минимальный in-memory SSH-сервер для тестов: публичный ключ
// клиента сверяется с wantKey (nil — принимать любой... нет, см. rejectAll),
// команда exec запоминается, exit-status настраивается.
type testServer struct {
	addr       string
	port       int
	closeFn    func()
	commands   []string
	mu         sync.Mutex
	rejectAll  bool
	exitStatus uint32
}

func newTestServer(t *testing.T) *testServer {
	t.Helper()
	_, hostKey, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	signer, err := ssh.NewSignerFromKey(hostKey)
	if err != nil {
		t.Fatal(err)
	}
	cfg := &ssh.ServerConfig{}
	cfg.AddHostKey(signer)
	srv := &testServer{}
	cfg.PublicKeyCallback = func(meta ssh.ConnMetadata, key ssh.PublicKey) (*ssh.Permissions, error) {
		if srv.rejectAll {
			return nil, errors.New("отказано")
		}
		return &ssh.Permissions{}, nil
	}
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	srv.addr = ln.Addr().(*net.TCPAddr).IP.String()
	srvPort := ln.Addr().(*net.TCPAddr).Port
	srv.closeFn = func() { ln.Close() }
	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			go srv.handleConn(conn, cfg)
		}
	}()
	t.Cleanup(srv.closeFn)
	// Порт для Client.Port сохраняем в поле через замыкание ниже.
	srv.port = srvPort
	return srv
}

func (s *testServer) handleConn(conn net.Conn, cfg *ssh.ServerConfig) {
	sconn, chans, reqs, err := ssh.NewServerConn(conn, cfg)
	if err != nil {
		conn.Close()
		return
	}
	defer sconn.Close()
	go ssh.DiscardRequests(reqs)
	for newCh := range chans {
		if newCh.ChannelType() != "session" {
			newCh.Reject(ssh.UnknownChannelType, "только session")
			continue
		}
		ch, chReqs, err := newCh.Accept()
		if err != nil {
			continue
		}
		go func(ch ssh.Channel, chReqs <-chan *ssh.Request) {
			for req := range chReqs {
				if req.Type == "exec" {
					var payload struct{ Command string }
					if err := ssh.Unmarshal(req.Payload, &payload); err == nil {
						s.mu.Lock()
						s.commands = append(s.commands, payload.Command)
						s.mu.Unlock()
					}
					if req.WantReply {
						req.Reply(true, nil)
					}
					msg := struct{ Status uint32 }{Status: s.exitStatus}
					ch.SendRequest("exit-status", false, ssh.Marshal(msg))
					ch.Close()
					return
				}
				if req.WantReply {
					req.Reply(false, nil)
				}
			}
		}(ch, chReqs)
	}
}

func (s *testServer) lastCommand() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.commands) == 0 {
		return ""
	}
	return s.commands[len(s.commands)-1]
}

func TestPowerOffSuccess(t *testing.T) {
	srv := newTestServer(t)
	keyPath, knownHosts := tempPaths(t)
	writeClientKey(t, keyPath)
	c := Client{User: "root", Port: srv.port, KeyPath: keyPath, KnownHostsPath: knownHosts}
	if err := c.PowerOff(context.Background(), srv.addr); err != nil {
		t.Fatalf("PowerOff: %v", err)
	}
	if cmd := srv.lastCommand(); cmd != "poweroff" {
		t.Fatalf("команда на сервере: %q", cmd)
	}
	b, err := os.ReadFile(knownHosts)
	if err != nil || !strings.Contains(string(b), srv.addr) {
		t.Fatalf("known_hosts не содержит хост: %q, err=%v", string(b), err)
	}
}

func TestPowerOffAuthRejected(t *testing.T) {
	srv := newTestServer(t)
	srv.rejectAll = true
	keyPath, knownHosts := tempPaths(t)
	writeClientKey(t, keyPath)
	c := Client{User: "root", Port: srv.port, KeyPath: keyPath, KnownHostsPath: knownHosts}
	err := c.PowerOff(context.Background(), srv.addr)
	if err == nil || !strings.Contains(err.Error(), "аутентификация") {
		t.Fatalf("ожидалась ошибка аутентификации, получено: %v", err)
	}
}

func TestPowerOffUnreachableHost(t *testing.T) {
	// Занимаем порт и освобождаем — подключение к нему даст отказ.
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	port := ln.Addr().(*net.TCPAddr).Port
	ln.Close()
	keyPath, knownHosts := tempPaths(t)
	writeClientKey(t, keyPath)
	c := Client{User: "root", Port: port, KeyPath: keyPath, KnownHostsPath: knownHosts}
	err = c.PowerOff(context.Background(), "127.0.0.1")
	if err == nil || !strings.Contains(err.Error(), "не удалось подключиться") {
		t.Fatalf("ожидалась ошибка подключения, получено: %v", err)
	}
}

func TestNotConfigured(t *testing.T) {
	c := Client{User: "root", Port: 22, KeyPath: "", KnownHostsPath: "x"}
	if err := c.PowerOff(context.Background(), "127.0.0.1"); !errors.Is(err, ErrNotConfigured) {
		t.Fatalf("ожидался ErrNotConfigured, получено: %v", err)
	}
}

func TestTOFURejectsChangedHostKey(t *testing.T) {
	srv := newTestServer(t)
	keyPath, knownHosts := tempPaths(t)
	writeClientKey(t, keyPath)
	// Подкладываем в known_hosts строку с ДРУГИМ ключом для этого хоста
	// (knownhosts.Line сам нормализует адрес в формат [host]:port).
	_, other, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	otherSigner, err := ssh.NewSignerFromKey(other)
	if err != nil {
		t.Fatal(err)
	}
	entry := knownhosts.Line([]string{net.JoinHostPort(srv.addr, strconv.Itoa(srv.port))}, otherSigner.PublicKey())
	if err := os.WriteFile(knownHosts, []byte(entry), 0o600); err != nil {
		t.Fatal(err)
	}
	c := Client{User: "root", Port: srv.port, KeyPath: keyPath, KnownHostsPath: knownHosts}
	err = c.PowerOff(context.Background(), srv.addr)
	if err == nil || !strings.Contains(err.Error(), "отличается") {
		t.Fatalf("ожидался отказ из-за смены хост-ключа, получено: %v", err)
	}
}

func TestPowerOffNonZeroExit(t *testing.T) {
	srv := newTestServer(t)
	srv.exitStatus = 1
	keyPath, knownHosts := tempPaths(t)
	writeClientKey(t, keyPath)
	c := Client{User: "root", Port: srv.port, KeyPath: keyPath, KnownHostsPath: knownHosts}
	err := c.PowerOff(context.Background(), srv.addr)
	if err == nil || !strings.Contains(err.Error(), "кодом 1") {
		t.Fatalf("ожидалась ошибка кода выхода, получено: %v", err)
	}
}

func TestContextCancel(t *testing.T) {
	srv := newTestServer(t)
	keyPath, knownHosts := tempPaths(t)
	writeClientKey(t, keyPath)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	c := Client{User: "root", Port: srv.port, KeyPath: keyPath, KnownHostsPath: knownHosts}
	if err := c.PowerOff(ctx, srv.addr); !errors.Is(err, context.Canceled) {
		t.Fatalf("ожидался context.Canceled, получено: %v", err)
	}
}

func tempPaths(t *testing.T) (keyPath, knownHosts string) {
	t.Helper()
	dir := t.TempDir()
	return filepath.Join(dir, "id_ed25519"), filepath.Join(dir, "known_hosts")
}

func writeClientKey(t *testing.T, path string) {
	t.Helper()
	_, key, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	block, err := ssh.MarshalPrivateKey(key, "")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, pem.EncodeToMemory(block), 0o600); err != nil {
		t.Fatal(err)
	}
}

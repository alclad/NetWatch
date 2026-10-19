// Package sshpower выключает Linux-ПК командой poweroff по SSH,
// авторизуясь приватным ключом. Хост-ключи проверяются по схеме TOFU
// (trust on first use): первый контакт запоминается в known_hosts,
// смена ключа означает возможную подмену и отвергается.
package sshpower

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"

	"golang.org/x/crypto/ssh"
	"golang.org/x/crypto/ssh/knownhosts"
)

// ErrNotConfigured — путь к приватному ключу не задан в настройках.
var ErrNotConfigured = errors.New("выключение не настроено: укажите путь к приватному SSH-ключу в настройках")

// dialTimeout применяется к TCP-подключению и SSH-рукопожатию,
// если у контекста нет более раннего дедлайна.
const dialTimeout = 10 * time.Second

// knownHostsMu защищает проверку и дозапись known_hosts от параллельных
// вызовов PowerOff (файл общий на все устройства).
var knownHostsMu sync.Mutex

// Client выключает устройства по SSH от имени User ключом из KeyPath.
type Client struct {
	User           string // SSH-пользователь
	Port           int    // SSH-порт
	KeyPath        string // приватный ключ (без пароля)
	KnownHostsPath string // хранилище хост-ключей, создаётся при необходимости
}

func (c Client) PowerOff(ctx context.Context, address string) error {
	if strings.TrimSpace(c.KeyPath) == "" {
		return ErrNotConfigured
	}
	signer, err := loadSigner(c.KeyPath)
	if err != nil {
		return err
	}
	hostCb, err := c.tofuCallback()
	if err != nil {
		return err
	}
	client, err := c.dial(ctx, address, signer, hostCb)
	if err != nil {
		return err
	}
	defer client.Close()
	return runPowerOff(ctx, client)
}

func loadSigner(path string) (ssh.Signer, error) {
	b, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil, fmt.Errorf("приватный ключ %s не найден", path)
	}
	if err != nil {
		return nil, fmt.Errorf("не удалось прочитать ключ %s: %w", path, err)
	}
	signer, err := ssh.ParsePrivateKey(b)
	if err != nil {
		var pp *ssh.PassphraseMissingError
		if errors.As(err, &pp) {
			return nil, fmt.Errorf("ключ %s защищён паролем — сгенерируйте ключ без пароля (ssh-keygen -N \"\")", path)
		}
		return nil, fmt.Errorf("не удалось разобрать ключ %s: %w", path, err)
	}
	return signer, nil
}

// tofuCallback возвращает проверку хост-ключа: известный ключ сверяется строго,
// неизвестный записывается в known_hosts и принимается (TOFU).
func (c Client) tofuCallback() (ssh.HostKeyCallback, error) {
	if c.KnownHostsPath == "" {
		return nil, errors.New("путь к known_hosts не задан")
	}
	knownHostsMu.Lock()
	defer knownHostsMu.Unlock()
	if _, err := os.Stat(c.KnownHostsPath); errors.Is(err, os.ErrNotExist) {
		if err := os.WriteFile(c.KnownHostsPath, nil, 0o600); err != nil {
			return nil, fmt.Errorf("не удалось создать known_hosts: %w", err)
		}
	}
	strict, err := knownhosts.New(c.KnownHostsPath)
	if err != nil {
		return nil, fmt.Errorf("не удалось прочитать known_hosts: %w", err)
	}
	return func(hostname string, remote net.Addr, key ssh.PublicKey) error {
		err := strict(hostname, remote, key)
		if err == nil {
			return nil
		}
		var keyErr *knownhosts.KeyError
		// Want пуст, если хост в файле отсутствует, — это первый контакт.
		if errors.As(err, &keyErr) && len(keyErr.Want) == 0 {
			f, err := os.OpenFile(c.KnownHostsPath, os.O_APPEND|os.O_WRONLY, 0o600)
			if err != nil {
				return fmt.Errorf("не удалось записать хост-ключ: %w", err)
			}
			defer f.Close()
			if _, err := fmt.Fprintln(f, knownhosts.Line([]string{hostname}, key)); err != nil {
				return fmt.Errorf("не удалось записать хост-ключ: %w", err)
			}
			return nil
		}
		return fmt.Errorf("хост-ключ %s отличается от сохранённого — возможно подмена; подключение отклонено (удалите строку хоста из %s, если ключ действительно сменился)", hostname, c.KnownHostsPath)
	}, nil
}

func (c Client) dial(ctx context.Context, address string, signer ssh.Signer, hostCb ssh.HostKeyCallback) (*ssh.Client, error) {
	timeout := dialTimeout
	if d, ok := ctx.Deadline(); ok {
		if t := time.Until(d); t > 0 && t < timeout {
			timeout = t
		}
	}
	cfg := &ssh.ClientConfig{
		User:            c.User,
		Auth:            []ssh.AuthMethod{ssh.PublicKeys(signer)},
		HostKeyCallback: hostCb,
		Timeout:         timeout,
	}
	hostPort := net.JoinHostPort(strings.Trim(address, "[]"), strconv.Itoa(c.Port))
	dialer := &net.Dialer{Timeout: timeout}
	conn, err := dialer.DialContext(ctx, "tcp", hostPort)
	if err != nil {
		return nil, fmt.Errorf("не удалось подключиться к %s: %w", hostPort, err)
	}
	// Второй аргумент — адрес для проверки хост-ключа: knownhosts требует
	// формат host:port и сам нормализует его ([host]:port для нестандартных портов).
	sshConn, chans, reqs, err := ssh.NewClientConn(conn, hostPort, cfg)
	if err != nil {
		conn.Close()
		// x/crypto/ssh не даёт типизированной ошибки аутентификации —
		// распознаём её по канонической строке рукопожатия.
		if strings.Contains(err.Error(), "unable to authenticate") {
			return nil, fmt.Errorf("SSH-аутентификация отклонена для %s (пользователь %q): проверьте ключ и его наличие в authorized_keys", hostPort, c.User)
		}
		return nil, fmt.Errorf("SSH-подключение к %s: %w", hostPort, err)
	}
	return ssh.NewClient(sshConn, chans, reqs), nil
}

// runPowerOff выполняет команду и ждёт её завершения. Выключающаяся машина
// часто рвёт соединение до передачи exit status — такой исход считается успехом.
func runPowerOff(ctx context.Context, client *ssh.Client) error {
	session, err := client.NewSession()
	if err != nil {
		return fmt.Errorf("не удалось открыть сессию: %w", err)
	}
	defer session.Close()
	var out strings.Builder
	session.Stdout = &out
	session.Stderr = &out
	done := make(chan error, 1)
	go func() {
		if err := session.Start("poweroff"); err != nil {
			done <- err
			return
		}
		done <- session.Wait()
	}()
	select {
	case err := <-done:
		if err == nil {
			return nil
		}
		var exitErr *ssh.ExitError
		if errors.As(err, &exitErr) {
			return fmt.Errorf("poweroff завершился с кодом %d%s", exitErr.ExitStatus(), outputSuffix(out.String()))
		}
		if errors.Is(err, io.EOF) || strings.Contains(err.Error(), "without exit status") {
			return nil // соединение оборвано после отправки команды — машина выключается
		}
		return fmt.Errorf("poweroff: %v%s", err, outputSuffix(out.String()))
	case <-ctx.Done():
		session.Close()
		client.Close()
		return ctx.Err()
	}
}

func outputSuffix(out string) string {
	if strings.TrimSpace(out) == "" {
		return ""
	}
	return "; вывод: " + strings.TrimSpace(out)
}

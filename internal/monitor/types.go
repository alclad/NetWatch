// Package monitor implements the persistent, transport-independent monitoring core.
package monitor

import (
	"context"
	"errors"
	"sort"
	"time"
)

var (
	ErrNotFound   = errors.New("device not found")
	ErrPermission = errors.New("ping permission denied")
	// ErrPowerUnavailable — выключение не включено на сервере
	// (нет SSH-ключа в настройках).
	ErrPowerUnavailable = errors.New("выключение недоступно: функция не настроена на сервере")
)

// Pinger must honor cancellation. Implementations may be called concurrently.
type Pinger interface {
	Ping(context.Context, string) (time.Duration, error)
}

type Settings struct {
	IntervalSeconds  int `json:"interval_seconds"`
	TimeoutSeconds   int `json:"timeout_seconds"`
	FailureThreshold int `json:"failure_threshold"`
	Concurrency      int `json:"concurrency"`
	// Выключение по SSH: пользователь, порт и путь к приватному ключу.
	// Пустой SSHKeyPath означает, что функция выключения не настроена.
	SSHUser    string `json:"ssh_user"`
	SSHPort    int    `json:"ssh_port"`
	SSHKeyPath string `json:"ssh_key_path"`
}

func DefaultSettings() Settings {
	return Settings{IntervalSeconds: 5, TimeoutSeconds: 2, FailureThreshold: 3, Concurrency: 16, SSHUser: "root", SSHPort: 22}
}

type DeviceInput struct {
	Name    string `json:"name"`
	Address string `json:"address"`
	URL     string `json:"url"`
	Group   string `json:"group"`
}

type Device struct {
	ID          string     `json:"id"`
	Name        string     `json:"name"`
	Address     string     `json:"address"`
	URL         string     `json:"url"`
	Group       string     `json:"group"`
	Status      string     `json:"status"`
	LatencyMS   *float64   `json:"latency_ms"`
	LastCheck   *time.Time `json:"last_check"`
	LastSuccess *time.Time `json:"last_success"`
	Error       string     `json:"error"`
	Failures    int        `json:"failures"`
}

type State struct {
	Devices     []Device `json:"devices"`
	Settings    Settings `json:"settings"`
	SystemError string   `json:"system_error"`
}

// Groups возвращает отсортированный список непустых групп устройств.
func (s State) Groups() []string {
	seen := map[string]bool{}
	var out []string
	for _, d := range s.Devices {
		if d.Group != "" && !seen[d.Group] {
			seen[d.Group] = true
			out = append(out, d.Group)
		}
	}
	sort.Strings(out)
	return out
}

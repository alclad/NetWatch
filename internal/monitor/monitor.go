package monitor

import (
	"bytes"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"os"
	"sort"
	"sync"
	"time"
)

// Monitor — ядро мониторинга: хранилище устройств и настроек, снапшоты.
// CRUD-операции — в devices.go, запись на диск — persist.go,
// подписчики — pubsub.go, цикл проверок — runner.go, валидация — validate.go,
// выключение по SSH — power.go.
type Monitor struct {
	mu           sync.RWMutex
	devices      map[string]Device
	settings     Settings
	configPath   string
	pinger       Pinger
	powerFactory func(Settings) PowerClient
	systemError  string
	subs         map[int]chan State
	nextSub      int
	writeMu      sync.Mutex
}

// New загружает конфигурацию. powerFactory строит клиент выключения из
// актуальных настроек; nil означает, что функция выключения недоступна.
func New(configPath string, pinger Pinger, powerFactory func(Settings) PowerClient) (*Monitor, error) {
	if pinger == nil {
		return nil, errors.New("nil pinger")
	}
	m := &Monitor{devices: make(map[string]Device), settings: DefaultSettings(), configPath: configPath, pinger: pinger, powerFactory: powerFactory, subs: make(map[int]chan State)}
	if configPath == "" {
		return m, nil
	}
	b, err := os.ReadFile(configPath)
	if errors.Is(err, os.ErrNotExist) {
		return m, nil
	}
	if err != nil {
		m.setSystemError(err)
		return m, err
	}
	var p persisted
	if err = json.Unmarshal(b, &p); err != nil {
		return nil, fmt.Errorf("decode config: %w", err)
	}
	// Проверяем «наличие настроек» до нормализации: пустой объект в старом
	// конфиге означает «использовать умолчания», а не невалидные нули.
	if p.Settings != (Settings{}) {
		p.Settings = applySshDefaults(p.Settings)
		if err = validateSettings(p.Settings); err != nil {
			return nil, err
		}
		m.settings = p.Settings
	}
	for _, d := range p.Devices {
		if err = validateInput(DeviceInput{Name: d.Name, Address: d.Address, URL: d.URL, Group: d.Group}); err != nil {
			return nil, err
		}
		if d.ID == "" {
			d.ID = newID()
		}
		// Результаты прошлых проверок недействительны после перезапуска.
		d.Status = "unknown"
		d.LatencyMS = nil
		d.LastCheck = nil
		d.LastSuccess = nil
		d.Error = ""
		d.Failures = 0
		m.devices[d.ID] = d
	}
	return m, nil
}

func (m *Monitor) Snapshot() State { m.mu.RLock(); defer m.mu.RUnlock(); return m.snapshotLocked() }

// snapshotLocked возвращает состояние со стабильным порядком устройств:
// сначала по группе, затем по адресу. Итерация по map недетерминирована,
// а случайный порядок заставляет список в UI «прыгать» при каждом обновлении.
func (m *Monitor) snapshotLocked() State {
	d := make([]Device, 0, len(m.devices))
	for _, v := range m.devices {
		d = append(d, v)
	}
	sort.SliceStable(d, func(i, j int) bool {
		if d[i].Group != d[j].Group {
			return d[i].Group < d[j].Group
		}
		return lessAddress(d[i].Address, d[j].Address)
	})
	return State{Devices: d, Settings: m.settings, SystemError: m.systemError}
}

// lessAddress сравнивает адреса естественно: IP — по байтам, иначе — лексикографически.
func lessAddress(a, b string) bool {
	ia, ib := net.ParseIP(a), net.ParseIP(b)
	if ia != nil && ib != nil {
		ca, cb := ia.To16(), ib.To16()
		return bytes.Compare(ca, cb) < 0
	}
	return a < b
}

func (m *Monitor) setSystemError(err error) { m.mu.Lock(); m.systemError = err.Error(); m.mu.Unlock() }

func newID() string {
	b := make([]byte, 12)
	if _, e := rand.Read(b); e == nil {
		return hex.EncodeToString(b)
	}
	return fmt.Sprintf("%d", time.Now().UnixNano())
}

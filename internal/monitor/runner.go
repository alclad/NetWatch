package monitor

import (
	"context"
	"errors"
	"os"
	"sync"
	"time"
)

// Run performs checks until ctx is canceled, then waits for no outstanding work.
// После изменения настроек интервал применяется к следующему циклу ожидания.
func (m *Monitor) Run(ctx context.Context) error {
	m.checkAll(ctx)
	for {
		m.mu.RLock()
		interval := time.Duration(m.settings.IntervalSeconds) * time.Second
		m.mu.RUnlock()
		t := time.NewTimer(interval)
		select {
		case <-ctx.Done():
			t.Stop()
			return ctx.Err()
		case <-t.C:
			m.checkAll(ctx)
		}
	}
}

func (m *Monitor) checkAll(ctx context.Context) {
	m.mu.RLock()
	ids := make([]string, 0, len(m.devices))
	for id := range m.devices {
		ids = append(ids, id)
	}
	s := m.settings
	m.mu.RUnlock()
	sem := make(chan struct{}, s.Concurrency)
	var wg sync.WaitGroup
	for _, id := range ids {
		select {
		case <-ctx.Done():
			return
		default:
		}
		wg.Add(1)
		go func(id string) {
			defer wg.Done()
			select {
			case sem <- struct{}{}:
			case <-ctx.Done():
				return
			}
			defer func() { <-sem }()
			addr, ok := m.deviceAddress(id)
			if !ok {
				return
			}
			c, cancel := context.WithTimeout(ctx, time.Duration(s.TimeoutSeconds)*time.Second)
			defer cancel()
			lat, e := m.pinger.Ping(c, addr)
			m.applyResult(id, lat, e)
		}(id)
	}
	wg.Wait()
	// Публикуем один снапшот на цикл вместо N (по числу устройств):
	// подписчики (SSE) и так получают состояние по heartbeat.
	if ctx.Err() == nil {
		m.publish(m.Snapshot())
	}
}

func (m *Monitor) deviceAddress(id string) (string, bool) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	d, ok := m.devices[id]
	return d.Address, ok
}

func (m *Monitor) applyResult(id string, lat time.Duration, e error) {
	now := time.Now()
	m.mu.Lock()
	d, ok := m.devices[id]
	if !ok {
		m.mu.Unlock()
		return
	}
	d.LastCheck = &now
	if e == nil {
		d.Status = "up"
		d.Failures = 0
		d.Error = ""
		v := float64(lat) / float64(time.Millisecond)
		d.LatencyMS = &v
		d.LastSuccess = &now
	} else {
		d.Failures++
		d.Error = e.Error()
		if errors.Is(e, os.ErrPermission) {
			m.systemError = "Недостаточно прав для ICMP: настройте net.ipv4.ping_group_range или выдайте бинарнику CAP_NET_RAW (см. README)"
		}
		if d.Failures >= m.settings.FailureThreshold {
			d.Status = "down"
		}
	}
	m.devices[id] = d
	m.mu.Unlock()
	// Публикация отсюда убрана: checkAll публикует один снапшот на весь цикл.
	// Результаты проверок — эфемерны; на диск пишутся только изменения
	// списка устройств и настроек (persist вызывается там).
}

package monitor

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

type fakePinger struct {
	mu   sync.Mutex
	errs map[string]error
}

func (f *fakePinger) Ping(ctx context.Context, address string) (time.Duration, error) {
	f.mu.Lock()
	err := f.errs[address]
	f.mu.Unlock()
	if err != nil {
		return 0, err
	}
	return 1500 * time.Microsecond, nil
}

func newTestMonitor(t *testing.T, p Pinger) *Monitor {
	t.Helper()
	m, err := New("", p)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return m
}

func deviceByName(t *testing.T, s State, name string) Device {
	t.Helper()
	for _, d := range s.Devices {
		if d.Name == name {
			return d
		}
	}
	t.Fatalf("device %q not found in state", name)
	return Device{}
}

func TestAddDeviceValidation(t *testing.T) {
	m := newTestMonitor(t, &fakePinger{})
	bad := []DeviceInput{
		{Name: "", Address: "192.168.1.1"},
		{Name: "x", Address: ""},
		{Name: "x", Address: "has space"},
		{Name: "x", Address: "10.0.0.1", URL: "javascript:alert(1)"},
		{Name: "x", Address: "10.0.0.1", URL: "not-a-url"},
	}
	for _, in := range bad {
		if _, err := m.AddDevice(in); err == nil {
			t.Errorf("AddDevice(%+v): ожидалась ошибка", in)
		}
	}
	if _, err := m.AddDevice(DeviceInput{Name: "ok", Address: "192.168.1.1", URL: "https://example.local", Group: "ADM"}); err != nil {
		t.Fatalf("валидное устройство отклонено: %v", err)
	}
	if _, err := m.AddDevice(DeviceInput{Name: "x", Address: "1.1.1.1", Group: strings.Repeat("g", 61)}); err == nil {
		t.Error("слишком длинная группа принята")
	}
}

func TestUpdateDeleteDevice(t *testing.T) {
	m := newTestMonitor(t, &fakePinger{})
	d, err := m.AddDevice(DeviceInput{Name: "srv", Address: "10.0.0.1"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := m.UpdateDevice(d.ID, DeviceInput{Name: "srv2", Address: "10.0.0.2", Group: " УТС "}); err != nil {
		t.Fatalf("UpdateDevice: %v", err)
	}
	if got := deviceByName(t, m.Snapshot(), "srv2"); got.Address != "10.0.0.2" || got.Group != "УТС" {
		t.Fatalf("после обновления: %+v", got)
	}
	if _, err := m.UpdateDevice("missing", DeviceInput{Name: "x", Address: "1.1.1.1"}); !errors.Is(err, ErrNotFound) {
		t.Fatalf("ожидался ErrNotFound, получено %v", err)
	}
	if err := m.DeleteDevice(d.ID); err != nil {
		t.Fatal(err)
	}
	if err := m.DeleteDevice(d.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("повторное удаление: %v", err)
	}
}

func TestStatusTransitions(t *testing.T) {
	fp := &fakePinger{errs: map[string]error{"10.0.0.9": errors.New("timeout")}}
	m := newTestMonitor(t, fp)
	if _, err := m.AddDevice(DeviceInput{Name: "good", Address: "10.0.0.1"}); err != nil {
		t.Fatal(err)
	}
	if _, err := m.AddDevice(DeviceInput{Name: "bad", Address: "10.0.0.9"}); err != nil {
		t.Fatal(err)
	}
	m.applyResult(deviceByName(t, m.Snapshot(), "good").ID, time.Millisecond, nil)
	good := deviceByName(t, m.Snapshot(), "good")
	if good.Status != "up" {
		t.Fatalf("good status = %q", good.Status)
	}
	if good.LatencyMS == nil || *good.LatencyMS != 1.0 {
		t.Fatalf("latency = %v", good.LatencyMS)
	}
	if good.LastCheck == nil || good.LastSuccess == nil {
		t.Fatal("last_check/last_success не выставлены")
	}
	badID := deviceByName(t, m.Snapshot(), "bad").ID
	// До порога статус не должен становиться down.
	for i := 0; i < m.settings.FailureThreshold-1; i++ {
		m.applyResult(badID, 0, errors.New("timeout"))
		if s := deviceByName(t, m.Snapshot(), "bad").Status; s == "down" {
			t.Fatalf("слишком ранний переход в down на попытке %d", i+1)
		}
	}
	m.applyResult(badID, 0, errors.New("timeout"))
	bad := deviceByName(t, m.Snapshot(), "bad")
	if bad.Status != "down" || bad.Failures != m.settings.FailureThreshold || bad.Error == "" {
		t.Fatalf("bad device: %+v", bad)
	}
	// Успех сбрасывает счётчик и переводит в up.
	m.applyResult(badID, time.Millisecond, nil)
	if got := deviceByName(t, m.Snapshot(), "bad"); got.Status != "up" || got.Failures != 0 || got.Error != "" {
		t.Fatalf("после успеха: %+v", got)
	}
}

func TestPermissionErrorSetsSystemError(t *testing.T) {
	m := newTestMonitor(t, &fakePinger{})
	d, err := m.AddDevice(DeviceInput{Name: "x", Address: "10.0.0.1"})
	if err != nil {
		t.Fatal(err)
	}
	m.applyResult(d.ID, 0, fmt.Errorf("wrap: %w", os.ErrPermission))
	if m.Snapshot().SystemError == "" {
		t.Fatal("SystemError не выставлен при EPERM")
	}
}

func TestSettingsValidation(t *testing.T) {
	m := newTestMonitor(t, &fakePinger{})
	bad := []Settings{
		{IntervalSeconds: 0, TimeoutSeconds: 1, FailureThreshold: 1, Concurrency: 1},
		{IntervalSeconds: 5, TimeoutSeconds: 0, FailureThreshold: 1, Concurrency: 1},
		{IntervalSeconds: 2, TimeoutSeconds: 5, FailureThreshold: 1, Concurrency: 1},
		{IntervalSeconds: 5, TimeoutSeconds: 1, FailureThreshold: 0, Concurrency: 1},
		{IntervalSeconds: 5, TimeoutSeconds: 1, FailureThreshold: 1, Concurrency: 0},
	}
	for _, s := range bad {
		if _, err := m.UpdateSettings(s); err == nil {
			t.Errorf("UpdateSettings(%+v): ожидалась ошибка", s)
		}
	}
	if _, err := m.UpdateSettings(Settings{IntervalSeconds: 10, TimeoutSeconds: 3, FailureThreshold: 2, Concurrency: 8}); err != nil {
		t.Fatalf("валидные настройки отклонены: %v", err)
	}
}

func TestPersistenceRoundTrip(t *testing.T) {
	path := filepath.Join(t.TempDir(), "netwatch.json")
	m, err := New(path, &fakePinger{})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := m.AddDevice(DeviceInput{Name: "nas", Address: "192.168.1.10", URL: "https://nas.local", Group: "ADM"}); err != nil {
		t.Fatal(err)
	}
	if _, err := m.UpdateSettings(Settings{IntervalSeconds: 7, TimeoutSeconds: 2, FailureThreshold: 4, Concurrency: 5}); err != nil {
		t.Fatal(err)
	}
	// Симулируем результат проверки — на диск он попасть не должен как активный статус.
	id := deviceByName(t, m.Snapshot(), "nas").ID
	m.applyResult(id, 2*time.Millisecond, nil)

	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("файл конфигурации не создан: %v", err)
	}
	var check map[string]any
	if err := json.Unmarshal(raw, &check); err != nil {
		t.Fatalf("конфигурация не JSON: %v", err)
	}

	m2, err := New(path, &fakePinger{})
	if err != nil {
		t.Fatal(err)
	}
	s := m2.Snapshot()
	if len(s.Devices) != 1 {
		t.Fatalf("устройств: %d", len(s.Devices))
	}
	d := s.Devices[0]
	if d.ID != id || d.Name != "nas" || d.URL != "https://nas.local" || d.Group != "ADM" {
		t.Fatalf("восстановленное устройство: %+v", d)
	}
	if groups := s.Groups(); len(groups) != 1 || groups[0] != "ADM" {
		t.Fatalf("Groups: %v", groups)
	}
	if d.Status != "unknown" || d.LatencyMS != nil || d.LastCheck != nil {
		t.Fatalf("статус не сброшен после перезапуска: %+v", d)
	}
	if s.Settings.IntervalSeconds != 7 || s.Settings.FailureThreshold != 4 || s.Settings.Concurrency != 5 {
		t.Fatalf("настройки: %+v", s.Settings)
	}
}

func TestCorruptConfig(t *testing.T) {
	path := filepath.Join(t.TempDir(), "netwatch.json")
	if err := os.WriteFile(path, []byte("{broken"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := New(path, &fakePinger{}); err == nil {
		t.Fatal("повреждённая конфигурация должна давать ошибку")
	}
}

func TestSubscribePublish(t *testing.T) {
	m := newTestMonitor(t, &fakePinger{})
	ch, cancel := m.Subscribe()
	defer cancel()
	if _, err := m.AddDevice(DeviceInput{Name: "a", Address: "10.1.1.1"}); err != nil {
		t.Fatal(err)
	}
	select {
	case s := <-ch:
		if len(s.Devices) != 1 {
			t.Fatalf("в событии устройств: %d", len(s.Devices))
		}
	case <-time.After(time.Second):
		t.Fatal("событие не получено")
	}
	cancel()
	if _, ok := <-ch; ok {
		t.Fatal("канал не закрыт после отписки")
	}
}

func TestRunChecksDevices(t *testing.T) {
	fp := &fakePinger{}
	m := newTestMonitor(t, fp)
	if _, err := m.UpdateSettings(Settings{IntervalSeconds: 1, TimeoutSeconds: 1, FailureThreshold: 1, Concurrency: 4}); err != nil {
		t.Fatal(err)
	}
	if _, err := m.AddDevice(DeviceInput{Name: "a", Address: "10.2.2.2"}); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- m.Run(ctx) }()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if deviceByName(t, m.Snapshot(), "a").Status == "up" {
			cancel()
			if err := <-done; !errors.Is(err, context.Canceled) && !errors.Is(err, context.DeadlineExceeded) {
				t.Fatalf("Run: %v", err)
			}
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatal("устройство не перешло в up за отведённое время")
}

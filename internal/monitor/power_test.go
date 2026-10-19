package monitor

import (
	"context"
	"errors"
	"testing"
)

type fakePower struct {
	calls    int
	lastAddr string
	err      error
}

func (f *fakePower) PowerOff(ctx context.Context, address string) error {
	f.calls++
	f.lastAddr = address
	return f.err
}

func TestPowerOffUsesAddressAndSettings(t *testing.T) {
	fp := &fakePower{}
	m := newTestMonitorWithPower(t, fp)
	d, err := m.AddDevice(DeviceInput{Name: "pc", Address: "192.168.1.5"})
	if err != nil {
		t.Fatal(err)
	}
	if err := m.PowerOff(context.Background(), d.ID); err != nil {
		t.Fatalf("PowerOff: %v", err)
	}
	if fp.calls != 1 || fp.lastAddr != "192.168.1.5" {
		t.Fatalf("fake вызван: calls=%d addr=%q", fp.calls, fp.lastAddr)
	}
	if err := m.PowerOff(context.Background(), "missing"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("неизвестный id: %v", err)
	}
}

func TestPowerOffWithoutFactory(t *testing.T) {
	m := newTestMonitor(t, &fakePinger{})
	d, err := m.AddDevice(DeviceInput{Name: "pc", Address: "192.168.1.5"})
	if err != nil {
		t.Fatal(err)
	}
	if err := m.PowerOff(context.Background(), d.ID); !errors.Is(err, ErrPowerUnavailable) {
		t.Fatalf("ожидался ErrPowerUnavailable, получено: %v", err)
	}
}

func TestSettingsSshDefaults(t *testing.T) {
	m := newTestMonitor(t, &fakePinger{})
	// Запрос без SSH-полей: нули заменяются умолчаниями, а не отвергаются.
	s, err := m.UpdateSettings(Settings{IntervalSeconds: 10, TimeoutSeconds: 2, FailureThreshold: 3, Concurrency: 4})
	if err != nil {
		t.Fatalf("настройки без ssh-полей отклонены: %v", err)
	}
	if s.SSHUser != "root" || s.SSHPort != 22 {
		t.Fatalf("умолчания не применены: %+v", s)
	}
	if _, err := m.UpdateSettings(Settings{IntervalSeconds: 10, TimeoutSeconds: 2, FailureThreshold: 3, Concurrency: 4, SSHPort: 70000}); err == nil {
		t.Fatal("невалидный ssh_port принят")
	}
}

func newTestMonitorWithPower(t *testing.T, p PowerClient) *Monitor {
	t.Helper()
	m, err := New("", &fakePinger{}, func(Settings) PowerClient { return p })
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return m
}

package monitor

import "context"

// PowerClient выключает устройство по адресу. Реализация (SSH) живёт
// в отдельном пакете; ядро знает только этот контракт.
type PowerClient interface {
	PowerOff(ctx context.Context, address string) error
}

// PowerOff отправляет устройству команду выключения по его адресу,
// используя клиент, построенный из актуальных настроек. Статус устройства
// не меняется: после реального выключения оно само перейдёт в «down»
// по порогу неудачных проверок.
func (m *Monitor) PowerOff(ctx context.Context, id string) error {
	m.mu.RLock()
	d, ok := m.devices[id]
	settings := m.settings
	factory := m.powerFactory
	m.mu.RUnlock()
	if !ok {
		return ErrNotFound
	}
	if factory == nil {
		return ErrPowerUnavailable
	}
	return factory(settings).PowerOff(ctx, d.Address)
}

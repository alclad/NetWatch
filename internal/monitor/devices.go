package monitor

import "strings"

// CRUD-операции над устройствами и настройками. Все они следуют одному
// шаблону: мутация под lock → снапшот → persist → publish подписчикам.

func (m *Monitor) AddDevice(in DeviceInput) (Device, error) {
	if err := validateInput(in); err != nil {
		return Device{}, err
	}
	m.mu.Lock()
	d := Device{ID: newID(), Name: in.Name, Address: in.Address, URL: in.URL, Group: strings.TrimSpace(in.Group), Status: "unknown"}
	m.devices[d.ID] = d
	s := m.snapshotLocked()
	m.mu.Unlock()
	if err := m.persist(s); err != nil {
		return d, err
	}
	m.publish(s)
	return d, nil
}

func (m *Monitor) UpdateDevice(id string, in DeviceInput) (Device, error) {
	if err := validateInput(in); err != nil {
		return Device{}, err
	}
	m.mu.Lock()
	d, ok := m.devices[id]
	if !ok {
		m.mu.Unlock()
		return Device{}, ErrNotFound
	}
	d.Name, d.Address, d.URL = in.Name, in.Address, in.URL
	d.Group = strings.TrimSpace(in.Group)
	m.devices[id] = d
	s := m.snapshotLocked()
	m.mu.Unlock()
	if err := m.persist(s); err != nil {
		return d, err
	}
	m.publish(s)
	return d, nil
}

func (m *Monitor) DeleteDevice(id string) error {
	m.mu.Lock()
	if _, ok := m.devices[id]; !ok {
		m.mu.Unlock()
		return ErrNotFound
	}
	delete(m.devices, id)
	s := m.snapshotLocked()
	m.mu.Unlock()
	if err := m.persist(s); err != nil {
		return err
	}
	m.publish(s)
	return nil
}

func (m *Monitor) UpdateSettings(v Settings) (Settings, error) {
	v = applySshDefaults(v)
	if err := validateSettings(v); err != nil {
		return Settings{}, err
	}
	m.mu.Lock()
	m.settings = v
	s := m.snapshotLocked()
	m.mu.Unlock()
	if err := m.persist(s); err != nil {
		return v, err
	}
	m.publish(s)
	return v, nil
}

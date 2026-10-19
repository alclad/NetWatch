package monitor

// Pub/Sub для подписчиков состояния (SSE-клиенты). Каналы буферизованы;
// если подписчик не успевает читать, событие отбрасывается — клиент всё равно
// получит актуальный снапшот при переподключении или по heartbeat.

func (m *Monitor) Subscribe() (<-chan State, func()) {
	m.mu.Lock()
	id := m.nextSub
	m.nextSub++
	ch := make(chan State, 8)
	m.subs[id] = ch
	m.mu.Unlock()
	return ch, func() {
		m.mu.Lock()
		if c, ok := m.subs[id]; ok {
			delete(m.subs, id)
			close(c)
		}
		m.mu.Unlock()
	}
}

func (m *Monitor) publish(s State) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	for _, ch := range m.subs {
		select {
		case ch <- s:
		default:
		}
	}
}

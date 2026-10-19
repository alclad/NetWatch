package monitor

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
)

type persisted struct {
	Devices  []Device `json:"devices"`
	Settings Settings `json:"settings"`
}

// persist атомарно записывает состояние на диск: временный файл в том же
// каталоге → write → sync → rename. Так при сбое не останется полусотранный конфиг.
func (m *Monitor) persist(s State) error {
	if m.configPath == "" {
		return nil
	}
	p := persisted{Devices: s.Devices, Settings: s.Settings}
	b, e := json.MarshalIndent(p, "", "  ")
	if e != nil {
		return e
	}
	m.writeMu.Lock()
	defer m.writeMu.Unlock()
	dir := filepath.Dir(m.configPath)
	f, e := os.CreateTemp(dir, ".monitor-")
	if e != nil {
		m.setSystemError(e)
		if errors.Is(e, os.ErrPermission) {
			return fmt.Errorf("%w: %v", ErrPermission, e)
		}
		return e
	}
	name := f.Name()
	defer os.Remove(name)
	if _, e = f.Write(append(b, '\n')); e == nil {
		e = f.Sync()
	}
	if ce := f.Close(); e == nil {
		e = ce
	}
	if e == nil {
		e = os.Rename(name, m.configPath)
	}
	if e != nil {
		m.setSystemError(e)
		if errors.Is(e, os.ErrPermission) {
			return fmt.Errorf("%w: %v", ErrPermission, e)
		}
		return e
	}
	return nil
}

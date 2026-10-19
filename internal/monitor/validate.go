package monitor

import (
	"errors"
	"net/url"
	"strings"
)

func validateSettings(s Settings) error {
	if s.IntervalSeconds < 1 || s.IntervalSeconds > 86400 {
		return errors.New("interval_seconds должен быть от 1 до 86400")
	}
	if s.TimeoutSeconds < 1 || s.TimeoutSeconds > 3600 {
		return errors.New("timeout_seconds должен быть от 1 до 3600")
	}
	if s.TimeoutSeconds > s.IntervalSeconds {
		return errors.New("timeout_seconds не должен превышать interval_seconds")
	}
	if s.FailureThreshold < 1 || s.FailureThreshold > 100 {
		return errors.New("failure_threshold должен быть от 1 до 100")
	}
	if s.Concurrency < 1 || s.Concurrency > 256 {
		return errors.New("concurrency должен быть от 1 до 256")
	}
	return nil
}

func validateInput(d DeviceInput) error {
	if strings.TrimSpace(d.Name) == "" {
		return errors.New("укажите имя устройства")
	}
	if len(d.Name) > 120 {
		return errors.New("имя слишком длинное (максимум 120 символов)")
	}
	addr := strings.TrimSpace(d.Address)
	if addr == "" {
		return errors.New("укажите адрес устройства")
	}
	if len(addr) > 255 || strings.ContainsAny(addr, " \t\r\n/\\") {
		return errors.New("адрес должен быть IPv4/IPv6-адресом или DNS-именем без пробелов")
	}
	if len(strings.TrimSpace(d.Group)) > 60 {
		return errors.New("название группы слишком длинное (максимум 60 символов)")
	}
	if d.URL != "" {
		u, e := url.ParseRequestURI(d.URL)
		if e != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
			return errors.New("ссылка должна начинаться с http:// или https://")
		}
	}
	return nil
}

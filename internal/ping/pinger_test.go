package ping

import (
	"context"
	"testing"
	"time"
)

// Не требует сети и специальных прав: несуществующий хост должен
// завершиться ошибкой резолва до отправки пакетов.
func TestUnresolvableHost(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	if _, err := (Pinger{}).Ping(ctx, "nonexistent-host.invalid."); err == nil {
		t.Fatal("ожидалась ошибка DNS для .invalid хоста")
	}
}

func TestCanceledContext(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := (Pinger{}).Ping(ctx, "127.0.0.1"); err == nil {
		t.Fatal("отменённый контекст должен давать ошибку")
	}
}

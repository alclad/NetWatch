package ping

import (
	"context"
	"errors"
	"fmt"
	goping "github.com/go-ping/ping"
	"net"
	"os"
	"syscall"
	"time"
)

// Pinger uses Linux datagram ICMP sockets unless Raw is explicitly selected.
type Pinger struct{ Raw bool }

func (client Pinger) Ping(ctx context.Context, address string) (time.Duration, error) {
	ips, err := net.DefaultResolver.LookupIPAddr(ctx, address)
	if err != nil {
		return 0, err
	}
	if len(ips) == 0 {
		return 0, fmt.Errorf("DNS не вернул адресов")
	}
	p, err := goping.NewPinger(ips[0].String())
	if err != nil {
		return 0, err
	}
	p.SetPrivileged(client.Raw)
	p.Count = 1
	p.Timeout = time.Second
	if deadline, ok := ctx.Deadline(); ok {
		p.Timeout = time.Until(deadline)
	}
	if err := ctx.Err(); err != nil {
		return 0, err
	}
	done := make(chan struct{})
	go func() {
		select {
		case <-ctx.Done():
			p.Stop()
		case <-done:
		}
	}()
	err = p.Run()
	close(done)
	if err != nil {
		if errors.Is(err, syscall.EPERM) || errors.Is(err, syscall.EACCES) {
			return 0, fmt.Errorf("ICMP: настройте ping_group_range или CAP_NET_RAW: %w", os.ErrPermission)
		}
		return 0, err
	}
	if err := ctx.Err(); err != nil {
		return 0, err
	}
	stats := p.Statistics()
	if stats.PacketsRecv == 0 {
		return 0, fmt.Errorf("нет ответа ICMP")
	}
	return stats.AvgRtt, nil
}

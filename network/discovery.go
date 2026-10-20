package network

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"time"
)

const discoveryRequest = "SNAKE_DISCOVER"

// Discover broadcasts a discovery request and collects rooms until the timeout
// or context deadline. Broadcast and loopback are attempted separately so the
// function also works on a single machine and in environments without broadcast.
func Discover(ctx context.Context, timeout time.Duration) ([]RoomInfo, error) {
	conn, err := net.ListenUDP("udp", &net.UDPAddr{})
	if err != nil {
		return nil, fmt.Errorf("bind discovery socket: %w", err)
	}
	defer conn.Close()

	deadline := time.Now().Add(timeout)
	if contextDeadline, ok := ctx.Deadline(); ok && contextDeadline.Before(deadline) {
		deadline = contextDeadline
	}
	_ = conn.SetReadDeadline(deadline)

	stop := make(chan struct{})
	defer close(stop)
	go func() {
		select {
		case <-ctx.Done():
			_ = conn.SetReadDeadline(time.Now())
		case <-stop:
		}
	}()

	request := []byte(discoveryRequest)
	// Broadcast is best effort: it often fails in containers and with VPNs.
	_, _ = conn.WriteToUDP(request, &net.UDPAddr{IP: net.IPv4bcast, Port: DiscoveryPort})
	_, _ = conn.WriteToUDP(request, &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1), Port: DiscoveryPort})

	rooms := make([]RoomInfo, 0, 4)
	seen := make(map[string]struct{})
	buffer := make([]byte, 4096)
	for {
		n, _, readErr := conn.ReadFromUDP(buffer)
		if readErr != nil {
			if ctx.Err() != nil {
				return rooms, ctx.Err()
			}
			// Timeouts and closed sockets both end the scan.
			return rooms, nil
		}
		var info RoomInfo
		if err := json.Unmarshal(buffer[:n], &info); err != nil {
			continue
		}
		if info.TCPPort <= 0 {
			continue
		}
		key := fmt.Sprintf("%s:%d", info.Address, info.TCPPort)
		if _, exists := seen[key]; exists {
			continue
		}
		seen[key] = struct{}{}
		rooms = append(rooms, info)
	}
}

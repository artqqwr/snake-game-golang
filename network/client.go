package network

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"sync"
	"sync/atomic"
	"time"
)

const (
	handshakeTimeout = 5 * time.Second
	writeTimeout     = 2 * time.Second
)

// Client is a guest connected to a Room.
type Client struct {
	PlayerID int
	Room     RoomInfo

	conn    net.Conn
	reader  *bufio.Reader
	encoder *json.Encoder
	writeMu sync.Mutex

	udp     *net.UDPConn
	udpAddr *net.UDPAddr

	snapshots chan Snapshot
	messages  chan Message
	done      chan struct{}
	closeOnce sync.Once
	sendSeq   atomic.Uint64
	lastSeq   atomic.Uint64
}

// Join connects to a host, performs the TCP handshake and starts reading both
// transports. The returned client is ready to receive snapshots and to send input.
func Join(ctx context.Context, address string) (*Client, error) {
	dialer := net.Dialer{Timeout: handshakeTimeout}
	conn, err := dialer.DialContext(ctx, "tcp", address)
	if err != nil {
		return nil, fmt.Errorf("join room: %w", err)
	}

	client := &Client{
		conn:      conn,
		encoder:   json.NewEncoder(conn),
		snapshots: make(chan Snapshot, 8),
		messages:  make(chan Message, 64),
		done:      make(chan struct{}),
	}
	stopWatch := make(chan struct{})
	go func() {
		select {
		case <-ctx.Done():
			_ = conn.Close()
		case <-stopWatch:
		}
	}()
	err = client.handshake()
	close(stopWatch)
	if err != nil {
		_ = conn.Close()
		return nil, err
	}
	if ctx.Err() != nil {
		_ = conn.Close()
		return nil, ctx.Err()
	}

	udpConn, err := net.ListenUDP("udp", &net.UDPAddr{})
	if err != nil {
		_ = conn.Close()
		return nil, fmt.Errorf("bind client udp: %w", err)
	}
	host, _, err := net.SplitHostPort(address)
	if err != nil {
		_ = udpConn.Close()
		_ = conn.Close()
		return nil, fmt.Errorf("parse host address: %w", err)
	}
	hostAddr, err := net.ResolveIPAddr("ip", host)
	if err != nil {
		_ = udpConn.Close()
		_ = conn.Close()
		return nil, fmt.Errorf("resolve host address: %w", err)
	}
	client.udp = udpConn
	client.udpAddr = &net.UDPAddr{IP: hostAddr.IP, Zone: hostAddr.Zone, Port: client.Room.UDPPort}

	go client.readSnapshots()
	go client.readControl()
	return client, nil
}

func (c *Client) handshake() error {
	_ = c.conn.SetReadDeadline(time.Now().Add(handshakeTimeout))
	c.reader = bufio.NewReaderSize(c.conn, 64*1024)

	hello := false
	info := false
	for !hello || !info {
		line, err := c.reader.ReadBytes('\n')
		if err != nil {
			return fmt.Errorf("handshake: %w", err)
		}
		var message Message
		if err := json.Unmarshal(line, &message); err != nil {
			continue
		}
		if message.Version != ProtocolVersion {
			return fmt.Errorf("handshake: unsupported protocol version %d", message.Version)
		}
		switch message.Type {
		case MessageHello:
			c.PlayerID = message.Player
			hello = true
		case MessageRoomInfo:
			if message.Room == nil {
				return errors.New("handshake: room info is missing")
			}
			c.Room = *message.Room
			info = true
		}
	}
	_ = c.conn.SetReadDeadline(time.Time{})
	return nil
}

// Snapshots exposes received world states. The channel keeps the newest state:
// stale snapshots are dropped and an overflow replaces the oldest queued one.
func (c *Client) Snapshots() <-chan Snapshot { return c.snapshots }

// Messages exposes control messages received over TCP.
func (c *Client) Messages() <-chan Message { return c.messages }

// ClearSnapshots drops every queued snapshot. It is used when a new round
// begins so state from the previous round cannot leak into it.
func (c *Client) ClearSnapshots() {
	for {
		select {
		case <-c.snapshots:
		default:
			return
		}
	}
}

// Disconnected is closed when the TCP control connection ends.
func (c *Client) Disconnected() <-chan struct{} { return c.done }

// SendReady notifies the host that the guest is ready to start.
func (c *Client) SendReady() error {
	return c.send(Message{Type: MessageReady, Version: ProtocolVersion, Player: c.PlayerID, Ready: true})
}

// SendReadyBot marks the client as a bot while reporting readiness. The host
// uses this only for display; bot decisions still travel over the normal input
// protocol.
func (c *Client) SendReadyBot() error {
	return c.send(Message{Type: MessageReady, Version: ProtocolVersion, Player: c.PlayerID, Ready: true, Bot: true})
}

// SendDisconnect asks the host to close the room for this guest.
func (c *Client) SendDisconnect(reason string) error {
	return c.send(Message{Type: MessageDisconnect, Version: ProtocolVersion, Player: c.PlayerID, Reason: reason})
}

// SendInput sends a direction intent over UDP. Sequence numbers are assigned
// here so callers never have to manage them.
func (c *Client) SendInput(direction int) error {
	if c.udp == nil || c.udpAddr == nil {
		return errors.New("client is not ready")
	}
	seq := c.sendSeq.Add(1)
	data := EncodeInput(c.PlayerID, Input{Direction: direction, Seq: seq})
	_, err := c.udp.WriteToUDP(data, c.udpAddr)
	return err
}

func (c *Client) send(message Message) error {
	c.writeMu.Lock()
	defer c.writeMu.Unlock()
	if c.conn == nil {
		return errors.New("client is closed")
	}
	_ = c.conn.SetWriteDeadline(time.Now().Add(writeTimeout))
	return c.encoder.Encode(message)
}

func (c *Client) readControl() {
	defer c.disconnect()
	scanner := bufio.NewScanner(c.reader)
	scanner.Buffer(make([]byte, 0, 4096), 64*1024)
	for scanner.Scan() {
		var message Message
		if err := json.Unmarshal(scanner.Bytes(), &message); err != nil {
			continue
		}
		if message.Version != ProtocolVersion {
			continue
		}
		c.queueMessage(message)
	}
}

// queueMessage keeps the newest control messages. A minimized guest may stop
// draining the queue for a while; dropping the oldest lobby update is safe,
// while losing a start or disconnect message would leave it stuck.
func (c *Client) queueMessage(message Message) {
	select {
	case c.messages <- message:
		return
	default:
	}
	select {
	case <-c.messages:
	default:
	}
	select {
	case c.messages <- message:
	default:
	}
}

func (c *Client) readSnapshots() {
	buffer := make([]byte, 64*1024)
	for {
		n, _, err := c.udp.ReadFromUDP(buffer)
		if err != nil {
			if c.isDisconnected() {
				return
			}
			time.Sleep(10 * time.Millisecond)
			continue
		}
		snapshot, err := DecodeSnapshot(buffer[:n])
		if err != nil {
			continue
		}
		if snapshot.Seq <= c.lastSeq.Load() {
			continue
		}
		c.lastSeq.Store(snapshot.Seq)
		select {
		case c.snapshots <- snapshot:
		default:
			// Keep the newest state: drop the oldest queued snapshot.
			select {
			case <-c.snapshots:
			default:
			}
			select {
			case c.snapshots <- snapshot:
			default:
			}
		}
	}
}

func (c *Client) disconnect() {
	if c == nil || c.done == nil {
		return
	}
	c.closeOnce.Do(func() {
		close(c.done)
	})
}

func (c *Client) isDisconnected() bool {
	select {
	case <-c.done:
		return true
	default:
		return false
	}
}

// Close releases the TCP and UDP sockets.
func (c *Client) Close() error {
	if c == nil {
		return nil
	}
	c.disconnect()
	if c.udp != nil {
		_ = c.udp.Close()
	}
	if c.conn != nil {
		return c.conn.Close()
	}
	return nil
}

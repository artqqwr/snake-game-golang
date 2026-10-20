package network

import (
	"context"
	"net"
	"strconv"
	"testing"
	"time"
)

const networkTestTimeout = 3 * time.Second

func startTestRoom(t *testing.T) *Room {
	t.Helper()
	room := NewRoom(RoomOptions{
		Name:          "test room",
		MapIndex:      2,
		Difficulty:    1,
		TCPPort:       -1,
		UDPPort:       -1,
		DiscoveryPort: -1,
	})
	ctx, cancel := context.WithCancel(context.Background())
	if err := room.Start(ctx); err != nil {
		cancel()
		t.Fatalf("start room: %v", err)
	}
	t.Cleanup(func() {
		room.Close()
		cancel()
	})
	return room
}

func joinTestRoom(t *testing.T, room *Room) *Client {
	t.Helper()
	address := net.JoinHostPort("127.0.0.1", strconv.Itoa(room.InfoSnapshot().TCPPort))
	client, err := Join(context.Background(), address)
	if err != nil {
		t.Fatalf("join room: %v", err)
	}
	t.Cleanup(func() {
		_ = client.Close()
	})
	return client
}

func waitRoomEvent(t *testing.T, events <-chan Event, kind EventType) Event {
	t.Helper()
	deadline := time.After(networkTestTimeout)
	for {
		select {
		case event := <-events:
			if event.Type == kind {
				return event
			}
		case <-deadline:
			t.Fatalf("timed out waiting for room event %q", kind)
		}
	}
}

func waitInput(t *testing.T, inputs <-chan InputEvent) InputEvent {
	t.Helper()
	select {
	case event := <-inputs:
		return event
	case <-time.After(networkTestTimeout):
		t.Fatal("timed out waiting for input")
	}
	return InputEvent{}
}

func waitMessageType(t *testing.T, messages <-chan Message, kind MessageType) Message {
	t.Helper()
	deadline := time.After(networkTestTimeout)
	for {
		select {
		case message := <-messages:
			if message.Type == kind {
				return message
			}
		case <-deadline:
			t.Fatalf("timed out waiting for control message %q", kind)
		}
	}
}

func waitSnapshot(t *testing.T, snapshots <-chan Snapshot) Snapshot {
	t.Helper()
	select {
	case snapshot := <-snapshots:
		return snapshot
	case <-time.After(networkTestTimeout):
		t.Fatal("timed out waiting for snapshot")
	}
	return Snapshot{}
}

func TestRoomGuestRoundTrip(t *testing.T) {
	room := startTestRoom(t)
	client := joinTestRoom(t, room)

	if client.PlayerID != 1 {
		t.Fatalf("guest id is %d, want 1", client.PlayerID)
	}
	if client.Room.Name != "test room" {
		t.Fatalf("guest room name is %q", client.Room.Name)
	}
	if client.Room.UDPPort != room.InfoSnapshot().UDPPort {
		t.Fatalf("guest UDP port %d does not match host port %d", client.Room.UDPPort, room.InfoSnapshot().UDPPort)
	}

	waitRoomEvent(t, room.Events(), EventJoined)

	if err := client.SendReady(); err != nil {
		t.Fatalf("send ready: %v", err)
	}
	ready := waitRoomEvent(t, room.Events(), EventReady)
	if !ready.Ready {
		t.Fatal("ready event is false")
	}

	if err := room.SendStart(3, 2, 4, 128, []int{0, 1}); err != nil {
		t.Fatalf("send start: %v", err)
	}
	start := waitMessageType(t, client.Messages(), MessageStart)
	if start.Start == nil {
		t.Fatalf("unexpected start message: %+v", start)
	}
	if start.Start.MapIndex != 3 || start.Start.Difficulty != 2 {
		t.Fatalf("start config is %+v", start.Start)
	}
	if start.Start.WorldSize != 128 || len(start.Start.ActivePlayers) != 2 {
		t.Fatalf("start roster is %+v", start.Start)
	}

	if err := client.SendInput(2); err != nil {
		t.Fatalf("send input: %v", err)
	}
	input := waitInput(t, room.Inputs())
	if input.Player != 1 || input.Input.Direction != 2 {
		t.Fatalf("host received input %+v", input)
	}

	sent := Snapshot{
		Seq:        5,
		Tick:       12,
		MapIndex:   3,
		Difficulty: 2,
		Players: []PlayerState{
			{ID: 0, Alive: true, Score: 1, Direction: 3, Segments: []Cell{{X: 4, Y: 5}, {X: 3, Y: 5}}},
			{ID: 1, Alive: true, Score: 2, Direction: 2, Segments: []Cell{{X: 9, Y: 9}, {X: 10, Y: 9}}},
		},
		Food: []Cell{{X: 15, Y: 16}},
	}
	if err := room.BroadcastSnapshot(sent); err != nil {
		t.Fatalf("broadcast snapshot: %v", err)
	}
	received := waitSnapshot(t, client.Snapshots())
	if received.Seq != sent.Seq || received.MapIndex != sent.MapIndex || len(received.Players) != 2 {
		t.Fatalf("received snapshot %+v, want %+v", received, sent)
	}
	if len(received.Food) != 1 || received.Food[0] != sent.Food[0] {
		t.Fatalf("received food %+v, want %+v", received.Food, sent.Food)
	}
}

func TestStartReachesEveryGuest(t *testing.T) {
	room := startTestRoom(t)
	first := joinTestRoom(t, room)
	second := joinTestRoom(t, room)
	waitRoomEvent(t, room.Events(), EventJoined)
	waitRoomEvent(t, room.Events(), EventJoined)

	if err := room.SendStart(1, 2, 7, 128, []int{0, 1, 2}); err != nil {
		t.Fatalf("send start: %v", err)
	}
	for index, client := range []*Client{first, second} {
		message := waitMessageType(t, client.Messages(), MessageStart)
		if message.Start == nil || len(message.Start.ActivePlayers) != 3 {
			t.Fatalf("guest %d start is %+v", index, message.Start)
		}
		if message.Start.Round != 7 || message.Start.WorldSize != 128 {
			t.Fatalf("guest %d start is %+v", index, message.Start)
		}
	}
}

func TestRoomDetectsGuestDisconnect(t *testing.T) {
	room := startTestRoom(t)
	client := joinTestRoom(t, room)
	waitRoomEvent(t, room.Events(), EventJoined)

	if err := client.Close(); err != nil {
		t.Fatalf("close client: %v", err)
	}
	waitRoomEvent(t, room.Events(), EventLeft)
	if room.HasGuest() {
		t.Fatal("room still reports a guest after disconnect")
	}
}

func TestRoomAcceptsMultipleGuests(t *testing.T) {
	room := startTestRoom(t)
	first := joinTestRoom(t, room)
	second := joinTestRoom(t, room)
	third := joinTestRoom(t, room)
	if first.PlayerID != 1 || second.PlayerID != 2 || third.PlayerID != 3 {
		t.Fatalf("guest ids are %d, %d, %d; want 1, 2, 3", first.PlayerID, second.PlayerID, third.PlayerID)
	}
	if ids := room.PlayerIDs(); len(ids) != 4 {
		t.Fatalf("room roster is %v, want four players", ids)
	}
}

func TestRoomFillsAndReusesAllPlayerSlots(t *testing.T) {
	room := startTestRoom(t)
	clients := make([]*Client, 0, MaxPlayers-1)
	for wantID := 1; wantID < MaxPlayers; wantID++ {
		client := joinTestRoom(t, room)
		if client.PlayerID != wantID {
			t.Fatalf("guest id is %d, want %d", client.PlayerID, wantID)
		}
		clients = append(clients, client)
	}
	if ids := room.PlayerIDs(); len(ids) != MaxPlayers {
		t.Fatalf("room roster is %v, want %d players", ids, MaxPlayers)
	}

	if err := clients[2].Close(); err != nil {
		t.Fatalf("close guest: %v", err)
	}
	waitRoomEvent(t, room.Events(), EventLeft)
	replacement := joinTestRoom(t, room)
	if replacement.PlayerID != 3 {
		t.Fatalf("replacement guest id is %d, want reused slot 3", replacement.PlayerID)
	}

	address := net.JoinHostPort("127.0.0.1", strconv.Itoa(room.InfoSnapshot().TCPPort))
	if extra, err := Join(context.Background(), address); err == nil {
		_ = extra.Close()
		t.Fatal("guest above MaxPlayers was accepted")
	}
}

func TestRoomRejectsGuestWhenFull(t *testing.T) {
	room := NewRoom(RoomOptions{
		Name:          "small room",
		TCPPort:       -1,
		UDPPort:       -1,
		DiscoveryPort: -1,
		MaxPlayers:    2,
	})
	ctx, cancel := context.WithCancel(context.Background())
	if err := room.Start(ctx); err != nil {
		cancel()
		t.Fatalf("start room: %v", err)
	}
	t.Cleanup(func() {
		room.Close()
		cancel()
	})
	first := joinTestRoom(t, room)
	if first.PlayerID != 1 {
		t.Fatalf("first guest id is %d", first.PlayerID)
	}
	address := net.JoinHostPort("127.0.0.1", strconv.Itoa(room.InfoSnapshot().TCPPort))
	if second, err := Join(context.Background(), address); err == nil {
		_ = second.Close()
		t.Fatal("guest above MaxPlayers was accepted")
	}
}

func TestRoomTracksLobbyReadiness(t *testing.T) {
	room := startTestRoom(t)
	client := joinTestRoom(t, room)
	waitRoomEvent(t, room.Events(), EventJoined)

	if roster, ok := room.StartRoster(); ok || roster != nil {
		t.Fatalf("room produced a start roster before the guest was ready: %v", roster)
	}
	if err := client.SendReady(); err != nil {
		t.Fatalf("send ready: %v", err)
	}
	waitRoomEvent(t, room.Events(), EventReady)
	if !room.AllReady() {
		t.Fatal("room is not ready after the guest reports ready")
	}
	roster, ok := room.StartRoster()
	if !ok || len(roster) != 2 || roster[0] != 0 || roster[1] != 1 {
		t.Fatalf("unexpected start roster %v (ok=%v)", roster, ok)
	}
	lobby := room.LobbySnapshot()
	if len(lobby.Players) != 2 || !lobby.Players[0].Ready || !lobby.Players[1].Ready {
		t.Fatalf("unexpected lobby snapshot: %+v", lobby)
	}
	if lobby.MaxPlayers != MaxPlayers {
		t.Fatalf("lobby max players is %d, want %d", lobby.MaxPlayers, MaxPlayers)
	}
}

func TestSnapshotReachesEveryGuest(t *testing.T) {
	room := startTestRoom(t)
	first := joinTestRoom(t, room)
	second := joinTestRoom(t, room)
	waitRoomEvent(t, room.Events(), EventJoined)
	waitRoomEvent(t, room.Events(), EventJoined)

	if err := first.SendInput(1); err != nil {
		t.Fatalf("first input: %v", err)
	}
	if err := second.SendInput(1); err != nil {
		t.Fatalf("second input: %v", err)
	}
	waitInput(t, room.Inputs())
	waitInput(t, room.Inputs())

	snapshot := Snapshot{
		Seq: 1,
		Players: []PlayerState{
			{ID: 0, Alive: true, Segments: []Cell{{X: 1, Y: 1}}},
			{ID: 1, Alive: true, Segments: []Cell{{X: 2, Y: 1}}},
			{ID: 2, Alive: true, Segments: []Cell{{X: 3, Y: 1}}},
		},
		Food: []Cell{{X: 10, Y: 10}},
	}
	if err := room.BroadcastSnapshot(snapshot); err != nil {
		t.Fatalf("broadcast snapshot: %v", err)
	}
	for index, client := range []*Client{first, second} {
		received := waitSnapshot(t, client.Snapshots())
		if len(received.Players) != 3 || len(received.Food) != 1 {
			t.Fatalf("guest %d received %+v", index, received)
		}
	}
}

func TestStaleInputIsIgnored(t *testing.T) {
	room := startTestRoom(t)
	client := joinTestRoom(t, room)
	waitRoomEvent(t, room.Events(), EventJoined)

	if err := client.SendInput(1); err != nil {
		t.Fatalf("send input: %v", err)
	}
	if event := waitInput(t, room.Inputs()); event.Input.Seq != 1 {
		t.Fatalf("first input seq is %d, want 1", event.Input.Seq)
	}

	stale := EncodeInput(1, Input{Direction: 2, Seq: 1})
	if _, err := client.udp.WriteToUDP(stale, client.udpAddr); err != nil {
		t.Fatalf("send stale input: %v", err)
	}
	select {
	case event := <-room.Inputs():
		t.Fatalf("stale input was forwarded: %+v", event)
	case <-time.After(300 * time.Millisecond):
	}
}

func TestControlMessageQueueKeepsNewest(t *testing.T) {
	client := &Client{messages: make(chan Message, 2)}
	client.queueMessage(Message{Type: MessageLobby})
	client.queueMessage(Message{Type: MessageLobby})
	client.queueMessage(Message{Type: MessageStart})
	client.queueMessage(Message{Type: MessageDisconnect})

	first := <-client.messages
	second := <-client.messages
	if first.Type != MessageStart || second.Type != MessageDisconnect {
		t.Fatalf("queued messages are %q and %q, want start and disconnect", first.Type, second.Type)
	}
}

func TestCloseIsIdempotent(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	room := NewRoom(RoomOptions{TCPPort: -1, UDPPort: -1, DiscoveryPort: -1})
	if err := room.Start(ctx); err != nil {
		t.Fatalf("start room: %v", err)
	}
	room.Close()
	room.Close()

	client := &Client{}
	if err := client.Close(); err != nil {
		t.Fatalf("close empty client: %v", err)
	}
}

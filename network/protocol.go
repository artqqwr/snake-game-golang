package network

// MessageType identifies a control message sent over TCP.
type MessageType string

const (
	MessageHello      MessageType = "hello"
	MessageRoomInfo   MessageType = "room_info"
	MessageLobby      MessageType = "lobby"
	MessageReady      MessageType = "ready"
	MessageStart      MessageType = "start"
	MessagePause      MessageType = "pause"
	MessageDisconnect MessageType = "disconnect"
)

// Message is a control message exchanged over the reliable TCP channel.
// Game state that changes every tick travels over UDP using the binary codec.
type Message struct {
	Type    MessageType `json:"type"`
	Version int         `json:"version"`
	Player  int         `json:"player,omitempty"`
	Ready   bool        `json:"ready,omitempty"`
	Paused  bool        `json:"paused,omitempty"`
	Reason  string      `json:"reason,omitempty"`
	Room    *RoomInfo   `json:"room,omitempty"`
	Lobby   *LobbyInfo  `json:"lobby,omitempty"`
	Start   *StartInfo  `json:"start,omitempty"`
}

// LobbyInfo is the current room roster and readiness state.
type LobbyInfo struct {
	Players    []LobbyPlayer `json:"players"`
	MaxPlayers int           `json:"max_players"`
	InRound    bool          `json:"in_round"`
}

// LobbyPlayer is one connected player in the lobby.
type LobbyPlayer struct {
	ID    int  `json:"id"`
	Ready bool `json:"ready"`
}

// StartInfo tells the guest which round configuration the host selected.
type StartInfo struct {
	MapIndex      int    `json:"map_index"`
	Difficulty    int    `json:"difficulty"`
	Round         uint32 `json:"round"`
	WorldSize     int    `json:"world_size"`
	ActivePlayers []int  `json:"active_players"`
}

// RoomInfo is both the discovery payload and the TCP handshake payload.
type RoomInfo struct {
	Name       string `json:"name"`
	Address    string `json:"address"`
	TCPPort    int    `json:"tcp_port"`
	UDPPort    int    `json:"udp_port"`
	Players    int    `json:"players"`
	MaxPlayers int    `json:"max_players"`
	MapIndex   int    `json:"map_index"`
	Difficulty int    `json:"difficulty"`
	InRound    bool   `json:"in_round"`
}

// Input is one direction intent produced by a guest.
type Input struct {
	Direction int
	Seq       uint64
}

// Snapshot is the authoritative world state broadcast by the host.
type Snapshot struct {
	Seq        uint64
	Round      uint32
	Tick       uint32
	MapIndex   int
	Difficulty int
	Paused     bool
	GameOver   bool
	Winner     int
	Players    []PlayerState
	Food       []Cell
}

// PlayerState is the network representation of one snake.
type PlayerState struct {
	ID        int
	Alive     bool
	Score     int
	Direction int
	Segments  []Cell
}

// Cell is a board coordinate.
type Cell struct {
	X int `json:"x"`
	Y int `json:"y"`
}

// ProtocolVersion must match on both ends; UDP packets carry the same version.
const ProtocolVersion = 3

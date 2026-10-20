package network

import (
	"encoding/binary"
	"errors"
	"fmt"
)

const (
	udpVersion   byte = ProtocolVersion
	udpTypeInput byte = 1
	udpTypeSnap  byte = 2

	// MaxFoodInPacket is a defensive limit for decoded snapshots.
	MaxFoodInPacket = 4096
)

// ErrMalformedPacket is returned when an UDP datagram cannot be decoded.
var ErrMalformedPacket = errors.New("malformed network packet")

type packetReader struct {
	data []byte
	pos  int
	err  error
}

func (r *packetReader) remaining() int {
	if r.err != nil {
		return 0
	}
	return len(r.data) - r.pos
}

func (r *packetReader) u8() byte {
	if r.err != nil || r.remaining() < 1 {
		r.err = ErrMalformedPacket
		return 0
	}
	value := r.data[r.pos]
	r.pos++
	return value
}

func (r *packetReader) u16() uint16 {
	if r.err != nil || r.remaining() < 2 {
		r.err = ErrMalformedPacket
		return 0
	}
	value := binary.LittleEndian.Uint16(r.data[r.pos:])
	r.pos += 2
	return value
}

func (r *packetReader) u32() uint32 {
	if r.err != nil || r.remaining() < 4 {
		r.err = ErrMalformedPacket
		return 0
	}
	value := binary.LittleEndian.Uint32(r.data[r.pos:])
	r.pos += 4
	return value
}

func (r *packetReader) u64() uint64 {
	if r.err != nil || r.remaining() < 8 {
		r.err = ErrMalformedPacket
		return 0
	}
	value := binary.LittleEndian.Uint64(r.data[r.pos:])
	r.pos += 8
	return value
}

func (r *packetReader) header(expected byte) bool {
	if r.u8() != udpVersion {
		r.err = fmt.Errorf("%w: unsupported version", ErrMalformedPacket)
		return false
	}
	if r.u8() != expected {
		r.err = fmt.Errorf("%w: unexpected message type", ErrMalformedPacket)
		return false
	}
	return r.err == nil
}

// EncodeInput produces the UDP packet a guest sends to the host.
func EncodeInput(player int, input Input) []byte {
	buffer := make([]byte, 0, 12)
	buffer = append(buffer, udpVersion, udpTypeInput, byte(player), byte(input.Direction))
	buffer = binary.LittleEndian.AppendUint64(buffer, input.Seq)
	return buffer
}

// DecodeInput parses a guest input packet.
func DecodeInput(data []byte) (int, Input, error) {
	reader := &packetReader{data: data}
	if !reader.header(udpTypeInput) {
		return 0, Input{}, reader.err
	}
	player := int(reader.u8())
	direction := reader.u8()
	seq := reader.u64()
	if reader.err != nil {
		return 0, Input{}, reader.err
	}
	if direction > 3 {
		return 0, Input{}, fmt.Errorf("%w: direction out of range", ErrMalformedPacket)
	}
	if player < 1 || player >= MaxPlayers {
		return 0, Input{}, fmt.Errorf("%w: player out of range", ErrMalformedPacket)
	}
	return player, Input{Direction: int(direction), Seq: seq}, nil
}

// EncodeSnapshot serialises the authoritative world state. Coordinates fit in
// one byte each, which keeps packets small even for long snakes.
func EncodeSnapshot(snapshot Snapshot) []byte {
	buffer := make([]byte, 0, 64)
	buffer = append(buffer, udpVersion, udpTypeSnap)

	var flags byte
	if snapshot.Paused {
		flags |= 1 << 0
	}
	if snapshot.GameOver {
		flags |= 1 << 1
	}
	winner := byte(0)
	if snapshot.Winner >= 0 {
		winner = byte(snapshot.Winner+1) & 0xff
	}

	buffer = append(buffer, flags, byte(snapshot.MapIndex), byte(snapshot.Difficulty), winner)
	buffer = binary.LittleEndian.AppendUint64(buffer, snapshot.Seq)
	buffer = binary.LittleEndian.AppendUint32(buffer, snapshot.Round)
	buffer = binary.LittleEndian.AppendUint32(buffer, snapshot.Tick)
	buffer = append(buffer, byte(len(snapshot.Players)))

	for _, player := range snapshot.Players {
		buffer = append(buffer, byte(player.ID))
		if player.Alive {
			buffer = append(buffer, 1)
		} else {
			buffer = append(buffer, 0)
		}
		buffer = binary.LittleEndian.AppendUint32(buffer, uint32(player.Score))
		buffer = append(buffer, byte(player.Direction))
		buffer = binary.LittleEndian.AppendUint16(buffer, uint16(len(player.Segments)))
		for _, cell := range player.Segments {
			buffer = append(buffer, byte(cell.X), byte(cell.Y))
		}
	}

	buffer = binary.LittleEndian.AppendUint16(buffer, uint16(len(snapshot.Food)))
	for _, food := range snapshot.Food {
		buffer = append(buffer, byte(food.X), byte(food.Y))
	}
	return buffer
}

// DecodeSnapshot parses a snapshot packet.
func DecodeSnapshot(data []byte) (Snapshot, error) {
	reader := &packetReader{data: data}
	if !reader.header(udpTypeSnap) {
		return Snapshot{}, reader.err
	}

	flags := reader.u8()
	mapIndex := int(reader.u8())
	difficulty := int(reader.u8())
	winner := int(reader.u8())
	seq := reader.u64()
	round := reader.u32()
	tick := reader.u32()
	playerCount := int(reader.u8())
	if reader.err != nil {
		return Snapshot{}, reader.err
	}
	if playerCount > MaxPlayers {
		return Snapshot{}, fmt.Errorf("%w: too many players", ErrMalformedPacket)
	}

	snapshot := Snapshot{
		Seq:        seq,
		Round:      round,
		Tick:       tick,
		MapIndex:   mapIndex,
		Difficulty: difficulty,
		Paused:     flags&(1<<0) != 0,
		GameOver:   flags&(1<<1) != 0,
		Winner:     winner - 1,
		Players:    make([]PlayerState, 0, playerCount),
	}
	for index := 0; index < playerCount; index++ {
		player := PlayerState{
			ID:        int(reader.u8()),
			Alive:     reader.u8() == 1,
			Score:     int(int32(reader.u32())),
			Direction: int(reader.u8()),
			Segments:  nil,
		}
		segmentCount := int(reader.u16())
		if reader.err != nil {
			return Snapshot{}, reader.err
		}
		if segmentCount > reader.remaining()/2 {
			return Snapshot{}, fmt.Errorf("%w: invalid segment count", ErrMalformedPacket)
		}
		if segmentCount > 0 {
			player.Segments = make([]Cell, 0, segmentCount)
			for segment := 0; segment < segmentCount; segment++ {
				player.Segments = append(player.Segments, Cell{X: int(reader.u8()), Y: int(reader.u8())})
			}
		}
		snapshot.Players = append(snapshot.Players, player)
	}

	foodCount := int(reader.u16())
	if reader.err != nil {
		return Snapshot{}, reader.err
	}
	if foodCount > reader.remaining()/2 || foodCount > MaxFoodInPacket {
		return Snapshot{}, fmt.Errorf("%w: invalid food count", ErrMalformedPacket)
	}
	if foodCount > 0 {
		snapshot.Food = make([]Cell, 0, foodCount)
		for food := 0; food < foodCount; food++ {
			snapshot.Food = append(snapshot.Food, Cell{X: int(reader.u8()), Y: int(reader.u8())})
		}
	}
	if reader.err != nil {
		return Snapshot{}, reader.err
	}
	return snapshot, nil
}

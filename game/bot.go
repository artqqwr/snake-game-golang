package game

import (
	"context"
	"log"
	"math/rand"
	"sync"
	"time"

	"github.com/artqqwr/snake-game-golang/network"
)

const (
	botJoinRetryDelay = 750 * time.Millisecond
	botHeartbeat      = time.Second
)

// BotManager owns a group of in-process bot clients. Bots use the ordinary
// guest protocol, so the host always validates their input and simulates their
// snakes just like a human player's.
type BotManager struct {
	ctx    context.Context
	cancel context.CancelFunc
	addr   string

	mu    sync.Mutex
	bots  []*botRunner
	wg    sync.WaitGroup
	next  int
	ended bool
}

type botRunner struct {
	cancel context.CancelFunc
	done   bool
}

// NewBotManager creates a manager. Add can be called from the UI goroutine;
// joining happens in the background so the window never blocks.
func NewBotManager(parent context.Context, address string) *BotManager {
	ctx, cancel := context.WithCancel(parent)
	return &BotManager{ctx: ctx, cancel: cancel, addr: address}
}

// Add starts one more bot. It returns false when the manager is closed.
func (m *BotManager) Add() bool {
	if m == nil {
		return false
	}
	m.mu.Lock()
	if m.ended {
		m.mu.Unlock()
		return false
	}
	index := m.next
	m.next++
	bctx, cancel := context.WithCancel(m.ctx)
	runner := &botRunner{cancel: cancel}
	m.bots = append(m.bots, runner)
	m.wg.Add(1)
	m.mu.Unlock()

	go func() {
		defer m.wg.Done()
		defer cancel()
		defer func() {
			m.mu.Lock()
			runner.done = true
			m.mu.Unlock()
		}()
		if err := runBot(bctx, m.addr, index, runner); err != nil && bctx.Err() == nil {
			log.Printf("bot %d: %v", index+1, err)
		}
	}()
	return true
}

// Count returns unfinished bots, including bots still connecting. Finished
// bots free their reserved slot so the UI can add a replacement.
func (m *BotManager) Count() int {
	if m == nil {
		return 0
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	count := 0
	for _, runner := range m.bots {
		if !runner.done {
			count++
		}
	}
	return count
}

// Close stops all bot clients and waits for their loops to finish.
func (m *BotManager) Close() {
	if m == nil {
		return
	}
	m.mu.Lock()
	if m.ended {
		m.mu.Unlock()
		return
	}
	m.ended = true
	m.cancel()
	bots := append([]*botRunner(nil), m.bots...)
	m.mu.Unlock()
	for _, runner := range bots {
		runner.cancel()
	}
	m.wg.Wait()
}

func runBot(ctx context.Context, address string, index int, runner *botRunner) error {
	var client *network.Client
	var err error
	for {
		client, err = network.Join(ctx, address)
		if err == nil {
			break
		}
		if ctx.Err() != nil {
			return ctx.Err()
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(botJoinRetryDelay):
		}
	}
	defer client.Close()
	return playBot(ctx, client, index)
}

func playBot(ctx context.Context, client *network.Client, index int) error {
	if err := client.SendReadyBot(); err != nil {
		return err
	}
	// Register the bot's UDP endpoint even while it waits for the next round.
	_ = client.SendInput(int(Right))

	var (
		start     *network.StartInfo
		active    bool
		obstacles []bool
		lastTick  uint32
		lastSent  = -1
		ready     bool
		paused    bool
	)
	heartbeat := time.NewTicker(botHeartbeat)
	defer heartbeat.Stop()
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-client.Disconnected():
			return nil
		case message := <-client.Messages():
			switch message.Type {
			case network.MessageStart:
				if message.Start == nil {
					continue
				}
				start = message.Start
				lastTick = 0
				lastSent = -1
				paused = false
				active = containsPlayer(start.ActivePlayers, client.PlayerID)
				ready = active
				if !active {
					// A mid-round joiner must wait for the next round.
					continue
				}
				obstacles = obstacleGrid(start.WorldSize, start.MapIndex)
				// Register and give the host an initial direction immediately;
				// the first authoritative snapshot will drive further choices.
				direction := int(Right)
				if err := client.SendInput(direction); err == nil {
					lastSent = direction
				}
			case network.MessagePause:
				paused = message.Paused
			case network.MessageDisconnect:
				return nil
			}
		case snapshot := <-client.Snapshots():
			if start == nil || snapshot.Round != start.Round || !active || !ready {
				continue
			}
			paused = snapshot.Paused
			if paused || snapshot.GameOver || snapshot.Tick == lastTick {
				continue
			}
			lastTick = snapshot.Tick
			own := findPlayer(snapshot.Players, client.PlayerID)
			if own == nil || !own.Alive || len(own.Segments) == 0 {
				continue
			}
			direction := chooseBotDirection(snapshot, own, obstacles, start.WorldSize, index)
			if direction != own.Direction || direction != lastSent {
				if err := client.SendInput(direction); err != nil {
					return err
				}
				lastSent = direction
			}
		case <-heartbeat.C:
			if start != nil && active && ready && !paused {
				_ = client.SendInput(lastSentOr(lastSent, int(Right)))
			}
		}
	}
}

func findPlayer(players []network.PlayerState, id int) *network.PlayerState {
	for index := range players {
		if players[index].ID == id {
			return &players[index]
		}
	}
	return nil
}

func lastSentOr(value, fallback int) int {
	if value >= int(Up) && value <= int(Right) {
		return value
	}
	return fallback
}

func obstacleGrid(worldSize, mapIndex int) []bool {
	if worldSize <= 0 {
		return nil
	}
	grid := make([]bool, worldSize*worldSize)
	definitions := maps(worldSize)
	if mapIndex < 0 || mapIndex >= len(definitions) {
		return grid
	}
	for _, obstacle := range definitions[mapIndex].Obstacles {
		if obstacle.X >= 0 && obstacle.X < worldSize && obstacle.Y >= 0 && obstacle.Y < worldSize {
			grid[obstacle.Y*worldSize+obstacle.X] = true
		}
	}
	return grid
}

type botCandidate struct {
	direction int
	area      int
	foodDist  int
}

// chooseBotDirection scores the four legal moves. It favours survivable open
// space first, then nearby food, and avoids cells next to enemy heads where a
// simultaneous move would cause a head-on collision.
func chooseBotDirection(snapshot network.Snapshot, own *network.PlayerState, obstacles []bool, worldSize, seed int) int {
	if own == nil || len(own.Segments) == 0 || worldSize <= 0 {
		return int(Right)
	}
	if len(obstacles) != worldSize*worldSize {
		obstacles = make([]bool, worldSize*worldSize)
	}

	blocked := make([]bool, len(obstacles))
	copy(blocked, obstacles)
	danger := make([]bool, len(obstacles))
	var ownTail Position

	for _, player := range snapshot.Players {
		if !player.Alive || len(player.Segments) == 0 {
			continue
		}
		for index, cell := range player.Segments {
			if cell.X < 0 || cell.X >= worldSize || cell.Y < 0 || cell.Y >= worldSize {
				continue
			}
			if player.ID == own.ID && index == len(player.Segments)-1 {
				// The tail vacates its cell on the next tick.
				ownTail = Position{X: cell.X, Y: cell.Y}
				continue
			}
			blocked[cell.Y*worldSize+cell.X] = true
		}
		if player.ID == own.ID {
			continue
		}
		head := player.Segments[0]
		for _, delta := range directionDelta {
			nx, ny := head.X+delta.X, head.Y+delta.Y
			if nx >= 0 && nx < worldSize && ny >= 0 && ny < worldSize {
				danger[ny*worldSize+nx] = true
			}
		}
	}

	if ownTail.X >= 0 && ownTail.X < worldSize && ownTail.Y >= 0 && ownTail.Y < worldSize {
		blocked[ownTail.Y*worldSize+ownTail.X] = false
	}

	food := make([]bool, len(blocked))
	for _, cell := range snapshot.Food {
		if cell.X >= 0 && cell.X < worldSize && cell.Y >= 0 && cell.Y < worldSize {
			food[cell.Y*worldSize+cell.X] = true
		}
	}

	head := own.Segments[0]
	rng := rand.New(rand.NewSource(int64(snapshot.Seq*31) + int64(own.ID*1009) + int64(seed)*7919 + int64(snapshot.Tick)))
	best := botCandidate{direction: -1, area: -1, foodDist: 1 << 20}
	fallback := botCandidate{direction: -1, area: -1, foodDist: 1 << 20}
	for direction := int(Up); direction <= int(Right); direction++ {
		if own.Direction >= int(Up) && own.Direction <= int(Right) && Direction(direction).IsOpposite(Direction(own.Direction)) {
			continue
		}
		delta := directionDelta[direction]
		nx, ny := head.X+delta.X, head.Y+delta.Y
		if nx < 0 || nx >= worldSize || ny < 0 || ny >= worldSize {
			continue
		}
		cell := ny*worldSize + nx
		if blocked[cell] {
			continue
		}
		if danger[cell] {
			continue
		}
		candidate := evaluateBotCandidate(direction, cell, blocked, danger, food, worldSize)
		if food[cell] {
			candidate.foodDist = 0
		}
		if candidate.area > best.area || (candidate.area == best.area && candidate.foodDist < best.foodDist) || (candidate.area == best.area && candidate.foodDist == best.foodDist && rng.Intn(2) == 0) {
			best = candidate
		}
		if fallback.area < 0 || candidate.area > fallback.area {
			fallback = candidate
		}
	}
	if best.direction >= 0 {
		return best.direction
	}
	if fallback.direction >= 0 {
		return fallback.direction
	}

	// No free neighbour: keep the current heading so the host resolves the
	// collision instead of the bot sending an invalid 180-degree turn.
	if own.Direction >= int(Up) && own.Direction <= int(Right) {
		return own.Direction
	}
	return int(Right)
}

func evaluateBotCandidate(direction, cell int, blocked, danger, food []bool, worldSize int) botCandidate {
	visited := make([]bool, len(blocked))
	queue := make([]int, 0, 256)
	queue = append(queue, cell)
	visited[cell] = true
	area := 0
	foodDist := 1 << 20
	for head := 0; head < len(queue); head++ {
		current := queue[head]
		area++
		x, y := current%worldSize, current/worldSize
		for _, delta := range directionDelta {
			nx, ny := x+delta.X, y+delta.Y
			if nx < 0 || nx >= worldSize || ny < 0 || ny >= worldSize {
				continue
			}
			next := ny*worldSize + nx
			if visited[next] || blocked[next] || danger[next] {
				continue
			}
			visited[next] = true
			queue = append(queue, next)
		}
	}
	// A separate breadth-first pass gives the nearest food distance from the
	// candidate cell. Reuse the queue allocation.
	for index := range visited {
		visited[index] = false
	}
	type node struct{ cell, depth int }
	nodes := make([]node, 0, 256)
	nodes = append(nodes, node{cell: cell})
	visited[cell] = true
	for head := 0; head < len(nodes); head++ {
		current := nodes[head]
		if food[current.cell] {
			foodDist = current.depth
			break
		}
		x, y := current.cell%worldSize, current.cell/worldSize
		for _, delta := range directionDelta {
			nx, ny := x+delta.X, y+delta.Y
			if nx < 0 || nx >= worldSize || ny < 0 || ny >= worldSize {
				continue
			}
			next := ny*worldSize + nx
			if visited[next] || blocked[next] || danger[next] {
				continue
			}
			visited[next] = true
			nodes = append(nodes, node{cell: next, depth: current.depth + 1})
		}
	}
	return botCandidate{direction: direction, area: area, foodDist: foodDist}
}

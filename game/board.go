package game

import (
	"math"
	"math/rand"
	"sort"

	"github.com/artqqwr/snake-game-golang/network"
	"github.com/hajimehoshi/ebiten/v2"
)

const (
	CellSize  = 20
	boardSize = 128

	maxFood      = 600
	droppedFoodS = 60 // seconds before dropped food disappears
)

// Direction is a movement axis.
type Direction int

const (
	Up Direction = iota
	Down
	Left
	Right
)

// Valid reports whether the direction is one of the four known values.
func (d Direction) Valid() bool {
	return d >= Up && d <= Right
}

// IsOpposite reports whether two directions are a 180 degree turn.
func (d Direction) IsOpposite(other Direction) bool {
	switch d {
	case Up:
		return other == Down
	case Down:
		return other == Up
	case Left:
		return other == Right
	case Right:
		return other == Left
	}
	return false
}

// Position is a board cell.
type Position struct {
	X, Y int
}

// CollisionMode selects what happens when a snake bites its own body.
type CollisionMode int

const (
	CollisionGameOver CollisionMode = iota
	CollisionTailCut
)

// MapDefinition is one selectable board layout.
type MapDefinition struct {
	Name      string
	Obstacles []Position
}

// Player is one snake plus its score and life state.
type Player struct {
	snake     *Snake
	score     int
	alive     bool
	direction Direction
	last      Position
}

// PlayerScore is a leaderboard row.
type PlayerScore struct {
	ID    int
	Score int
	Alive bool
}

// Board contains the whole simulation. In multiplayer the host owns the board
// and guests only render snapshots received from it.
type Board struct {
	size          int
	mapDefinition MapDefinition
	mapIndex      int
	difficulty    int
	obstacles     map[Position]struct{}
	obstacleCells []Position

	players []*Player
	food    []*Food
	localID int

	collisionMode CollisionMode

	tick        int
	tickRate    int
	ticks       uint32
	snapshotSeq uint64
	gameOver    bool
	winner      int
}

// NewSoloBoard creates a one-player board.
func NewSoloBoard(size, mapIndex, difficulty int, collisionMode CollisionMode) *Board {
	return newBoard(size, mapIndex, difficulty, collisionMode, []int{0}, 0)
}

// NewMultiplayerBoard creates a board for the given active network player IDs.
// Unused slots stay empty, so IDs do not have to be contiguous.
func NewMultiplayerBoard(size, mapIndex, difficulty, localID int, activePlayers []int) *Board {
	return newBoard(size, mapIndex, difficulty, CollisionTailCut, activePlayers, localID)
}

func newBoard(size, mapIndex, difficulty int, collisionMode CollisionMode, activePlayers []int, localID int) *Board {
	definitions := maps(size)
	if mapIndex < 0 || mapIndex >= len(definitions) {
		mapIndex = 0
	}

	active := make([]int, 0, len(activePlayers))
	seen := make(map[int]struct{}, len(activePlayers))
	for _, id := range activePlayers {
		if id < 0 || id >= network.MaxPlayers {
			continue
		}
		if _, exists := seen[id]; exists {
			continue
		}
		seen[id] = struct{}{}
		active = append(active, id)
	}
	if len(active) == 0 {
		active = []int{0}
	}
	sort.Ints(active)
	if _, exists := seen[localID]; !exists {
		localID = active[0]
	}

	tickRate := 6 - difficulty
	if tickRate < 2 {
		tickRate = 2
	}
	if tickRate > 6 {
		tickRate = 6
	}

	definition := definitions[mapIndex]
	board := &Board{
		size:          size,
		mapDefinition: definition,
		mapIndex:      mapIndex,
		difficulty:    difficulty,
		collisionMode: collisionMode,
		localID:       localID,
		tickRate:      tickRate,
		winner:        -1,
		obstacles:     make(map[Position]struct{}, len(definition.Obstacles)),
		obstacleCells: append([]Position(nil), definition.Obstacles...),
		players:       make([]*Player, network.MaxPlayers),
	}
	for _, obstacle := range definition.Obstacles {
		board.obstacles[obstacle] = struct{}{}
	}

	for index, id := range active {
		board.players[id] = &Player{
			snake:     NewSnake(board.findSpawn(index, len(active))),
			alive:     true,
			direction: Right,
			last:      Position{},
		}
		board.players[id].last = board.players[id].snake.Position()
	}
	for index := 0; index < foodTargetFor(size); index++ {
		board.spawnFood()
	}
	return board
}

// Update advances the simulation. It returns true when a simulation tick
// happened, which the host uses to decide when to broadcast a snapshot.
func (b *Board) Update() bool {
	if b.gameOver {
		return false
	}

	b.tick++
	if b.tick < b.tickRate {
		return false
	}
	b.tick = 0
	b.ticks++

	for _, player := range b.players {
		if player == nil || !player.alive || player.snake == nil {
			continue
		}
		if !b.inside(player.snake.NextPosition()) {
			// Soft world border: turn if requested, but do not leave the world
			// and do not kill the snake.
			player.snake.TurnInPlace()
			player.last = player.snake.Position()
			continue
		}
		player.snake.Move()
		player.direction = player.snake.Direction()
		player.last = player.snake.Position()
	}

	deaths := make([]bool, len(b.players))
	for id, player := range b.players {
		if player == nil || !player.alive || player.snake == nil {
			continue
		}
		head := player.snake.Position()
		if b.isObstacle(head) {
			deaths[id] = true
			continue
		}
		if b.hitsEnemy(id, head) {
			deaths[id] = true
			continue
		}
		if bodyIndex, hit := player.snake.BodyCollision(); hit {
			if b.collisionMode == CollisionGameOver {
				deaths[id] = true
			} else {
				player.snake.CutTail(bodyIndex)
			}
		}
	}

	anyDeath := false
	for id, player := range b.players {
		if deaths[id] {
			b.killPlayer(id)
			anyDeath = true
			continue
		}
		if player == nil || !player.alive || player.snake == nil {
			continue
		}
		b.eatFood(player)
	}
	b.expireFood()
	b.ensureFood()

	if anyDeath && b.aliveCount() <= 1 {
		b.finishRound()
	}
	return true
}

// QueueDirection applies a direction intent to a player for the next tick.
// Directions that would immediately leave the soft world border are ignored.
func (b *Board) QueueDirection(playerID int, direction Direction) {
	if !b.CanSteer(playerID, direction) {
		return
	}
	b.players[playerID].snake.SetDirection(direction)
}

// CanSteer reports whether a direction can be applied without leaving the
// world immediately.
func (b *Board) CanSteer(playerID int, direction Direction) bool {
	if playerID < 0 || playerID >= len(b.players) {
		return false
	}
	player := b.players[playerID]
	if player == nil || !player.alive || player.snake == nil || !direction.Valid() {
		return false
	}
	if player.snake.Direction().IsOpposite(direction) {
		return false
	}
	delta := directionDelta[direction]
	head := player.snake.Position()
	return b.inside(Position{X: head.X + delta.X, Y: head.Y + delta.Y})
}

// LocalSnake is the snake controlled by the local player, or nil when dead.
func (b *Board) LocalSnake() *Snake {
	if b == nil || b.localID < 0 || b.localID >= len(b.players) {
		return nil
	}
	player := b.players[b.localID]
	if player == nil || !player.alive {
		return nil
	}
	return player.snake
}

// Snake returns the snake of a player, or nil for an invalid or dead player.
func (b *Board) Snake(playerID int) *Snake {
	if b == nil || playerID < 0 || playerID >= len(b.players) {
		return nil
	}
	if b.players[playerID] == nil || !b.players[playerID].alive {
		return nil
	}
	return b.players[playerID].snake
}

// Score returns the score of a player.
func (b *Board) Score(playerID int) int {
	if b == nil || playerID < 0 || playerID >= len(b.players) || b.players[playerID] == nil {
		return 0
	}
	return b.players[playerID].score
}

// LocalScore returns the local player's score.
func (b *Board) LocalScore() int {
	if b == nil {
		return 0
	}
	return b.Score(b.localID)
}

// Alive reports whether a player is still in the round.
func (b *Board) Alive(playerID int) bool {
	if b == nil || playerID < 0 || playerID >= len(b.players) || b.players[playerID] == nil {
		return false
	}
	return b.players[playerID].alive
}

// LocalAlive reports whether the local player is still playing.
func (b *Board) LocalAlive() bool {
	if b == nil {
		return false
	}
	return b.Alive(b.localID)
}

// PlayerCount returns the number of active snakes in the round.
func (b *Board) PlayerCount() int {
	if b == nil {
		return 0
	}
	count := 0
	for _, player := range b.players {
		if player != nil {
			count++
		}
	}
	return count
}

// ActiveIDs returns the sorted active player IDs.
func (b *Board) ActiveIDs() []int {
	if b == nil {
		return nil
	}
	ids := make([]int, 0, len(b.players))
	for id, player := range b.players {
		if player != nil {
			ids = append(ids, id)
		}
	}
	sort.Ints(ids)
	return ids
}

// aliveCount counts snakes that are still playing.
func (b *Board) aliveCount() int {
	count := 0
	for _, player := range b.players {
		if player != nil && player.alive {
			count++
		}
	}
	return count
}

// AliveCount returns how many snakes are still alive.
func (b *Board) AliveCount() int {
	return b.aliveCount()
}

// LocalID returns the local player id.
func (b *Board) LocalID() int {
	if b == nil {
		return 0
	}
	return b.localID
}

// IsGameOver reports whether the round has ended.
func (b *Board) IsGameOver() bool {
	return b != nil && b.gameOver
}

// Winner returns the winning player id, or -1 for a draw.
func (b *Board) Winner() int {
	if b == nil {
		return -1
	}
	return b.winner
}

// MapName returns the display name of the current layout.
func (b *Board) MapName() string {
	if b == nil {
		return ""
	}
	return b.mapDefinition.Name
}

// Size returns the board size in cells.
func (b *Board) Size() int {
	if b == nil {
		return 0
	}
	return b.size
}

// WorldPixels returns the world size in screen pixels.
func (b *Board) WorldPixels() int {
	return b.Size() * CellSize
}

// CameraTarget returns the cell the camera should follow. If the local player
// is out, the current leader is followed.
func (b *Board) CameraTarget() Position {
	if b == nil {
		return Position{}
	}
	var local *Player
	if b.localID >= 0 && b.localID < len(b.players) {
		local = b.players[b.localID]
	}
	if local != nil && local.alive && local.snake != nil {
		return local.snake.Position()
	}
	if leader := b.leaderID(); leader >= 0 {
		return b.players[leader].snake.Position()
	}
	if local != nil {
		return local.last
	}
	return Position{X: b.size / 2, Y: b.size / 2}
}

func (b *Board) leaderID() int {
	best := -1
	bestScore := -1
	for id, player := range b.players {
		if player == nil || !player.alive || player.snake == nil {
			continue
		}
		if player.score > bestScore || (player.score == bestScore && (best < 0 || id < best)) {
			best = id
			bestScore = player.score
		}
	}
	return best
}

// Leaderboard returns up to limit active players, ordered by score.
func (b *Board) Leaderboard(limit int) []PlayerScore {
	if b == nil {
		return nil
	}
	scores := make([]PlayerScore, 0, len(b.players))
	for id, player := range b.players {
		if player == nil {
			continue
		}
		scores = append(scores, PlayerScore{ID: id, Score: player.score, Alive: player.alive})
	}
	sort.Slice(scores, func(i, j int) bool {
		if scores[i].Score != scores[j].Score {
			return scores[i].Score > scores[j].Score
		}
		return scores[i].ID < scores[j].ID
	})
	if limit > 0 && len(scores) > limit {
		scores = scores[:limit]
	}
	return scores
}

// BuildSnapshot serialises the current world state for the guests.
func (b *Board) BuildSnapshot(seq uint64, round uint32, paused bool) network.Snapshot {
	snapshot := network.Snapshot{
		Seq:        seq,
		Round:      round,
		Tick:       b.ticks,
		MapIndex:   b.mapIndex,
		Difficulty: b.difficulty,
		Paused:     paused,
		GameOver:   b.gameOver,
		Winner:     b.winner,
	}
	for id, player := range b.players {
		if player == nil {
			continue
		}
		state := network.PlayerState{
			ID:        id,
			Alive:     player.alive,
			Score:     player.score,
			Direction: int(player.direction),
		}
		if player.alive && player.snake != nil {
			state.Segments = cellsFromPositions(player.snake.Body())
		}
		snapshot.Players = append(snapshot.Players, state)
	}
	for _, food := range b.food {
		snapshot.Food = append(snapshot.Food, network.Cell{X: food.pos.X, Y: food.pos.Y})
	}
	return snapshot
}

// ApplySnapshot replaces the world state with an authoritative update. Stale
// snapshots are ignored.
func (b *Board) ApplySnapshot(snapshot network.Snapshot) bool {
	if b == nil || snapshot.Seq <= b.snapshotSeq {
		return false
	}
	b.snapshotSeq = snapshot.Seq

	for _, state := range snapshot.Players {
		if state.ID < 0 || state.ID >= len(b.players) {
			continue
		}
		player := b.players[state.ID]
		if player == nil {
			continue
		}
		player.score = state.Score
		if state.Alive && len(state.Segments) > 0 {
			if player.snake == nil {
				player.snake = NewSnake(Position{X: state.Segments[0].X, Y: state.Segments[0].Y})
			}
			player.snake.SetState(positionsFromCells(state.Segments), Direction(state.Direction))
			player.alive = true
			player.direction = Direction(state.Direction)
			player.last = player.snake.Position()
		} else {
			player.alive = false
			player.snake = nil
			if state.Direction >= 0 && state.Direction <= int(Right) {
				player.direction = Direction(state.Direction)
			}
			if len(state.Segments) > 0 {
				player.last = Position{X: state.Segments[0].X, Y: state.Segments[0].Y}
			}
		}
	}

	b.food = b.food[:0]
	for _, cell := range snapshot.Food {
		b.food = append(b.food, NewFood(Position{X: cell.X, Y: cell.Y}))
	}
	b.gameOver = snapshot.GameOver
	b.winner = snapshot.Winner
	return true
}

// Eliminate removes a player from the round, for example after a disconnect.
// Their body becomes food and the round ends if only one player remains.
func (b *Board) Eliminate(playerID int) {
	if b == nil || playerID < 0 || playerID >= len(b.players) {
		return
	}
	player := b.players[playerID]
	if player == nil || !player.alive || b.gameOver {
		return
	}
	b.killPlayer(playerID)
	if b.aliveCount() <= 1 {
		b.finishRound()
	}
}

// LastSnapshotSeq returns the sequence number of the last applied snapshot.
func (b *Board) LastSnapshotSeq() uint64 {
	if b == nil {
		return 0
	}
	return b.snapshotSeq
}

func (b *Board) eatFood(player *Player) {
	if player == nil || player.snake == nil {
		return
	}
	head := player.snake.Position()
	kept := b.food[:0]
	for _, food := range b.food {
		if food.Position() == head {
			player.snake.Grow()
			player.score++
			continue
		}
		kept = append(kept, food)
	}
	b.food = kept
}

func (b *Board) expireFood() {
	kept := b.food[:0]
	for _, food := range b.food {
		if food.ttl > 0 {
			food.ttl--
			if food.ttl == 0 {
				continue
			}
		}
		kept = append(kept, food)
	}
	b.food = kept
}

func (b *Board) ensureFood() {
	target := foodTargetFor(b.size)
	for len(b.food) < target {
		if !b.spawnFood() {
			return
		}
	}
}

func (b *Board) killPlayer(playerID int) {
	player := b.players[playerID]
	if player == nil || !player.alive {
		return
	}
	if player.snake != nil {
		body := append([]Position(nil), player.snake.Body()...)
		player.last = player.snake.Position()
		player.direction = player.snake.Direction()
		player.alive = false
		player.snake = nil
		b.dropBody(body)
		return
	}
	player.alive = false
}

func (b *Board) dropBody(body []Position) {
	if len(body) == 0 {
		return
	}
	dropCount := len(body) / 2
	if dropCount < 1 {
		dropCount = 1
	}
	step := len(body) / dropCount
	if step < 1 {
		step = 1
	}
	dropped := 0
	ttl := droppedFoodS * 60 / b.tickRate
	if ttl < 1 {
		ttl = 1
	}
	for index := 0; index < len(body) && dropped < dropCount; index += step {
		pos := body[index]
		if b.isObstacle(pos) {
			continue
		}
		if len(b.food) >= maxFood {
			break
		}
		b.food = append(b.food, &Food{pos: pos, ttl: ttl, dropped: true})
		dropped++
	}
}

func (b *Board) finishRound() {
	alive := 0
	winner := -1
	for id, player := range b.players {
		if player != nil && player.alive {
			alive++
			winner = id
		}
	}
	if alive == 1 {
		b.winner = winner
	} else {
		b.winner = -1
	}
	b.gameOver = true
}

func (b *Board) hitsEnemy(playerID int, head Position) bool {
	for id, player := range b.players {
		if id == playerID || player == nil || !player.alive || player.snake == nil {
			continue
		}
		if EnemyCollision(head, player.snake.Body()) {
			return true
		}
	}
	return false
}

func (b *Board) inside(pos Position) bool {
	return pos.X >= 0 && pos.X < b.size && pos.Y >= 0 && pos.Y < b.size
}

func (b *Board) isObstacle(pos Position) bool {
	_, exists := b.obstacles[pos]
	return exists
}

func (b *Board) snakeAt(pos Position) bool {
	for _, player := range b.players {
		if player == nil || player.snake == nil {
			continue
		}
		if player.snake.Contains(pos) {
			return true
		}
	}
	return false
}

func (b *Board) foodAt(pos Position) bool {
	for _, food := range b.food {
		if food.pos == pos {
			return true
		}
	}
	return false
}

func (b *Board) findSpawn(index, total int) Position {
	center := b.size / 2
	preferred := Position{X: center, Y: center}
	if total > 1 {
		angle := 2*math.Pi*float64(index)/float64(total) + math.Pi/4
		radius := float64(b.size) * 0.32
		preferred = Position{
			X: center + int(math.Round(math.Cos(angle)*radius)),
			Y: center + int(math.Round(math.Sin(angle)*radius)),
		}
	}
	if b.canSpawn(preferred) {
		return preferred
	}
	for radius := 1; radius < b.size; radius++ {
		for offsetY := -radius; offsetY <= radius; offsetY++ {
			for offsetX := -radius; offsetX <= radius; offsetX++ {
				if absInt(offsetX) != radius && absInt(offsetY) != radius {
					continue
				}
				candidate := Position{X: preferred.X + offsetX, Y: preferred.Y + offsetY}
				if b.canSpawn(candidate) {
					return candidate
				}
			}
		}
	}
	return Position{X: b.size/2 + initialSnakeLength, Y: b.size / 2}
}

func (b *Board) canSpawn(pos Position) bool {
	if pos.X < initialSnakeLength+1 || pos.X >= b.size-2 || pos.Y < 2 || pos.Y >= b.size-2 {
		return false
	}
	for offset := 0; offset < initialSnakeLength; offset++ {
		cell := Position{X: pos.X - offset, Y: pos.Y}
		if b.isObstacle(cell) || b.snakeAt(cell) {
			return false
		}
	}
	return true
}

func (b *Board) spawnFood() bool {
	for attempt := 0; attempt < b.size*4; attempt++ {
		pos := Position{X: rand.Intn(b.size), Y: rand.Intn(b.size)}
		if b.isObstacle(pos) || b.snakeAt(pos) || b.foodAt(pos) {
			continue
		}
		b.food = append(b.food, NewFood(pos))
		return true
	}
	free := b.freeCells()
	if len(free) == 0 {
		return false
	}
	b.food = append(b.food, NewFood(free[rand.Intn(len(free))]))
	return true
}

func (b *Board) freeCells() []Position {
	free := make([]Position, 0, b.size*b.size)
	for x := 0; x < b.size; x++ {
		for y := 0; y < b.size; y++ {
			pos := Position{X: x, Y: y}
			if b.isObstacle(pos) || b.snakeAt(pos) || b.foodAt(pos) {
				continue
			}
			free = append(free, pos)
		}
	}
	return free
}

func foodTargetFor(size int) int {
	target := size * size / 160
	if target < 12 {
		target = 12
	}
	if target > 180 {
		target = 180
	}
	return target
}

// Draw renders the visible part of the world using the camera.
func (b *Board) Draw(screen *ebiten.Image, camera *Camera) {
	screen.Fill(backgroundColor)
	if b == nil || camera == nil {
		return
	}
	camX, camY := camera.Position()
	viewWidth := screenWidth
	viewHeight := screenWidth

	drawWorldBorder(screen, b, camX, camY)

	for _, pos := range b.obstacleCells {
		screenX := float64(pos.X*CellSize) - camX
		screenY := hudHeight + float64(pos.Y*CellSize) - camY
		if screenX < -CellSize || screenX > float64(viewWidth) || screenY < hudHeight-CellSize || screenY > hudHeight+float64(viewHeight) {
			continue
		}
		fillRect(screen, screenX, screenY, CellSize, CellSize, obstacleColor)
	}

	for _, food := range b.food {
		screenX := int(math.Round(float64(food.pos.X*CellSize) - camX))
		screenY := int(math.Round(hudHeight + float64(food.pos.Y*CellSize) - camY))
		if screenX < -CellSize || screenX > viewWidth || screenY < hudHeight-CellSize || screenY > hudHeight+viewHeight {
			continue
		}
		food.DrawAt(screen, screenX, screenY)
	}

	for id, player := range b.players {
		if player == nil || !player.alive || player.snake == nil {
			continue
		}
		colors := snakePalette[id%len(snakePalette)]
		for segmentIndex, pos := range player.snake.Body() {
			screenX := float64(pos.X*CellSize) - camX
			screenY := hudHeight + float64(pos.Y*CellSize) - camY
			if screenX < -CellSize || screenX > float64(viewWidth) || screenY < hudHeight-CellSize || screenY > hudHeight+float64(viewHeight) {
				continue
			}
			clr := colors.body
			if segmentIndex == 0 {
				clr = colors.head
			}
			fillRect(screen, screenX, screenY, CellSize-1, CellSize-1, clr)
		}
		if id == b.localID && len(player.snake.Body()) > 0 {
			head := player.snake.Position()
			screenX := float64(head.X*CellSize) - camX
			screenY := hudHeight + float64(head.Y*CellSize) - camY
			fillRect(screen, screenX+7, screenY+7, 5, 5, localMarkerColor)
		}
	}
}

func drawWorldBorder(screen *ebiten.Image, b *Board, camX, camY float64) {
	worldPixels := float64(b.WorldPixels())
	const borderWidth = 3.0
	leftEdge := -camX
	rightEdge := worldPixels - camX
	topEdge := -camY
	bottomEdge := worldPixels - camY
	if leftEdge >= -borderWidth && leftEdge <= borderWidth {
		fillRect(screen, 0, hudHeight, borderWidth, screenWidth, worldBorderColor)
	}
	if rightEdge >= screenWidth-borderWidth && rightEdge <= screenWidth+borderWidth {
		fillRect(screen, screenWidth-borderWidth, hudHeight, borderWidth, screenWidth, worldBorderColor)
	}
	if topEdge >= -borderWidth && topEdge <= borderWidth {
		fillRect(screen, 0, hudHeight, screenWidth, borderWidth, worldBorderColor)
	}
	if bottomEdge >= screenWidth-borderWidth && bottomEdge <= screenWidth+borderWidth {
		fillRect(screen, 0, hudHeight+screenWidth-borderWidth, screenWidth, borderWidth, worldBorderColor)
	}
}

func cellsFromPositions(positions []Position) []network.Cell {
	cells := make([]network.Cell, 0, len(positions))
	for _, position := range positions {
		cells = append(cells, network.Cell{X: position.X, Y: position.Y})
	}
	return cells
}

func positionsFromCells(cells []network.Cell) []Position {
	positions := make([]Position, 0, len(cells))
	for _, cell := range cells {
		positions = append(positions, Position{X: cell.X, Y: cell.Y})
	}
	return positions
}

func absInt(value int) int {
	if value < 0 {
		return -value
	}
	return value
}

func maps(size int) []MapDefinition {
	center := size / 2
	wellWidth := size / 6
	if wellWidth < 7 {
		wellWidth = 7
	}
	wellX := center - wellWidth/2
	wellY := center - wellWidth/2
	well := rectangle(wellX, wellY, wellWidth, wellWidth)
	// Leave an entrance so the interior is not an inaccessible food trap.
	entranceStart := wellX + wellWidth/2 - 1
	entranceEnd := entranceStart + 2
	filtered := well[:0]
	for _, cell := range well {
		if cell.Y == wellY+wellWidth-1 && cell.X >= entranceStart && cell.X <= entranceEnd {
			continue
		}
		filtered = append(filtered, cell)
	}
	well = filtered

	inset := size / 4
	corridors := line(inset, inset, size-inset-1, inset)
	corridors = append(corridors, line(inset, size-inset-1, size-inset-1, size-inset-1)...)

	cross := line(center, size/10, center, size-size/10-1)
	cross = append(cross, line(size/10, center, size-size/10-1, center)...)

	// The maze is a ring with openings on every side, so it divides the map
	// into connected rooms instead of trapping players or food inside.
	maze := gappedLine(size/6, size/6, size-size/6-1, size/6)
	maze = append(maze, gappedLine(size/6, size-size/6-1, size-size/6-1, size-size/6-1)...)
	maze = append(maze, gappedLine(size/6, size/6, size/6, size-size/6-1)...)
	maze = append(maze, gappedLine(size-size/6-1, size/6, size-size/6-1, size-size/6-1)...)

	return []MapDefinition{
		{Name: "OPEN"},
		{Name: "WELL", Obstacles: well},
		{Name: "CORRIDORS", Obstacles: corridors},
		{Name: "CROSS", Obstacles: cross},
		{Name: "MAZE", Obstacles: maze},
	}
}

func line(x1, y1, x2, y2 int) []Position {
	var result []Position
	stepX, stepY := 0, 0
	switch {
	case x1 < x2:
		stepX = 1
	case x1 > x2:
		stepX = -1
	}
	switch {
	case y1 < y2:
		stepY = 1
	case y1 > y2:
		stepY = -1
	}
	for x, y := x1, y1; ; x, y = x+stepX, y+stepY {
		result = append(result, Position{X: x, Y: y})
		if x == x2 && y == y2 {
			break
		}
	}
	return result
}

// gappedLine builds a straight wall with an opening in the middle.
func gappedLine(x1, y1, x2, y2 int) []Position {
	cells := line(x1, y1, x2, y2)
	const gap = 3
	if len(cells) <= gap*2+2 {
		return cells
	}
	middle := len(cells) / 2
	result := make([]Position, 0, len(cells)-gap*2)
	result = append(result, cells[:middle-gap]...)
	result = append(result, cells[middle+gap:]...)
	return result
}

func rectangle(x, y, width, height int) []Position {
	var result []Position
	for currentX := x; currentX < x+width; currentX++ {
		result = append(result, Position{X: currentX, Y: y}, Position{X: currentX, Y: y + height - 1})
	}
	for currentY := y + 1; currentY < y+height-1; currentY++ {
		result = append(result, Position{X: x, Y: currentY}, Position{X: x + width - 1, Y: currentY})
	}
	return result
}

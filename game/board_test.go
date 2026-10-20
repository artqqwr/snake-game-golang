package game

import (
	"testing"

	"github.com/artqqwr/snake-game-golang/network"
)

func TestMapsContainFiveLayouts(t *testing.T) {
	if got := len(maps(32)); got != 5 {
		t.Fatalf("maps() returned %d layouts, want 5", got)
	}
	if got := len(maps(boardSize)); got != 5 {
		t.Fatalf("maps(boardSize) returned %d layouts, want 5", got)
	}
}

func TestMapFreeCellsAreReachable(t *testing.T) {
	for _, size := range []int{32, boardSize} {
		for mapIndex, definition := range maps(size) {
			obstacles := make(map[Position]struct{}, len(definition.Obstacles))
			for _, obstacle := range definition.Obstacles {
				obstacles[obstacle] = struct{}{}
			}
			start := Position{X: -1, Y: -1}
			freeCount := 0
			for y := 0; y < size; y++ {
				for x := 0; x < size; x++ {
					pos := Position{X: x, Y: y}
					if _, blocked := obstacles[pos]; blocked {
						continue
					}
					freeCount++
					if start.X < 0 {
						start = pos
					}
				}
			}
			if start.X < 0 {
				t.Fatalf("map %d at size %d has no free cells", mapIndex, size)
			}

			visited := map[Position]struct{}{start: {}}
			queue := []Position{start}
			for len(queue) > 0 {
				current := queue[0]
				queue = queue[1:]
				for _, delta := range []Position{{X: 1}, {X: -1}, {Y: 1}, {Y: -1}} {
					next := Position{X: current.X + delta.X, Y: current.Y + delta.Y}
					if next.X < 0 || next.X >= size || next.Y < 0 || next.Y >= size {
						continue
					}
					if _, blocked := obstacles[next]; blocked {
						continue
					}
					if _, seen := visited[next]; seen {
						continue
					}
					visited[next] = struct{}{}
					queue = append(queue, next)
				}
			}
			if len(visited) != freeCount {
				t.Fatalf("map %q at size %d has %d inaccessible free cells", definition.Name, size, freeCount-len(visited))
			}
		}
	}
}

func TestSoloBoardHasOneSnake(t *testing.T) {
	board := NewSoloBoard(32, 0, 1, CollisionGameOver)
	if board.PlayerCount() != 1 {
		t.Fatalf("solo board has %d players, want 1", board.PlayerCount())
	}
	if !board.Alive(0) {
		t.Fatal("solo snake should be alive")
	}
}

func TestMultiplayerBoardUsesSparsePlayerIDs(t *testing.T) {
	board := NewMultiplayerBoard(32, 0, 1, 2, []int{0, 2, 5})
	if board.PlayerCount() != 3 {
		t.Fatalf("multiplayer board has %d players, want 3", board.PlayerCount())
	}
	if board.LocalID() != 2 {
		t.Fatalf("local id is %d, want 2", board.LocalID())
	}
	if board.Snake(1) != nil {
		t.Fatal("inactive player 1 has a snake")
	}
	if board.Snake(0).Position() == board.Snake(2).Position() {
		t.Fatal("snakes spawned on the same cell")
	}
}

func TestSelfCollisionGameOver(t *testing.T) {
	board := NewSoloBoard(32, 0, 2, CollisionGameOver)
	board.tickRate = 1
	board.food = nil
	snake := board.LocalSnake()
	snake.body = []Position{{X: 5, Y: 5}, {X: 5, Y: 6}, {X: 4, Y: 6}, {X: 4, Y: 5}}
	snake.direction = Down
	snake.next = Down
	snake.queued = false

	board.Update()

	if !board.IsGameOver() {
		t.Fatal("expected self-collision to end the game")
	}
	if board.Alive(0) {
		t.Fatal("self-collision should remove the solo snake")
	}
}

func TestSelfCollisionCutsTail(t *testing.T) {
	board := NewSoloBoard(32, 0, 2, CollisionTailCut)
	board.tickRate = 1
	board.food = nil
	snake := board.LocalSnake()
	snake.body = []Position{{X: 5, Y: 5}, {X: 5, Y: 6}, {X: 4, Y: 6}, {X: 4, Y: 5}}
	snake.direction = Down
	snake.next = Down
	snake.queued = false

	board.Update()

	if board.IsGameOver() {
		t.Fatal("tail-cut mode should continue after self-collision")
	}
	if got := len(snake.body); got != 2 {
		t.Fatalf("tail-cut mode left %d segments, want 2", got)
	}
}

func TestFoodDoesNotSpawnOnObstaclesOrSnakes(t *testing.T) {
	board := NewSoloBoard(32, 3, 1, CollisionGameOver)
	if len(board.food) != foodTargetFor(32) {
		t.Fatalf("board has %d food, want %d", len(board.food), foodTargetFor(32))
	}
	for _, food := range board.food {
		if board.isObstacle(food.Position()) {
			t.Fatalf("food spawned on obstacle at %+v", food.Position())
		}
		if board.snakeAt(food.Position()) {
			t.Fatalf("food spawned on snake at %+v", food.Position())
		}
	}
}

func TestSoftBorderStopsWithoutDeath(t *testing.T) {
	board := NewSoloBoard(32, 0, 0, CollisionGameOver)
	board.tickRate = 1
	board.food = nil
	snake := board.LocalSnake()
	snake.body = []Position{{X: 0, Y: 5}, {X: 1, Y: 5}, {X: 2, Y: 5}}
	snake.direction = Left
	snake.next = Left
	snake.queued = false

	board.Update()

	if board.IsGameOver() {
		t.Fatal("soft border should not kill the snake")
	}
	if got := snake.Position(); got != (Position{X: 0, Y: 5}) {
		t.Fatalf("head left the world: %+v", got)
	}
	if board.CanSteer(0, Left) {
		t.Fatal("outward direction should be rejected at the border")
	}
	if !board.CanSteer(0, Up) {
		t.Fatal("inward direction should be accepted at the border")
	}
	board.QueueDirection(0, Left)
	if got := snake.NextDirection(); got != Left {
		t.Fatalf("blocked direction changed the next direction to %v", got)
	}
}

func TestEnemyCollisionKillsAndDropsFood(t *testing.T) {
	board := NewMultiplayerBoard(32, 0, 0, 0, []int{0, 1})
	board.tickRate = 1
	board.food = nil

	first := board.Snake(0)
	first.body = []Position{{X: 5, Y: 5}, {X: 4, Y: 5}, {X: 3, Y: 5}}
	first.direction = Right
	first.next = Right
	first.queued = false

	second := board.Snake(1)
	second.body = []Position{{X: 6, Y: 5}, {X: 7, Y: 5}, {X: 8, Y: 5}}
	second.direction = Right
	second.next = Right
	second.queued = false

	board.Update()

	if !board.IsGameOver() {
		t.Fatal("expected the round to end after hitting the enemy")
	}
	if board.Winner() != 1 {
		t.Fatalf("winner is %d, want 1", board.Winner())
	}
	if board.Alive(0) {
		t.Fatal("player 0 should be dead")
	}
	if board.Snake(0) != nil {
		t.Fatal("dead player still has a snake")
	}
	if len(board.food) == 0 {
		t.Fatal("dead snake did not leave food")
	}
}

func TestDropBodyKeepsHalfOfTheSnake(t *testing.T) {
	board := NewSoloBoard(32, 0, 0, CollisionGameOver)
	board.food = nil
	body := []Position{{X: 4, Y: 4}, {X: 5, Y: 4}, {X: 6, Y: 4}, {X: 7, Y: 4}, {X: 8, Y: 4}, {X: 9, Y: 4}}
	board.dropBody(body)
	if len(board.food) != 3 {
		t.Fatalf("six-segment snake dropped %d pellets, want 3", len(board.food))
	}
	for _, food := range board.food {
		if !food.dropped {
			t.Fatal("body food is not marked as dropped")
		}
		if food.ttl <= 0 {
			t.Fatal("dropped food has no lifetime")
		}
	}

	board.food = nil
	board.dropBody(body[:3])
	if len(board.food) != 1 {
		t.Fatalf("three-segment snake dropped %d pellets, want 1", len(board.food))
	}
}

func TestEatingFoodGrowsSnake(t *testing.T) {
	board := NewSoloBoard(32, 0, 0, CollisionGameOver)
	board.tickRate = 1
	board.food = []*Food{NewFood(Position{X: 6, Y: 5})}
	snake := board.LocalSnake()
	snake.body = []Position{{X: 5, Y: 5}, {X: 4, Y: 5}, {X: 3, Y: 5}}
	snake.direction = Right
	snake.next = Right
	snake.queued = false

	board.Update()

	if got := board.LocalScore(); got != 1 {
		t.Fatalf("score is %d, want 1", got)
	}
	if got := len(snake.Body()); got != 4 {
		t.Fatalf("snake length is %d, want 4", got)
	}
}

func TestQueueDirectionAllowsOneTurnPerTick(t *testing.T) {
	snake := NewSnake(Position{X: 10, Y: 10})
	snake.SetDirection(Up)
	snake.SetDirection(Left)
	if got := snake.NextDirection(); got != Up {
		t.Fatalf("second turn in the same tick was queued: %v", got)
	}
	snake.Move()
	snake.SetDirection(Left)
	if got := snake.NextDirection(); got != Left {
		t.Fatalf("turn after the tick was ignored: %v", got)
	}
}

func TestReverseDirectionIsIgnored(t *testing.T) {
	snake := NewSnake(Position{X: 10, Y: 10})
	snake.Steer(Left)
	if got := snake.NextDirection(); got != Right {
		t.Fatalf("reverse turn changed direction to %v", got)
	}
}

func TestApplySnapshotUpdatesWorld(t *testing.T) {
	board := NewMultiplayerBoard(32, 0, 0, 1, []int{0, 1})
	snapshot := network.Snapshot{
		Seq:      7,
		MapIndex: 0,
		Players: []network.PlayerState{
			{ID: 0, Alive: true, Score: 4, Direction: int(Left), Segments: []network.Cell{{X: 1, Y: 1}, {X: 2, Y: 1}, {X: 3, Y: 1}}},
			{ID: 1, Alive: true, Score: 1, Direction: int(Up), Segments: []network.Cell{{X: 10, Y: 10}, {X: 10, Y: 11}, {X: 10, Y: 12}}},
		},
		Food: []network.Cell{{X: 20, Y: 20}},
	}

	if !board.ApplySnapshot(snapshot) {
		t.Fatal("snapshot was not applied")
	}
	if got := board.Score(0); got != 4 {
		t.Fatalf("player 0 score is %d, want 4", got)
	}
	if got := board.Snake(0).Position(); got != (Position{X: 1, Y: 1}) {
		t.Fatalf("player 0 head is %+v, want {1 1}", got)
	}
	if len(board.food) != 1 || board.food[0].Position() != (Position{X: 20, Y: 20}) {
		t.Fatalf("food is %+v, want one pellet at {20 20}", board.food)
	}
	if board.ApplySnapshot(snapshot) {
		t.Fatal("stale snapshot should be ignored")
	}
}

func TestApplySnapshotMarksDeadPlayer(t *testing.T) {
	board := NewMultiplayerBoard(32, 0, 0, 1, []int{0, 1})
	snapshot := network.Snapshot{
		Seq: 1,
		Players: []network.PlayerState{
			{ID: 0, Alive: true, Score: 4, Direction: int(Left), Segments: []network.Cell{{X: 1, Y: 1}, {X: 2, Y: 1}}},
			{ID: 1, Alive: false, Score: 1, Direction: int(Up)},
		},
	}
	if !board.ApplySnapshot(snapshot) {
		t.Fatal("snapshot was not applied")
	}
	if board.LocalAlive() {
		t.Fatal("local player should be marked dead")
	}
	if board.LocalSnake() != nil {
		t.Fatal("dead local player still has a snake")
	}
	if board.CameraTarget() != (Position{X: 1, Y: 1}) {
		t.Fatalf("camera target is %+v, want leader", board.CameraTarget())
	}
}

func TestBuildSnapshotContainsWorldState(t *testing.T) {
	board := NewMultiplayerBoard(32, 1, 1, 0, []int{0, 1})
	snapshot := board.BuildSnapshot(9, 3, true)

	if snapshot.Seq != 9 || snapshot.Round != 3 || !snapshot.Paused {
		t.Fatalf("snapshot header is %+v", snapshot)
	}
	if len(snapshot.Players) != 2 {
		t.Fatalf("snapshot has %d players, want 2", len(snapshot.Players))
	}
	if len(snapshot.Food) != foodTargetFor(32) {
		t.Fatalf("snapshot has %d food, want %d", len(snapshot.Food), foodTargetFor(32))
	}
	for _, player := range snapshot.Players {
		if len(player.Segments) != initialSnakeLength {
			t.Fatalf("player %d has %d segments, want %d", player.ID, len(player.Segments), initialSnakeLength)
		}
	}
}

func TestRoundContinuesWithThreePlayers(t *testing.T) {
	board := NewMultiplayerBoard(32, 0, 0, 0, []int{0, 1, 2})
	board.tickRate = 1
	board.food = nil

	board.Snake(0).body = []Position{{X: 5, Y: 5}, {X: 4, Y: 5}, {X: 3, Y: 5}}
	board.Snake(0).direction = Right
	board.Snake(0).next = Right
	board.Snake(0).queued = false

	board.Snake(1).body = []Position{{X: 6, Y: 5}, {X: 7, Y: 5}, {X: 8, Y: 5}}
	board.Snake(1).direction = Right
	board.Snake(1).next = Right
	board.Snake(1).queued = false

	board.Snake(2).body = []Position{{X: 20, Y: 20}, {X: 19, Y: 20}, {X: 18, Y: 20}}
	board.Snake(2).direction = Right
	board.Snake(2).next = Right
	board.Snake(2).queued = false

	board.Update()

	if board.IsGameOver() {
		t.Fatal("round ended while two players were still alive")
	}
	if board.Alive(0) {
		t.Fatal("player 0 should have died on player 1's body")
	}
	board.Eliminate(1)
	if !board.IsGameOver() || board.Winner() != 2 {
		t.Fatalf("round after disconnect: over=%v winner=%d, want player 2", board.IsGameOver(), board.Winner())
	}
}

func TestDroppedFoodExpires(t *testing.T) {
	board := NewSoloBoard(32, 0, 0, CollisionGameOver)
	board.food = []*Food{{pos: Position{X: 10, Y: 10}, ttl: 1, dropped: true}}
	board.expireFood()
	if len(board.food) != 0 {
		t.Fatalf("expired food remains: %+v", board.food)
	}
}

func TestLeaderboardSortsByScore(t *testing.T) {
	board := NewMultiplayerBoard(32, 0, 0, 0, []int{0, 1, 2})
	board.players[0].score = 3
	board.players[1].score = 9
	board.players[2].score = 5
	rows := board.Leaderboard(0)
	if len(rows) != 3 {
		t.Fatalf("leaderboard has %d rows, want 3", len(rows))
	}
	if rows[0].ID != 1 || rows[1].ID != 2 || rows[2].ID != 0 {
		t.Fatalf("leaderboard order is %+v", rows)
	}
}

func TestCameraClampsToWorld(t *testing.T) {
	camera := NewCamera(32*CellSize, screenWidth)
	camera.SnapTo(Position{X: 0, Y: 0})
	x, y := camera.Position()
	if x != 0 || y != 0 {
		t.Fatalf("camera at top-left is %v,%v", x, y)
	}
	camera.SnapTo(Position{X: 31, Y: 31})
	x, y = camera.Position()
	if x != 32*CellSize-screenWidth || y != 32*CellSize-screenWidth {
		t.Fatalf("camera at bottom-right is %v,%v", x, y)
	}
}

func TestCameraTargetFollowsLeaderWhenLocalIsDead(t *testing.T) {
	board := NewMultiplayerBoard(32, 0, 0, 0, []int{0, 1})
	board.players[0].score = 1
	board.players[1].score = 9
	board.killPlayer(0)

	if got, want := board.CameraTarget(), board.players[1].snake.Position(); got != want {
		t.Fatalf("camera target is %+v, want leader at %+v", got, want)
	}
}

func TestCameraTargetKeepsLastPositionWithoutLeader(t *testing.T) {
	board := NewSoloBoard(32, 0, 0, CollisionTailCut)
	board.killPlayer(0)
	board.players[0].last = Position{X: 0, Y: 0}

	if got := board.CameraTarget(); got != (Position{X: 0, Y: 0}) {
		t.Fatalf("camera target is %+v, want last known position", got)
	}
}

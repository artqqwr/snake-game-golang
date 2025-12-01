package game

import (
	"image/color"

	"github.com/hajimehoshi/ebiten/v2"
)

type Snake struct {
	direction Direction
	color     color.Color
	image     *ebiten.Image
	size      int
	speed     float64

	head *SnakeBody
	tail *SnakeBody
}

func NewSnake(pos Position) *Snake {
	snake := Snake{}

	snake.direction = Right
	snake.color = color.White
	snake.speed = 1

	snake.image = ebiten.NewImage(CellSize, CellSize)
	snake.head = NewSnakeBody(pos, nil, nil, snake.color)
	snake.tail = snake.head

	return &snake
}

func (s *Snake) Position() Position {
	return s.head.Position()
}

var directionMap = map[Direction]func() (int, int){
	Up:    func() (int, int) { return 0, -1 },
	Down:  func() (int, int) { return 0, 1 },
	Left:  func() (int, int) { return -1, 0 },
	Right: func() (int, int) { return 1, 0 },
}

var snakeMoveMap = map[ebiten.Key]func(snake *Snake){
	ebiten.KeyDown: func(snake *Snake) {
		if snake.direction == Up {
			return
		}
		snake.direction = Down
	},
	ebiten.KeyUp: func(snake *Snake) {
		if snake.direction == Down {
			return
		}
		snake.direction = Up
	},
	ebiten.KeyRight: func(snake *Snake) {
		if snake.direction == Left {
			return
		}
		snake.direction = Right
	},
	ebiten.KeyLeft: func(snake *Snake) {
		if snake.direction == Right {
			return
		}
		snake.direction = Left
	},
	ebiten.KeyI: func(snake *Snake) { snake.increase() },
}

func (s *Snake) IsCollidingWith(other Object) bool {
	return s.Position() == other.Position()
}

func (s *Snake) isCollidingWithBody() (*SnakeBody, bool) {
	headPos := s.head.Position()

	for bodyPart := s.head.next; bodyPart != nil; bodyPart = bodyPart.next {
		if headPos.X == bodyPart.pos.X && headPos.Y == bodyPart.pos.Y {
			return bodyPart, true
		}
	}
	return nil, false
}

func (s *Snake) Update() error {
	for bodyPart := s.tail; bodyPart != nil; bodyPart = bodyPart.prev {

		if err := bodyPart.Update(); err != nil {
			return err
		}
	}

	for key, snakeMoveFunc := range snakeMoveMap {
		if ebiten.IsKeyPressed(key) {
			snakeMoveFunc(s)
		}
	}

	x, y := directionMap[s.direction]()
	pos := Position{
		X: s.head.pos.X + int(float64(x)*s.speed),
		Y: s.head.pos.Y + int(float64(y)*s.speed),
	}
	s.head.pos = pos

	if bodyPart, ok := s.isCollidingWithBody(); ok {
		s.tail = bodyPart
		s.tail.next = nil
	}

	return nil
}

func (s *Snake) Draw(screen *ebiten.Image) {
	for bodyPart := s.head; bodyPart != nil; bodyPart = bodyPart.next {
		bodyPart.Draw(screen)
	}
}

func (s *Snake) increase() {
	newTail := NewSnakeBody(s.tail.pos, nil, s.tail, s.color)

	s.tail.next = newTail
	s.tail = newTail
	s.size++
}

type SnakeBody struct {
	pos  Position
	next *SnakeBody
	prev *SnakeBody

	image *ebiten.Image
}

func (s *SnakeBody) IsCollisionWith(other Object) bool {
	return s.pos == other.Position()
}

func NewSnakeBody(pos Position, next *SnakeBody, prev *SnakeBody, color color.Color) *SnakeBody {
	bodyPart := SnakeBody{
		pos:  pos,
		next: next,
		prev: prev,
	}

	bodyPart.image = ebiten.NewImage(CellSize, CellSize)
	bodyPart.image.Fill(color)

	return &bodyPart
}
func (s *SnakeBody) Update() error {
	if s.IsHead() {
		return nil
	}

	s.pos = s.prev.pos

	return nil
}

func (s *SnakeBody) Position() Position {
	return s.pos
}

func (s *SnakeBody) Draw(screen *ebiten.Image) {
	op := &ebiten.DrawImageOptions{}
	op.GeoM.Translate(float64(s.pos.X)*CellSize, float64(s.pos.Y)*CellSize)
	screen.DrawImage(s.image, op)
}

func (s *SnakeBody) IsTail() bool {
	return s.next == nil
}

func (s *SnakeBody) IsHead() bool {
	return s.prev == nil
}

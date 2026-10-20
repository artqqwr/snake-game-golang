package game

import (
	"image/color"

	"github.com/hajimehoshi/ebiten/v2"
)

var (
	snakePalette = []snakeColors{
		{head: color.RGBA{R: 80, G: 220, B: 110, A: 255}, body: color.RGBA{R: 35, G: 160, B: 75, A: 255}},
		{head: color.RGBA{R: 90, G: 150, B: 240, A: 255}, body: color.RGBA{R: 40, G: 90, B: 190, A: 255}},
		{head: color.RGBA{R: 245, G: 165, B: 60, A: 255}, body: color.RGBA{R: 190, G: 105, B: 30, A: 255}},
		{head: color.RGBA{R: 190, G: 115, B: 245, A: 255}, body: color.RGBA{R: 125, G: 60, B: 175, A: 255}},
		{head: color.RGBA{R: 80, G: 215, B: 215, A: 255}, body: color.RGBA{R: 30, G: 145, B: 145, A: 255}},
		{head: color.RGBA{R: 245, G: 225, B: 85, A: 255}, body: color.RGBA{R: 185, G: 160, B: 30, A: 255}},
		{head: color.RGBA{R: 245, G: 120, B: 185, A: 255}, body: color.RGBA{R: 175, G: 60, B: 125, A: 255}},
		{head: color.RGBA{R: 240, G: 95, B: 95, A: 255}, body: color.RGBA{R: 170, G: 45, B: 45, A: 255}},
	}
	localMarkerColor = color.RGBA{R: 255, G: 255, B: 255, A: 230}
)

type snakeColors struct {
	head color.RGBA
	body color.RGBA
}

const initialSnakeLength = 3

var directionDelta = [4]Position{
	Up:    {Y: -1},
	Down:  {Y: 1},
	Left:  {X: -1},
	Right: {X: 1},
}

// Snake is a body of cells plus the current and requested direction.
type Snake struct {
	body      []Position
	direction Direction
	next      Direction
	queued    bool
}

func NewSnake(pos Position) *Snake {
	body := make([]Position, initialSnakeLength)
	for index := range body {
		body[index] = Position{X: pos.X - index, Y: pos.Y}
	}
	return &Snake{body: body, direction: Right, next: Right}
}

func (s *Snake) Position() Position {
	return s.body[0]
}

// Body exposes the segments for rendering and snapshot serialisation.
func (s *Snake) Body() []Position {
	return s.body
}

func (s *Snake) Direction() Direction {
	return s.direction
}

func (s *Snake) NextDirection() Direction {
	return s.next
}

// SetDirection queues at most one turn per simulation tick. Opposite turns and
// turns received after a turn was already queued are ignored.
func (s *Snake) SetDirection(direction Direction) {
	if !direction.Valid() || s.queued {
		return
	}
	if s.direction.IsOpposite(direction) {
		return
	}
	s.next = direction
	s.queued = true
}

// Steer updates the requested direction without latching it.
func (s *Snake) Steer(direction Direction) {
	if !direction.Valid() || s.direction.IsOpposite(direction) {
		return
	}
	s.next = direction
}

// SetState replaces the snake state with an authoritative snapshot.
func (s *Snake) SetState(body []Position, direction Direction) {
	if len(body) == 0 {
		return
	}
	s.body = body
	if direction.Valid() {
		s.direction = direction
		s.next = direction
	}
	s.queued = false
}

func (s *Snake) Move() {
	if s.next.Valid() {
		s.direction = s.next
	}
	s.queued = false
	delta := directionDelta[s.direction]
	head := Position{X: s.body[0].X + delta.X, Y: s.body[0].Y + delta.Y}
	copy(s.body[1:], s.body[:len(s.body)-1])
	s.body[0] = head
}

// NextPosition returns the cell the snake would enter on the next tick.
func (s *Snake) NextPosition() Position {
	direction := s.next
	if !direction.Valid() {
		direction = s.direction
	}
	delta := directionDelta[direction]
	return Position{X: s.body[0].X + delta.X, Y: s.body[0].Y + delta.Y}
}

// TurnInPlace applies a queued turn without moving the body. It is used when a
// soft world border blocks the next step.
func (s *Snake) TurnInPlace() {
	if s.next.Valid() {
		s.direction = s.next
	}
	s.queued = false
}

func (s *Snake) Grow() {
	if len(s.body) == 0 {
		return
	}
	s.body = append(s.body, s.body[len(s.body)-1])
}

func (s *Snake) Contains(pos Position) bool {
	for _, segment := range s.body {
		if segment == pos {
			return true
		}
	}
	return false
}

func (s *Snake) BodyCollision() (int, bool) {
	head := s.body[0]
	for index := 1; index < len(s.body); index++ {
		if s.body[index] == head {
			return index, true
		}
	}
	return 0, false
}

func (s *Snake) CutTail(index int) {
	if index > 0 && index < len(s.body) {
		s.body = s.body[:index]
	}
}

func (s *Snake) DrawAt(screen *ebiten.Image, offsetX, offsetY int) {
	s.DrawAtColor(screen, offsetX, offsetY, snakePalette[0].head, snakePalette[0].body)
}

func (s *Snake) DrawAtColor(screen *ebiten.Image, offsetX, offsetY int, headColor, bodyColor color.Color) {
	for index, pos := range s.body {
		current := bodyColor
		if index == 0 {
			current = headColor
		}
		fillRect(screen, float64(offsetX+pos.X*CellSize), float64(offsetY+pos.Y*CellSize), CellSize-1, CellSize-1, current)
	}
}

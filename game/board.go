package game

import (
	"math/rand"

	"github.com/hajimehoshi/ebiten/v2"
)

const CellSize = 20

type Object interface {
	Position() Position
	Update() error
	Draw(screen *ebiten.Image)
}

type Direction int

const (
	Up Direction = iota
	Down
	Left
	Right
)

type Position struct {
	X, Y int
}

func NewRandomPosition(max int) Position {
	pos := Position{rand.Intn(max), rand.Intn(max)}

	return pos
}

type Board struct {
	snakes []*Snake
	apples []*Apple

	size      int
	tick      int
	tickSpeed int
}

func NewBoard(size int) *Board {
	b := Board{
		snakes:    make([]*Snake, 0, 2),
		apples:    make([]*Apple, 0, 5),
		size:      size,
		tickSpeed: 5,
	}

	//pos := NewRandomPosition(size)
	pos := Position{30, 10}
	b.apples = append(b.apples, NewApple(pos))
	b.snakes = append(b.snakes, NewSnake(Position{0, 10}))

	return &b
}

func (b *Board) Update() error {
	b.tick++

	if b.tick < b.tickSpeed {
		return nil
	}

	b.tick = 0

	for _, snake := range b.snakes {
		for _, apple := range b.apples {
			if snake.IsCollidingWith(apple) {
				snake.increase()
				apple.pos = NewRandomPosition(b.size)
			}
		}

		if err := snake.Update(); err != nil {
			return err
		}

		newX := snake.Position().X
		newY := snake.Position().Y
		if newX < 0 {
			newX = b.size - 1
		} else if newX >= b.size {
			newX = 0
		}

		if newY < 0 {
			newY = b.size - 1
		} else if newY >= b.size {
			newY = 0
		}

		snake.head.pos = Position{newX, newY}

	}

	return nil
}

func (b *Board) Draw(screen *ebiten.Image) {
	for _, snake := range b.snakes {
		snake.Draw(screen)
	}

	for _, apple := range b.apples {
		apple.Draw(screen)
	}
}

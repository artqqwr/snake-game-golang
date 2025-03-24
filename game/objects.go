package game

import (
	"errors"
	"image"
	"image/color"
	"math/rand"

	"github.com/hajimehoshi/ebiten/v2"
	"golang.org/x/image/colornames"
)

type Object interface {
	GetImage() *ebiten.Image
	GetPosition() Position
}

type defaultObject struct {
	image *ebiten.Image
	pos   Position
}

func (obj defaultObject) GetPosition() Position {
	return obj.pos
}

func (obj defaultObject) GetImage() *ebiten.Image {
	return obj.image
}

func (obj defaultObject) Update() error {
	return errors.New("not implemented")
}

func (obj defaultObject) Draw(screen *ebiten.Image) {
	geoM := ebiten.GeoM{}
	geoM.Translate(obj.GetPosition().X, obj.GetPosition().Y)

	screen.DrawImage(obj.GetImage(), &ebiten.DrawImageOptions{
		GeoM: geoM,
	})
}

func (obj *defaultObject) IsCollidedWith(object Object) bool {
	rect1 := obj.GetImage().Bounds()
	rect2 := object.GetImage().Bounds()

	// Получаем координаты прямоугольников
	rect1 = rect1.Add(image.Point{int(obj.GetPosition().X), int(obj.GetPosition().Y)})
	rect2 = rect2.Add(image.Point{int(object.GetPosition().X), int(object.GetPosition().Y)})

	// Проверка на пересечение
	return !rect1.Intersect(rect2).Empty()

	// return obj.GetPosition().X-object.GetPosition().X < float64(object.GetImage().Bounds().Dx()) &&
	// obj.GetPosition().Y-object.GetPosition().Y < float64(object.GetImage().Bounds().Dy()) &&
	// obj.GetPosition().X+object.GetPosition().X > float64(object.GetImage().Bounds().Dx()) &&
	// obj.GetPosition().Y+object.GetPosition().Y > float64(object.GetImage().Bounds().Dy())

	// selfBounds := obj.GetImage().Bounds()
	// bodyPartBounds := object.GetImage().Bounds()

	// selfXMax := selfBounds.Max.X
	// selfXMin := selfBounds.Min.X
	// selfYMax := selfBounds.Max.Y
	// selfYMin := selfBounds.Min.Y

	// bodyPartXMax := bodyPartBounds.Max.X
	// bodyPartXMin := selfBounds.Min.X
	// bodyPartYMax := bodyPartBounds.Max.Y
	// bodyPartYMin := bodyPartBounds.Min.Y

	// return (bodyPartXMin <= selfXMin && bodyPartXMin <= selfXMax) &&
	// 	(bodyPartXMax >= selfXMin && bodyPartXMax >= selfXMax) &&
	// 	(bodyPartYMin <= selfYMin && bodyPartYMin <= selfYMax) &&
	// 	(bodyPartYMax >= selfYMin && bodyPartYMax >= selfYMax) && obj.pos == object.GetPosition()

}

type Position struct {
	X, Y float64
}

type Direction Position

var (
	UP    = Direction{0, 1}
	DOWN  = Direction{0, -1}
	RIGHT = Direction{1, 0}
	LEFT  = Direction{-1, 0}
)

type SnakeBodyPart struct {
	defaultObject
	Left *SnakeBodyPart
	Next *SnakeBodyPart
}

type SnakeBody struct {
	Size int

	Color     color.Color
	Direction Direction
	Head      *SnakeBodyPart
	Tail      *SnakeBodyPart
}

func (b *SnakeBody) Increase() {
	bp := SnakeBodyPart{}

	bp.image = ebiten.NewImage(ObjectSize, ObjectSize)
	bp.image.Fill(b.Color)

	if b.Head != nil {
		bp.pos = b.Head.pos

		bp.Next = b.Head
		b.Head.Left = &bp
	} else {
		bp.pos = Position{100, 100}
	}

	b.Head = &bp

	b.Size++
}

func (b *SnakeBody) Update() error {

	keys := map[ebiten.Key]func(){
		ebiten.KeyUp: func() {
			if b.Direction == RIGHT || b.Direction == LEFT {
				b.Direction = UP
				// b.Direction
			} else if b.Direction != DOWN {
				b.Direction = UP
			}
		},
		ebiten.KeyDown: func() {
			if b.Direction == RIGHT || b.Direction == LEFT {
				b.Direction = DOWN
				// b.Direction.Y = DOWN.Y
			} else if b.Direction != UP {
				b.Direction = DOWN
			}
		},
		ebiten.KeyRight: func() {
			if b.Direction != LEFT {
				b.Direction = RIGHT
			}
		},
		ebiten.KeyLeft: func() {
			if b.Direction != RIGHT {
				b.Direction = LEFT
			}
		},

		ebiten.KeyI: func() { b.Increase() },
	}

	for key, f := range keys {
		if ebiten.IsKeyPressed(key) {
			f()
		}
	}

	b.Head.pos.X += b.Direction.X
	b.Head.pos.Y -= b.Direction.Y

	for bp := b.Tail; bp != nil && bp.Left != nil; bp = bp.Left {
		if b.Tail == b.Head {
			break
		}

		if bp != b.Head {
			if bp.pos == b.Head.pos {
				bp.Left.Next = nil
				break
			}
		}

		bp.pos = bp.Left.pos
	}

	return nil
}

func (b *SnakeBody) Draw(screen *ebiten.Image) {
	for bp := b.Head; bp != nil; bp = bp.Next {
		bp.Draw(screen)
	}
}

type Snake struct {
	SnakeBody
}

func NewSnake() *Snake {
	s := &Snake{}

	s.Color = colornames.Green

	s.Increase()

	s.Tail = s.Head

	return s
}

type Apple struct {
	defaultObject
}

func NewApple(pos Position) *Apple {
	a := &Apple{
		defaultObject: defaultObject{
			image: ebiten.NewImage(ObjectSize, ObjectSize),
			pos:   pos,
		},
	}
	a.image.Fill(colornames.Red)

	return a
}

type Board struct {
	snakes []*Snake
	apples []*Apple
	size   int
}

func NewBoard(size uint) *Board {
	b := &Board{
		snakes: []*Snake{},
		apples: []*Apple{},
		size:   int(size),
	}

	b.snakes = append(b.snakes, NewSnake())
	b.apples = append(b.apples, NewApple(Position{float64(rand.Intn(b.size)), float64(rand.Intn(b.size))}))

	return b
}

func (b *Board) Update() error {
	for _, s := range b.snakes {
		s.Update()

		for _, a := range b.apples {
			if s.Head.IsCollidedWith(a) {
				b.apples = b.apples[1:]
				b.apples = append(b.apples, NewApple(Position{float64(rand.Intn(b.size)), float64(rand.Intn(b.size))}))
				s.Increase()
				s.Increase()
				s.Increase()

				break
			}
		}
	}

	return nil
}

func (b *Board) Draw(screen *ebiten.Image) {
	for _, s := range b.snakes {
		s.Draw(screen)
	}

	for _, a := range b.apples {
		a.Draw(screen)
	}
}

package game

import (
	"image/color"

	"github.com/hajimehoshi/ebiten/v2"
)

type Apple struct {
	pos Position

	image *ebiten.Image
}

func (a *Apple) Position() Position {
	return a.pos
}

func (a *Apple) Update() error {
	return nil
}

func (a *Apple) Draw(screen *ebiten.Image) {
	op := &ebiten.DrawImageOptions{}
	op.GeoM.Translate(float64(a.pos.X)*CellSize, float64(a.pos.Y)*CellSize)

	screen.DrawImage(a.image, op)
}

func (a *Apple) IsCollisionWith(other Object) bool {
	return a.pos == other.Position()
}

func NewApple(pos Position) *Apple {
	apple := Apple{pos: pos}
	apple.image = ebiten.NewImage(CellSize, CellSize)
	apple.image.Fill(color.RGBA{R: 255})

	return &apple
}

package game

import (
	"image/color"

	"github.com/hajimehoshi/ebiten/v2"
)

var foodColor = color.RGBA{R: 235, G: 75, B: 75, A: 255}

// Food is a pellet on the board. Dropped pellets expire after a while, while
// baseline food is permanent and is replenished by the board.
type Food struct {
	pos     Position
	ttl     int
	dropped bool
}

func NewFood(pos Position) *Food {
	return &Food{pos: pos}
}

func (f *Food) Position() Position {
	return f.pos
}

func (f *Food) DrawAt(screen *ebiten.Image, screenX, screenY int) {
	fillRect(screen, float64(screenX+7), float64(screenY+7), 6, 6, foodColor)
}

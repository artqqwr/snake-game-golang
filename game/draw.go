package game

import (
	"image/color"

	"github.com/hajimehoshi/ebiten/v2"
	"github.com/hajimehoshi/ebiten/v2/vector"
)

// fillRect draws a crisp rectangle without anti-aliasing.
func fillRect(dst *ebiten.Image, x, y, width, height float64, clr color.Color) {
	vector.FillRect(dst, float32(x), float32(y), float32(width), float32(height), clr, false)
}

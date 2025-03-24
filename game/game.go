package game

import (
	"log"

	"github.com/hajimehoshi/ebiten/v2"
)

type Window struct {
	Width, Height int
	Title         string
}

type Game struct {
	window Window
	board  *Board
}

func New() *Game {
	g := Game{
		window: Window{
			Width:  720,
			Height: 480,
			Title:  "Snake Game",
		},
	}

	g.board = NewBoard(100)
	return &g
}

func (g *Game) Update() error {
	if err := g.board.Update(); err != nil {
		return err
	}

	return nil
}

func (g *Game) Draw(screen *ebiten.Image) {
	g.board.Draw(screen)
}

func (g *Game) Layout(outsideWidth, outsideHeight int) (screenWidth, screenHeight int) {
	return 320, 240
}

func (g *Game) Run() {
	ebiten.SetWindowSize(g.window.Width, g.window.Height)
	ebiten.SetWindowTitle(g.window.Title)
	ebiten.SetWindowResizingMode(ebiten.WindowResizingModeEnabled)

	if err := ebiten.RunGame(g); err != nil {
		log.Fatal(err)
	}
}

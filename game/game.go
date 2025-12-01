package game

import "github.com/hajimehoshi/ebiten/v2"

type Game struct {
	board *Board
}

func (g *Game) Update() error {

	return g.board.Update()
}

func (g *Game) Draw(screen *ebiten.Image) {
	g.board.Draw(screen)
}

func (g *Game) Layout(outsideWidth, outsideHeight int) (screenWidth, screenHeight int) {
	return 640, 640
}

func New() *Game {
	g := Game{
		board: NewBoard(640 / CellSize),
	}

	return &g
}

func (g *Game) Run() error {
	ebiten.SetWindowSize(640, 640)
	return ebiten.RunGame(g)
}

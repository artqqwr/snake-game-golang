package main

import "github.com/artqqwr/snake-game-golang/game"

func main() {
	if err := game.New().Run(); err != nil {
		panic(err)
	}
}

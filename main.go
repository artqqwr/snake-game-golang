package main

import (
	"flag"
	"log"

	"github.com/artqqwr/snake-game-golang/game"
	"github.com/artqqwr/snake-game-golang/network"
)

func main() {
	host := flag.Bool("host", false, "start a LAN room immediately")
	join := flag.String("join", "", "join a LAN room at ip:port")
	name := flag.String("name", "Snake LAN Room", "room name shown to other players")
	maxPlayers := flag.Int("max", 8, "maximum players in a hosted room (2-8)")
	flag.Parse()

	if *maxPlayers < 2 || *maxPlayers > network.MaxPlayers {
		log.Fatalf("-max must be between 2 and %d", network.MaxPlayers)
	}

	app := game.NewWithOptions(game.Options{
		Host:       *host,
		Join:       *join,
		RoomName:   *name,
		MaxPlayers: *maxPlayers,
	})
	if err := app.Run(); err != nil {
		log.Fatal(err)
	}
}

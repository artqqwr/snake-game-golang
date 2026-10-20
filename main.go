package main

import (
	"context"
	"flag"
	"log"
	"os"
	"os/signal"
	"syscall"

	"github.com/artqqwr/snake-game-golang/game"
	"github.com/artqqwr/snake-game-golang/network"
)

func main() {
	host := flag.Bool("host", false, "start a LAN room immediately")
	join := flag.String("join", "", "join a LAN room at ip:port")
	server := flag.Bool("server", false, "run a headless dedicated server (no window or local player)")
	bots := flag.Int("bots", 0, "number of smart bots to add (implies -host when no mode is selected)")
	name := flag.String("name", "Snake LAN Room", "room name shown to other players")
	maxPlayers := flag.Int("max", 8, "maximum players in a hosted room (2-8; dedicated: guests)")
	flag.Parse()

	if *maxPlayers < 2 || *maxPlayers > network.MaxPlayers {
		log.Fatalf("-max must be between 2 and %d", network.MaxPlayers)
	}
	if *bots < 0 || *bots > network.MaxPlayers {
		log.Fatalf("-bots must be between 0 and %d", network.MaxPlayers)
	}
	if *server && *join != "" {
		log.Fatal("-server cannot be combined with -join")
	}
	if *server && *host {
		log.Fatal("-server cannot be combined with -host")
	}
	if *host && *join != "" {
		log.Fatal("-host cannot be combined with -join")
	}
	if *bots > 0 && !*server && !*host && *join == "" {
		// `snake --bots 5` is a convenient shorthand for hosting with bots.
		*host = true
	}
	if *bots > 0 && !*server && !*host {
		log.Fatal("-bots requires -server or -host (or omit -join to host automatically)")
	}
	if *server && *bots > *maxPlayers {
		log.Fatalf("-bots cannot exceed -max (%d)", *maxPlayers)
	}
	if *host && *bots > *maxPlayers-1 {
		log.Fatalf("with -host, -bots cannot exceed -max-1 (%d)", *maxPlayers-1)
	}

	if *server {
		ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
		defer stop()
		err := game.RunServer(ctx, game.ServerOptions{
			Name:       *name,
			MaxPlayers: *maxPlayers,
			Bots:       *bots,
		})
		if err != nil {
			log.Fatal(err)
		}
		return
	}

	app := game.NewWithOptions(game.Options{
		Host:       *host,
		Join:       *join,
		RoomName:   *name,
		MaxPlayers: *maxPlayers,
		Bots:       *bots,
	})
	if err := app.Run(); err != nil {
		log.Fatal(err)
	}
}

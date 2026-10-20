package game

import (
	"fmt"
	"image/color"
	"net"
	"strconv"

	"github.com/artqqwr/snake-game-golang/network"
	"github.com/hajimehoshi/ebiten/v2"
	"github.com/hajimehoshi/ebiten/v2/ebitenutil"
)

const screenWidth = 640

var (
	backgroundColor  = color.RGBA{R: 18, G: 24, B: 32, A: 255}
	obstacleColor    = color.RGBA{R: 90, G: 105, B: 120, A: 255}
	panelColor       = color.RGBA{R: 30, G: 40, B: 52, A: 255}
	selectedColor    = color.RGBA{R: 45, G: 115, B: 80, A: 255}
	worldBorderColor = color.RGBA{R: 80, G: 190, B: 140, A: 190}
)

// Draw renders the current screen.
func (g *Game) Draw(screen *ebiten.Image) {
	switch g.screen {
	case screenMenu:
		g.drawMenu(screen)
	case screenRooms:
		g.drawRooms(screen)
	case screenLobby:
		g.drawLobby(screen)
	case screenPlaying, screenPaused, screenGameOver:
		if g.board != nil {
			g.board.Draw(screen, g.camera)
			g.drawHUD(screen)
		} else {
			screen.Fill(backgroundColor)
		}
		switch {
		case g.screen == screenGameOver:
			g.drawResults(screen)
		case g.screen == screenPaused:
			g.drawOverlay(screen, "PAUSED", "P / ESC - RESUME")
		case g.session != nil && g.session.paused:
			g.drawPauseOverlay(screen)
		case g.session != nil && g.board != nil && !g.board.LocalAlive():
			g.drawSpectatorOverlay(screen)
		}
	}
}

// Layout keeps the board square and reserves space for the HUD.
func (g *Game) Layout(outsideWidth, outsideHeight int) (int, int) {
	return screenWidth, screenWidth + hudHeight
}

// Run starts the Ebitengine window.
func (g *Game) Run() error {
	ebiten.SetWindowSize(screenWidth, screenWidth+hudHeight)
	ebiten.SetWindowTitle("Snake")
	return ebiten.RunGame(g)
}

func (g *Game) drawMenu(screen *ebiten.Image) {
	screen.Fill(backgroundColor)
	fillRect(screen, 72, 52, 496, 560, panelColor)
	drawCentered(screen, "S N A K E", 82)
	drawCentered(screen, "MAIN MENU", 112)
	drawCentered(screen, "UP/DOWN SELECT   LEFT/RIGHT CHANGE   ENTER START", 145)

	items := []string{
		fmt.Sprintf("1  MAP          %s", mapDisplayName(g.mapIndex)),
		fmt.Sprintf("2  DIFFICULTY   %s", difficultyName(g.difficultyIndex)),
		fmt.Sprintf("3  SELF HIT     %s", collisionModeDisplayName(g.collisionMode)),
		"4  START GAME",
		fmt.Sprintf("5  MULTIPLAYER  %s", multiplayerModeName(g.multiplayerMode)),
	}
	for index, item := range items {
		y := 195 + index*62
		if index == g.menuIndex {
			fillRect(screen, 112, float64(y-14), 416, 42, selectedColor)
			item = "> " + item + " <"
		}
		ebitenutil.DebugPrintAt(screen, item, 135, y)
	}
	drawCentered(screen, "KEYS 1-5 SELECT MENU ITEM", 475)
	drawCentered(screen, fmt.Sprintf("HIGH SCORE: %d", g.highScore), 520)
	drawCentered(screen, "5 + LEFT/RIGHT: OFF / HOST / JOIN", 545)
	if g.networkStatus != "" {
		drawCentered(screen, "NET: "+g.networkStatus, 575)
	}
	if g.networkDetail != "" {
		drawCentered(screen, g.networkDetail, 595)
	}
}

func (g *Game) drawRooms(screen *ebiten.Image) {
	screen.Fill(backgroundColor)
	fillRect(screen, 60, 52, 520, 560, panelColor)
	drawCentered(screen, "LAN ROOMS", 82)
	if g.scanning {
		drawCentered(screen, "SCANNING LOCAL NETWORK...", 112)
	} else if len(g.rooms) == 0 {
		drawCentered(screen, "NO ROOMS FOUND", 112)
	} else {
		drawCentered(screen, fmt.Sprintf("FOUND %d ROOM(S)", len(g.rooms)), 112)
	}

	if g.manualMode {
		drawCentered(screen, "ENTER HOST ADDRESS (IP:PORT)", 175)
		fillRect(screen, 100, 205, 440, 38, selectedColor)
		drawCentered(screen, g.manualAddress+"_", 218)
		drawCentered(screen, "ENTER - CONNECT     ESC - CANCEL", 280)
		if g.networkDetail != "" {
			drawCentered(screen, g.networkDetail, 320)
		}
		return
	}

	start := 0
	if g.roomIndex >= maxRoomListItems {
		start = g.roomIndex - maxRoomListItems + 1
	}
	for index := start; index < len(g.rooms) && index < start+maxRoomListItems; index++ {
		room := g.rooms[index]
		address := net.JoinHostPort(room.Address, strconv.Itoa(room.TCPPort))
		label := fmt.Sprintf("%s  %s  %d/%d  %s", room.Name, address, room.Players, room.MaxPlayers, mapDisplayName(room.MapIndex))
		if room.InRound {
			label += "  IN ROUND"
		}
		y := 165 + (index-start)*48
		if index == g.roomIndex {
			fillRect(screen, 80, float64(y-12), 480, 36, selectedColor)
			label = "> " + label
		}
		ebitenutil.DebugPrintAt(screen, label, 94, y)
	}

	drawCentered(screen, "ENTER - JOIN     R - RESCAN", 500)
	drawCentered(screen, "I - MANUAL IP     ESC - BACK", 530)
	if g.networkDetail != "" {
		drawCentered(screen, g.networkDetail, 575)
	}
}

func (g *Game) drawLobby(screen *ebiten.Image) {
	screen.Fill(backgroundColor)
	fillRect(screen, 72, 52, 496, 560, panelColor)
	if g.session == nil {
		drawCentered(screen, "JOINING ROOM", 90)
		drawCentered(screen, "CONNECTING...", 160)
		if g.networkDetail != "" {
			drawCentered(screen, g.networkDetail, 190)
		}
		drawCentered(screen, "ESC - CANCEL", 260)
		return
	}

	s := g.session
	lobby := s.lobby
	maxPlayers := lobby.MaxPlayers
	if maxPlayers <= 0 {
		maxPlayers = g.maxPlayers
	}
	players := len(lobby.Players)
	ready := 0
	for _, player := range lobby.Players {
		if player.Ready {
			ready++
		}
	}

	if s.isHost() {
		drawCentered(screen, "HOSTING ROOM", 80)
		drawCentered(screen, s.status, 120)
		drawCentered(screen, s.detail, 148)
		drawCentered(screen, fmt.Sprintf("PLAYERS %d/%d   READY %d/%d", players, maxPlayers, ready, players), 190)
		g.drawLobbyRoster(screen, lobby, 225)
		drawCentered(screen, "SHARE THE ADDRESS ABOVE WITH YOUR FRIENDS", 500)
		if players > 1 && ready == players {
			fillRect(screen, 150, 530, 340, 36, selectedColor)
			drawCentered(screen, "ENTER - START ROUND", 542)
		} else {
			drawCentered(screen, "WAITING FOR PLAYERS TO JOIN AND READY UP", 542)
		}
		drawCentered(screen, "ESC - CLOSE ROOM", 585)
		return
	}

	drawCentered(screen, "JOINED ROOM", 80)
	drawCentered(screen, s.status, 120)
	drawCentered(screen, s.detail, 148)
	drawCentered(screen, fmt.Sprintf("PLAYERS %d/%d   READY %d/%d", players, maxPlayers, ready, players), 190)
	g.drawLobbyRoster(screen, lobby, 225)
	if s.waiting || lobby.InRound {
		drawCentered(screen, "A ROUND IS IN PROGRESS - YOU WILL JOIN THE NEXT ONE", 500)
	} else {
		drawCentered(screen, "WAITING FOR HOST TO START THE ROUND", 500)
	}
	drawCentered(screen, "ESC - LEAVE ROOM", 560)
}

func (g *Game) drawLobbyRoster(screen *ebiten.Image, lobby network.LobbyInfo, startY int) {
	if len(lobby.Players) == 0 {
		return
	}
	for index, player := range lobby.Players {
		if index >= 8 {
			break
		}
		status := "NOT READY"
		if player.Ready {
			status = "READY"
		}
		label := fmt.Sprintf("P%d", player.ID+1)
		if player.ID == 0 {
			label += "  HOST"
		}
		if g.session != nil && player.ID == g.session.playerID {
			label += "  (YOU)"
		}
		y := startY + index*28
		if g.session != nil && player.ID == g.session.playerID {
			fillRect(screen, 110, float64(y-6), 420, 24, selectedColor)
		}
		ebitenutil.DebugPrintAt(screen, fmt.Sprintf("%-20s %s", label, status), 130, y)
	}
}

func (g *Game) drawHUD(screen *ebiten.Image) {
	fillRect(screen, 0, 0, screenWidth, hudHeight, panelColor)
	if g.board != nil {
		if g.session != nil {
			line := fmt.Sprintf("YOU P%d %03d   ALIVE %d/%d   MAP %s",
				g.board.LocalID()+1, g.board.LocalScore(),
				g.board.AliveCount(), g.board.PlayerCount(), g.board.MapName())
			ebitenutil.DebugPrintAt(screen, line, 10, 7)

			leaders := g.board.Leaderboard(3)
			parts := ""
			for _, row := range leaders {
				if parts != "" {
					parts += "  "
				}
				parts += fmt.Sprintf("P%d:%03d", row.ID+1, row.Score)
			}
			ebitenutil.DebugPrintAt(screen, parts, 360, 7)
		} else {
			ebitenutil.DebugPrintAt(screen,
				fmt.Sprintf("SCORE %03d   BEST %03d   MAP %s", g.board.LocalScore(), g.highScore, g.board.MapName()), 12, 7)
		}
	}

	hint := "MOVE: ARROWS/WASD   P: PAUSE"
	if g.session != nil {
		role := "HOST"
		hint = "MOVE: WASD   P: PAUSE   ESC: CLOSE ROOM"
		if g.session.isGuest() {
			role = "GUEST"
			hint = "MOVE: WASD   HOST CONTROLS PAUSE   ESC: LEAVE"
		}
		ebitenutil.DebugPrintAt(screen, "NET "+role, 545, 27)
	} else {
		ebitenutil.DebugPrintAt(screen, "SOFT WORLD BORDER - DO NOT HIT SNAKES OR WALLS", 250, 27)
	}
	ebitenutil.DebugPrintAt(screen, hint, 10, 27)
}

func (g *Game) drawSpectatorOverlay(screen *ebiten.Image) {
	fillRect(screen, 170, hudHeight+10, 300, 30, panelColor)
	drawCentered(screen, "ELIMINATED - SPECTATING", hudHeight+20)
}

func (g *Game) drawResults(screen *ebiten.Image) {
	title := "GAME OVER"
	hint := "ENTER - RETRY   ESC - MENU"
	rows := []PlayerScore(nil)

	if g.session != nil {
		title = "ROUND OVER"
		hint = "ESC - LEAVE ROOM"
		if g.session.isHost() {
			if g.session.room.HasGuest() && g.session.room.AllReady() {
				hint = "ENTER - REMATCH   ESC - CLOSE ROOM"
			} else if g.session.room.HasGuest() {
				hint = "WAITING FOR PLAYERS TO READY   ESC - CLOSE ROOM"
			} else {
				hint = "WAITING FOR PLAYERS   ESC - CLOSE ROOM"
			}
		} else {
			hint = "WAITING FOR HOST REMATCH   ESC - LEAVE ROOM"
		}
		if g.board != nil {
			switch {
			case g.board.Winner() < 0:
				title = "DRAW"
			case g.board.Winner() == g.board.LocalID():
				title = "YOU WIN"
			default:
				title = "ROUND OVER"
			}
			rows = g.board.Leaderboard(8)
		}
	} else if g.board != nil {
		title = "GAME OVER"
	}

	fillRect(screen, 90, 170, 460, 360, panelColor)
	drawCentered(screen, title, 195)
	if g.session != nil && g.board != nil {
		if g.board.Winner() >= 0 {
			drawCentered(screen, fmt.Sprintf("WINNER: P%d", g.board.Winner()+1), 225)
		} else {
			drawCentered(screen, "NO SURVIVORS", 225)
		}
	}

	if len(rows) > 0 {
		for index, row := range rows {
			if index >= 8 {
				break
			}
			label := fmt.Sprintf("P%d", row.ID+1)
			if row.ID == g.board.LocalID() {
				label += " (YOU)"
			}
			if !row.Alive {
				label += "  OUT"
			}
			line := fmt.Sprintf("%-18s %03d", label, row.Score)
			y := 265 + index*27
			if row.ID == g.board.LocalID() {
				fillRect(screen, 125, float64(y-5), 390, 23, selectedColor)
			}
			ebitenutil.DebugPrintAt(screen, line, 150, y)
		}
	} else if g.board != nil {
		drawCentered(screen, fmt.Sprintf("SCORE %d", g.board.LocalScore()), 285)
	}
	drawCentered(screen, hint, 505)
}

func (g *Game) drawPauseOverlay(screen *ebiten.Image) {
	hint := "P - RESUME   ESC - CLOSE ROOM"
	if g.session != nil && g.session.isGuest() {
		hint = "WAITING FOR HOST TO RESUME"
	}
	g.drawOverlay(screen, "PAUSED", hint)
}

func (g *Game) drawOverlay(screen *ebiten.Image, title, hint string) {
	fillRect(screen, 120, 250, 400, 120, panelColor)
	drawCentered(screen, title, 280)
	drawCentered(screen, hint, 320)
}

func drawCentered(screen *ebiten.Image, text string, y int) {
	const charWidth = 6
	x := (screenWidth - len(text)*charWidth) / 2
	if x < 8 {
		x = 8
	}
	ebitenutil.DebugPrintAt(screen, text, x, y)
}

func mapDisplayName(index int) string {
	names := []string{"OPEN", "WELL", "CORRIDORS", "CROSS", "MAZE"}
	if index < 0 || index >= len(names) {
		return "UNKNOWN"
	}
	return names[index]
}

func difficultyName(index int) string {
	names := []string{"EASY", "NORMAL", "HARD"}
	if index < 0 || index >= len(names) {
		return "EASY"
	}
	return names[index]
}

func collisionModeDisplayName(mode CollisionMode) string {
	if mode == CollisionTailCut {
		return "CUT TAIL"
	}
	return "GAME OVER"
}

func multiplayerModeName(mode int) string {
	switch mode {
	case multiplayerHost:
		return "HOST"
	case multiplayerJoin:
		return "JOIN"
	default:
		return "OFF"
	}
}

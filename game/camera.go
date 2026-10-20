package game

// Camera follows a board cell and clamps itself to the world bounds.
type Camera struct {
	x, y           float64
	worldWidth     int
	worldHeight    int
	viewportWidth  int
	viewportHeight int
}

// NewCamera creates a camera for a square world rendered into a square
// viewport. Dimensions are in pixels.
func NewCamera(worldSize, viewportSize int) *Camera {
	return &Camera{
		worldWidth:     worldSize,
		worldHeight:    worldSize,
		viewportWidth:  viewportSize,
		viewportHeight: viewportSize,
	}
}

// SnapTo centres the camera on a cell immediately.
func (c *Camera) SnapTo(target Position) {
	if c == nil {
		return
	}
	c.x, c.y = c.target(target)
	c.clamp()
}

// Follow eases the camera towards a cell. It is intended to be called once per
// Ebitengine update.
func (c *Camera) Follow(target Position) {
	if c == nil {
		return
	}
	targetX, targetY := c.target(target)
	const smoothing = 0.14
	c.x += (targetX - c.x) * smoothing
	c.y += (targetY - c.y) * smoothing
	c.clamp()
}

func (c *Camera) target(target Position) (float64, float64) {
	x := float64(target.X*CellSize+CellSize/2) - float64(c.viewportWidth)/2
	y := float64(target.Y*CellSize+CellSize/2) - float64(c.viewportHeight)/2
	return x, y
}

func (c *Camera) clamp() {
	maxX := float64(c.worldWidth - c.viewportWidth)
	if maxX <= 0 {
		c.x = maxX / 2
	} else if c.x < 0 {
		c.x = 0
	} else if c.x > maxX {
		c.x = maxX
	}
	maxY := float64(c.worldHeight - c.viewportHeight)
	if maxY <= 0 {
		c.y = maxY / 2
	} else if c.y < 0 {
		c.y = 0
	} else if c.y > maxY {
		c.y = maxY
	}
}

// Position returns the top-left world pixel currently visible.
func (c *Camera) Position() (float64, float64) {
	if c == nil {
		return 0, 0
	}
	return c.x, c.y
}

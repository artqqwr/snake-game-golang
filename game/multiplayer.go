package game

// EnemyCollision reports whether a head is inside another snake's body.
func EnemyCollision(head Position, body []Position) bool {
	for _, segment := range body {
		if head == segment {
			return true
		}
	}
	return false
}

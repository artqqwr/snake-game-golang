# Simple snake game

Only snake, only appels.

![image](https://github.com/artqqwr/snake-game-golang/blob/main/image.png?raw=true)

The project is over a year old.

<details> <summary>The snake is implemented via double linked list. It is not justified in any way, I just wanted it that way:)</summary>
To be honest, I made it that way on purpose, so that each “piece” of the snake would be an “object”. That's why the snake colliding with its body can easily throw away the “bitten off tail” (snakeBodyPart.Left.Next = nil). </details>

At each “update” I move the head of the snake in the current direction, go through the entire snake and assign the position of the current part to the previous one (top to bottom).

I realize that this is not very productive. + there are “loops in a loop” in the code.

I'll have to rewrite it.

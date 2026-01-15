package model

import (
	"log"
	"math/rand"
	"sync"

	pb "lab4/internal/config"
	"google.golang.org/protobuf/proto"
)

const (
	DefaultStateOrder = 0
	DefaultNextId = 1

	Incr = 1
	SizeEmptyField = 5
	OffsetCenter = 2

	FoodDropEat = 0.5
)

func Int32Ptr(v int32) *int32 {
	return &v
}

func Int64Ptr(v int64) *int64 {
	return &v
}

func StringPtr(v string) *string {
	return &v
}

func BoolPtr(v bool) *bool {
	return &v
}

type GameEngine struct {
	Config       *pb.GameConfig
	State        *pb.GameState
	GameName     string
	stateOrder   int32
	mu           sync.RWMutex
	nextPlayerId int32
}

func NewGameEngine(config *pb.GameConfig, gameName string) *GameEngine {
	ge := &GameEngine{
		Config:       config,
		GameName:     gameName,
		stateOrder:   DefaultStateOrder,
		nextPlayerId: DefaultNextId,
	}
	ge.State = &pb.GameState{
		StateOrder: Int32Ptr(0),
		Snakes:     make([]*pb.GameState_Snake, 0),
		Foods:      make([]*pb.GameState_Coord, 0),
		Players:    &pb.GamePlayers{Players: make([]*pb.GamePlayer, 0)},
	}
	return ge
}

func NewGameEngineFromState(config *pb.GameConfig, state *pb.GameState, gameName string) *GameEngine {
	maxPlayerId := int32(0)
	if state.Players != nil {
		for _, p := range state.Players.Players {
			if p.GetId() > maxPlayerId {
				maxPlayerId = p.GetId()
			}
		}
	}

	ge := &GameEngine{
		Config:       config,
		GameName:     gameName,
		State:        state,
		stateOrder:   state.GetStateOrder(),
		nextPlayerId: maxPlayerId + Incr,
	}
	return ge
}

func (ge *GameEngine) GetWidth() int32 {
	return ge.Config.GetWidth()
}

func (ge *GameEngine) GetHeight() int32 {
	return ge.Config.GetHeight()
}

func (ge *GameEngine) normalizeX(x int32) int32 {
	width := ge.GetWidth()
	x = x % width
	if x < 0 {
		x += width
	}
	return x
}

func (ge *GameEngine) normalizeY(y int32) int32 {
	height := ge.GetHeight()
	y = y % height
	if y < 0 {
		y += height
	}
	return y
}

func (ge *GameEngine) ExpandSnake(snake *pb.GameState_Snake) []pb.GameState_Coord {
	if len(snake.Points) == 0 {
		return nil
	}

	var cells []pb.GameState_Coord

	headX := snake.Points[0].GetX()
	headY := snake.Points[0].GetY()
	cells = append(cells, pb.GameState_Coord{X: Int32Ptr(headX), Y: Int32Ptr(headY)})

	curX := headX
	curY := headY

	for i := 1; i < len(snake.Points); i++ {
		dx := snake.Points[i].GetX()
		dy := snake.Points[i].GetY()

		steps := abs32(dx) + abs32(dy)
		stepX := int32(0)
		stepY := int32(0)
		if dx != 0 {
			stepX = dx / abs32(dx)
		}
		if dy != 0 {
			stepY = dy / abs32(dy)
		}

		for j := int32(0); j < steps; j++ {
			curX = ge.normalizeX(curX + stepX)
			curY = ge.normalizeY(curY + stepY)
			cells = append(cells, pb.GameState_Coord{X: Int32Ptr(curX), Y: Int32Ptr(curY)})
		}
	}

	return cells
}

func abs32(x int32) int32 {
	if x < 0 {
		return -x
	}
	return x
}

func (ge *GameEngine) CompressSnake(cells []pb.GameState_Coord) []*pb.GameState_Coord {
	if len(cells) == 0 {
		return nil
	}

	result := []*pb.GameState_Coord{
		{X: Int32Ptr(cells[0].GetX()), Y: Int32Ptr(cells[0].GetY())},
	}

	if len(cells) == 1 {
		return result
	}

	prevX := cells[0].GetX()
	prevY := cells[0].GetY()

	offsetX := int32(0)
	offsetY := int32(0)

	for i := 1; i < len(cells); i++ {
		dx := cells[i].GetX() - prevX
		dy := cells[i].GetY() - prevY

		if dx > ge.GetWidth()/2 {
			dx -= ge.GetWidth()
		} else if dx < -ge.GetWidth()/2 {
			dx += ge.GetWidth()
		}
		if dy > ge.GetHeight()/2 {
			dy -= ge.GetHeight()
		} else if dy < -ge.GetHeight()/2 {
			dy += ge.GetHeight()
		}

		if (offsetX != 0 && dx != sign32(offsetX)) || (offsetY != 0 && dy != sign32(offsetY)) ||
			(offsetX == 0 && offsetY == 0) {
			if offsetX != 0 || offsetY != 0 {
				result = append(result, &pb.GameState_Coord{X: Int32Ptr(offsetX), Y: Int32Ptr(offsetY)})
			}
			offsetX = dx
			offsetY = dy
		} else {
			offsetX += dx
			offsetY += dy
		}

		prevX = cells[i].GetX()
		prevY = cells[i].GetY()
	}

	if offsetX != 0 || offsetY != 0 {
		result = append(result, &pb.GameState_Coord{X: Int32Ptr(offsetX), Y: Int32Ptr(offsetY)})
	}

	return result
}

func sign32(x int32) int32 {
	if x > 0 {
		return 1
	} else if x < 0 {
		return -1
	}
	return 0
}

func (ge *GameEngine) FindFreeSquare() (int32, int32, bool) {
	ge.mu.RLock()
	defer ge.mu.RUnlock()

	width := ge.GetWidth()
	height := ge.GetHeight()

	occupied := make(map[int64]bool)
	for _, snake := range ge.State.Snakes {
		cells := ge.ExpandSnake(snake)
		for _, c := range cells {
			key := int64(c.GetY())*int64(width) + int64(c.GetX())
			occupied[key] = true
		}
	}

	for _, food := range ge.State.Foods {
		key := int64(food.GetY())*int64(width) + int64(food.GetX())
		occupied[key] = true
	}

	for startY := int32(0); startY < height; startY++ {
		for startX := int32(0); startX < width; startX++ {
			found := true
			for dy := int32(0); dy < SizeEmptyField && found; dy++ {
				for dx := int32(0); dx < SizeEmptyField && found; dx++ {
					x := ge.normalizeX(startX + dx)
					y := ge.normalizeY(startY + dy)
					key := int64(y)*int64(width) + int64(x)
					if occupied[key] {
						found = false
					}
				}
			}
			if found {
				centerX := ge.normalizeX(startX + 2)
				centerY := ge.normalizeY(startY + 2)
				return centerX, centerY, true
			}
		}
	}
	return 0, 0, false
}

func (ge *GameEngine) AddPlayer(name string, role pb.NodeRole, ipAddr string, port int32) int32 {
	ge.mu.Lock()
	defer ge.mu.Unlock()

	playerId := ge.nextPlayerId
	ge.nextPlayerId++

	player := &pb.GamePlayer{
		Name:      StringPtr(name),
		Id:        Int32Ptr(playerId),
		IpAddress: StringPtr(ipAddr),
		Port:      Int32Ptr(port),
		Role:      role.Enum(),
		Type:      pb.PlayerType_HUMAN.Enum(),
		Score:     Int32Ptr(0),
	}

	ge.State.Players.Players = append(ge.State.Players.Players, player)
	return playerId
}

func (ge *GameEngine) CreateSnakeForPlayer(playerId int32) bool {
	centerX, centerY, found := ge.FindFreeSquare()
	if !found {
		return false
	}

	directions := []pb.Direction{pb.Direction_UP, pb.Direction_DOWN, pb.Direction_LEFT, pb.Direction_RIGHT}
	tailDir := directions[rand.Intn(4)]

	var tailX, tailY int32
	var headDir pb.Direction

	switch tailDir {
	case pb.Direction_UP:
		tailX, tailY = centerX, ge.normalizeY(centerY-Incr)
		headDir = pb.Direction_DOWN
	case pb.Direction_DOWN:
		tailX, tailY = centerX, ge.normalizeY(centerY+Incr)
		headDir = pb.Direction_UP
	case pb.Direction_LEFT:
		tailX, tailY = ge.normalizeX(centerX-Incr), centerY
		headDir = pb.Direction_RIGHT
	case pb.Direction_RIGHT:
		tailX, tailY = ge.normalizeX(centerX+Incr), centerY
		headDir = pb.Direction_LEFT
	}

	offsetX := tailX - centerX
	offsetY := tailY - centerY

	if offsetX > ge.GetWidth()/2 {
		offsetX -= ge.GetWidth()
	} else if offsetX < -ge.GetWidth()/2 {
		offsetX += ge.GetWidth()
	}
	if offsetY > ge.GetHeight()/2 {
		offsetY -= ge.GetHeight()
	} else if offsetY < -ge.GetHeight()/2 {
		offsetY += ge.GetHeight()
	}

	ge.mu.Lock()
	snake := &pb.GameState_Snake{
		PlayerId: Int32Ptr(playerId),
		Points: []*pb.GameState_Coord{
			{X: Int32Ptr(centerX), Y: Int32Ptr(centerY)},
			{X: Int32Ptr(offsetX), Y: Int32Ptr(offsetY)},
		},
		State:         pb.GameState_Snake_ALIVE.Enum(),
		HeadDirection: headDir.Enum(),
	}
	ge.State.Snakes = append(ge.State.Snakes, snake)
	ge.mu.Unlock()

	return true
}

func (ge *GameEngine) GetPlayer(playerId int32) *pb.GamePlayer {
	ge.mu.RLock()
	defer ge.mu.RUnlock()

	for _, p := range ge.State.Players.Players {
		if p.GetId() == playerId {
			return p
		}
	}
	return nil
}

func (ge *GameEngine) GetSnake(playerId int32) *pb.GameState_Snake {
	ge.mu.RLock()
	defer ge.mu.RUnlock()

	for _, s := range ge.State.Snakes {
		if s.GetPlayerId() == playerId {
			return s
		}
	}
	return nil
}

func (ge *GameEngine) IsPlayerAlive(playerId int32) bool {
	ge.mu.RLock()
	defer ge.mu.RUnlock()

	for _, s := range ge.State.Snakes {
		if s.GetPlayerId() == playerId && len(s.Points) > 0 && s.GetState() == pb.GameState_Snake_ALIVE {
			return true
		}
	}
	return false
}

func (ge *GameEngine) GetAlivePlayerIds() []int32 {
	ge.mu.RLock()
	defer ge.mu.RUnlock()

	var result []int32
	for _, s := range ge.State.Snakes {
		if len(s.Points) > 0 && s.GetState() == pb.GameState_Snake_ALIVE {
			result = append(result, s.GetPlayerId())
		}
	}
	return result
}

func (ge *GameEngine) HasAnyAlivePlayers() bool {
	ge.mu.RLock()
	defer ge.mu.RUnlock()

	for _, s := range ge.State.Snakes {
		if len(s.Points) > 0 && s.GetState() == pb.GameState_Snake_ALIVE {
			return true
		}
	}
	return false
}

func (ge *GameEngine) SetSnakeDirection(playerId int32, dir pb.Direction) bool {
	ge.mu.Lock()
	defer ge.mu.Unlock()

	for _, snake := range ge.State.Snakes {
		if snake.GetPlayerId() == playerId && snake.GetState() == pb.GameState_Snake_ALIVE {
			currentDir := snake.GetHeadDirection()

			if (currentDir == pb.Direction_UP && dir == pb.Direction_DOWN) ||
				(currentDir == pb.Direction_DOWN && dir == pb.Direction_UP) ||
				(currentDir == pb.Direction_LEFT && dir == pb.Direction_RIGHT) ||
				(currentDir == pb.Direction_RIGHT && dir == pb.Direction_LEFT) {
				return false
			}

			snake.HeadDirection = dir.Enum()
			return true
		}
	}
	return false
}

func (ge *GameEngine) UpdateState() {
	ge.mu.Lock()
	defer ge.mu.Unlock()

	width := ge.GetWidth()

	type HeadInfo struct {
		snakeIdx int
		x, y     int32
	}
	newHeads := make(map[int]HeadInfo) 

	for idx, snake := range ge.State.Snakes {
		if len(snake.Points) == 0 {
			continue
		}

		cells := ge.expandSnakeUnsafe(snake)
		if len(cells) == 0 {
			continue
		}

		dir := snake.GetHeadDirection()
		headX := cells[0].GetX()
		headY := cells[0].GetY()

		var newHeadX, newHeadY int32
		switch dir {
		case pb.Direction_UP:
			newHeadX, newHeadY = headX, ge.normalizeY(headY-1)
		case pb.Direction_DOWN:
			newHeadX, newHeadY = headX, ge.normalizeY(headY+1)
		case pb.Direction_LEFT:
			newHeadX, newHeadY = ge.normalizeX(headX-1), headY
		case pb.Direction_RIGHT:
			newHeadX, newHeadY = ge.normalizeX(headX+1), headY
		}

		newHeads[idx] = HeadInfo{idx, newHeadX, newHeadY}
	}

	foodMap := make(map[int64]bool)
	for _, f := range ge.State.Foods {
		key := int64(f.GetY())*int64(width) + int64(f.GetX())
		foodMap[key] = true
	}

	willEatFood := make(map[int]bool)
	for idx, head := range newHeads {
		key := int64(head.y)*int64(width) + int64(head.x)
		if foodMap[key] {
			willEatFood[idx] = true
		}
	}

	occupied := make(map[int64]int)
	for idx, snake := range ge.State.Snakes {
		if len(snake.Points) == 0 {
			continue
		}
		cells := ge.expandSnakeUnsafe(snake)
		lastIdx := len(cells) - 1
		for i, c := range cells {
			if i == lastIdx && !willEatFood[idx] {
				continue
			}
			key := int64(c.GetY())*int64(width) + int64(c.GetX())
			occupied[key] = idx
		}
	}

	ateFood := make([]bool, len(ge.State.Snakes))
	dead := make([]bool, len(ge.State.Snakes))

	headPositions := make(map[int64][]int)
	for idx, head := range newHeads {
		key := int64(head.y)*int64(width) + int64(head.x)
		headPositions[key] = append(headPositions[key], idx)
	}

	for pos, snakes := range headPositions {
		if len(snakes) > 1 {
			y := int32(pos / int64(width))
			x := int32(pos % int64(width))
			log.Printf("Head-to-head collision at (%d, %d), snakes: %v", x, y, snakes)
			for _, idx := range snakes {
				dead[idx] = true
			}
		}
	}

	for idx, head := range newHeads {
		if dead[idx] {
			continue
		}

		key := int64(head.y)*int64(width) + int64(head.x)

		if foodMap[key] {
			ateFood[idx] = true
			delete(foodMap, key)
		}

		if victimIdx, exists := occupied[key]; exists {
			dead[idx] = true
			log.Printf("Snake %d died - collision with snake %d body at (%d, %d)", idx, victimIdx, head.x, head.y)
			if victimIdx != idx && !dead[victimIdx] {
				for _, p := range ge.State.Players.Players {
					if p.GetId() == ge.State.Snakes[victimIdx].GetPlayerId() {
						*p.Score++
						break
					}
				}
			}
		}
	}

	for idx, snake := range ge.State.Snakes {
		if len(snake.Points) == 0 {
			continue
		}

		if dead[idx] {
			cells := ge.expandSnakeUnsafe(snake)
			for _, c := range cells {
				if rand.Float32() < FoodDropEat {
					key := int64(c.GetY())*int64(width) + int64(c.GetX())
					foodMap[key] = true
				}
			}
			snake.Points = nil
			log.Printf("Snake %d (player %d) died and removed", idx, snake.GetPlayerId())
			continue
		}

		head, hasHead := newHeads[idx]
		if !hasHead {
			continue
		}

		cells := ge.expandSnakeUnsafe(snake)

		newCells := []pb.GameState_Coord{{X: Int32Ptr(head.x), Y: Int32Ptr(head.y)}}
		newCells = append(newCells, cells...)

		if !ateFood[idx] && len(newCells) > 1 {
			newCells = newCells[:len(newCells)-Incr]
		} else if ateFood[idx] {
			for _, p := range ge.State.Players.Players {
				if p.GetId() == snake.GetPlayerId() {
					*p.Score++
					break
				}
			}
		}

		snake.Points = ge.compressSnakeUnsafe(newCells)
	}

	aliveSnakes := make([]*pb.GameState_Snake, 0)
	for _, snake := range ge.State.Snakes {
		if len(snake.Points) > 0 {
			aliveSnakes = append(aliveSnakes, snake)
		}
	}
	ge.State.Snakes = aliveSnakes

	newFoods := make([]*pb.GameState_Coord, 0)
	for key := range foodMap {
		y := int32(key / int64(width))
		x := int32(key % int64(width))
		newFoods = append(newFoods, &pb.GameState_Coord{X: Int32Ptr(x), Y: Int32Ptr(y)})
	}
	ge.State.Foods = newFoods

	ge.spawnFoodUnsafe()

	ge.stateOrder++
	ge.State.StateOrder = Int32Ptr(ge.stateOrder)
}

func (ge *GameEngine) spawnFoodUnsafe() {
	aliveCount := int32(0)
	for _, snake := range ge.State.Snakes {
		if snake.GetState() == pb.GameState_Snake_ALIVE {
			aliveCount++
		}
	}

	targetFood := ge.Config.GetFoodStatic() + aliveCount
	currentFood := int32(len(ge.State.Foods))

	if currentFood >= targetFood {
		return
	}

	width := ge.GetWidth()
	height := ge.GetHeight()
	occupied := make(map[int64]bool)

	for _, snake := range ge.State.Snakes {
		cells := ge.expandSnakeUnsafe(snake)
		for _, c := range cells {
			key := int64(c.GetY())*int64(width) + int64(c.GetX())
			occupied[key] = true
		}
	}
	for _, f := range ge.State.Foods {
		key := int64(f.GetY())*int64(width) + int64(f.GetX())
		occupied[key] = true
	}

	var emptyCells []int64
	for y := int32(0); y < height; y++ {
		for x := int32(0); x < width; x++ {
			key := int64(y)*int64(width) + int64(x)
			if !occupied[key] {
				emptyCells = append(emptyCells, key)
			}
		}
	}

	toSpawn := int(targetFood - currentFood)
	if toSpawn > len(emptyCells) {
		toSpawn = len(emptyCells)
	}

	rand.Shuffle(len(emptyCells), func(i, j int) {
		emptyCells[i], emptyCells[j] = emptyCells[j], emptyCells[i]
	})

	for i := 0; i < toSpawn; i++ {
		key := emptyCells[i]
		y := int32(key / int64(width))
		x := int32(key % int64(width))
		ge.State.Foods = append(ge.State.Foods, &pb.GameState_Coord{
			X: Int32Ptr(x),
			Y: Int32Ptr(y),
		})
	}
}

func (ge *GameEngine) expandSnakeUnsafe(snake *pb.GameState_Snake) []pb.GameState_Coord {
	if len(snake.Points) == 0 {
		return nil
	}

	var cells []pb.GameState_Coord
	headX := snake.Points[0].GetX()
	headY := snake.Points[0].GetY()
	cells = append(cells, pb.GameState_Coord{X: Int32Ptr(headX), Y: Int32Ptr(headY)})

	curX := headX
	curY := headY

	for i := 1; i < len(snake.Points); i++ {
		dx := snake.Points[i].GetX()
		dy := snake.Points[i].GetY()

		steps := abs32(dx) + abs32(dy)
		stepX := int32(0)
		stepY := int32(0)
		if dx != 0 {
			stepX = dx / abs32(dx)
		}
		if dy != 0 {
			stepY = dy / abs32(dy)
		}

		for j := int32(0); j < steps; j++ {
			curX = ge.normalizeX(curX + stepX)
			curY = ge.normalizeY(curY + stepY)
			cells = append(cells, pb.GameState_Coord{X: Int32Ptr(curX), Y: Int32Ptr(curY)})
		}
	}

	return cells
}

func (ge *GameEngine) compressSnakeUnsafe(cells []pb.GameState_Coord) []*pb.GameState_Coord {
	if len(cells) == 0 {
		return nil
	}

	width := ge.Config.GetWidth()
	height := ge.Config.GetHeight()

	result := []*pb.GameState_Coord{
		{X: Int32Ptr(cells[0].GetX()), Y: Int32Ptr(cells[0].GetY())},
	}

	if len(cells) == 1 {
		return result
	}

	prevX := cells[0].GetX()
	prevY := cells[0].GetY()
	offsetX := int32(0)
	offsetY := int32(0)

	for i := 1; i < len(cells); i++ {
		dx := cells[i].GetX() - prevX
		dy := cells[i].GetY() - prevY

		if dx > width/2 {
			dx -= width
		} else if dx < -width/2 {
			dx += width
		}
		if dy > height/2 {
			dy -= height
		} else if dy < -height/2 {
			dy += height
		}

		if (offsetX != 0 && dx != sign32(offsetX)) || (offsetY != 0 && dy != sign32(offsetY)) ||
			(offsetX == 0 && offsetY == 0) {
			if offsetX != 0 || offsetY != 0 {
				result = append(result, &pb.GameState_Coord{X: Int32Ptr(offsetX), Y: Int32Ptr(offsetY)})
			}
			offsetX = dx
			offsetY = dy
		} else {
			offsetX += dx
			offsetY += dy
		}

		prevX = cells[i].GetX()
		prevY = cells[i].GetY()
	}

	if offsetX != 0 || offsetY != 0 {
		result = append(result, &pb.GameState_Coord{X: Int32Ptr(offsetX), Y: Int32Ptr(offsetY)})
	}

	return result
}

func (ge *GameEngine) SetPlayerRole(playerId int32, role pb.NodeRole) {
	ge.mu.Lock()
	defer ge.mu.Unlock()

	for _, p := range ge.State.Players.Players {
		if p.GetId() == playerId {
			p.Role = role.Enum()
			log.Printf("Set player %d role to %v", playerId, role)
			return
		}
	}
}

func (ge *GameEngine) MakeSnakeZombie(playerId int32) {
	ge.mu.Lock()
	defer ge.mu.Unlock()

	for _, snake := range ge.State.Snakes {
		if snake.GetPlayerId() == playerId {
			snake.State = pb.GameState_Snake_ZOMBIE.Enum()
			log.Printf("Made snake of player %d ZOMBIE", playerId)
			break
		}
	}
}

func (ge *GameEngine) RemovePlayer(playerId int32) {
	ge.mu.Lock()
	defer ge.mu.Unlock()

	for _, snake := range ge.State.Snakes {
		if snake.GetPlayerId() == playerId {
			snake.State = pb.GameState_Snake_ZOMBIE.Enum()
			break
		}
	}

	players := ge.State.Players.Players
	for i, p := range players {
		if p.GetId() == playerId {
			ge.State.Players.Players = append(players[:i], players[i+1:]...)
			log.Printf("Removed player %d from players list", playerId)
			break
		}
	}
}

func (ge *GameEngine) GetState() *pb.GameState {
	ge.mu.RLock()
	defer ge.mu.RUnlock()
	return ge.State
}

func (ge *GameEngine) GetStateCopy() *pb.GameState {
	ge.mu.RLock()
	defer ge.mu.RUnlock()
	return proto.Clone(ge.State).(*pb.GameState)
}

func (ge *GameEngine) SetState(state *pb.GameState) {
	ge.mu.Lock()
	defer ge.mu.Unlock()

	if state.GetStateOrder() > ge.stateOrder {
		ge.State = state
		ge.stateOrder = state.GetStateOrder()
	}
}

func (ge *GameEngine) GetAlivePlayersCount() int {
	return len(ge.GetAlivePlayerIds())
}

func (ge *GameEngine) FindNormalPlayer() *pb.GamePlayer {
	ge.mu.RLock()
	defer ge.mu.RUnlock()

	aliveIds := make(map[int32]bool)
	for _, s := range ge.State.Snakes {
		if len(s.Points) > 0 && s.GetState() == pb.GameState_Snake_ALIVE {
			aliveIds[s.GetPlayerId()] = true
		}
	}

	for _, p := range ge.State.Players.Players {
		if p.GetRole() == pb.NodeRole_NORMAL && aliveIds[p.GetId()] {
			return p
		}
	}
	return nil
}

func (ge *GameEngine) GetDeputy() *pb.GamePlayer {
	ge.mu.RLock()
	defer ge.mu.RUnlock()

	for _, p := range ge.State.Players.Players {
		if p.GetRole() == pb.NodeRole_DEPUTY {
			return p
		}
	}
	return nil
}

func (ge *GameEngine) GetMaster() *pb.GamePlayer {
	ge.mu.RLock()
	defer ge.mu.RUnlock()

	for _, p := range ge.State.Players.Players {
		if p.GetRole() == pb.NodeRole_MASTER {
			return p
		}
	}
	return nil
}

func (ge *GameEngine) GetAliveSnakesCount() int {
	ge.mu.RLock()
	defer ge.mu.RUnlock()

	count := 0
	for _, snake := range ge.State.Snakes {
		if len(snake.Points) > 0 {
			count++
		}
	}
	return count
}

func (ge *GameEngine) HasSnakes() bool {
	ge.mu.RLock()
	defer ge.mu.RUnlock()
	for _, snake := range ge.State.Snakes {
		if len(snake.Points) > 0 {
			return true
		}
	}
	return false
}

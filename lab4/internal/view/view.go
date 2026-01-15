package view

import (
	"fmt"
	"image/color"
	"sort"
	"sync"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/app"
	"fyne.io/fyne/v2/canvas"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/dialog"
	"fyne.io/fyne/v2/layout"
	"fyne.io/fyne/v2/theme"
	"fyne.io/fyne/v2/widget"

	pb "lab4/internal/config"
	"lab4/internal/model"
)

type EventType int

const (
	EventNewGame EventType = iota
	EventJoinGame
	EventExit
	EventDirection
	EventLeaveGame
)

type ViewEvent struct {
	Type       EventType
	Direction  pb.Direction
	GameName   string
	AsViewer   bool
	Config     *pb.GameConfig
	PlayerName string
}

type GameListItem struct {
	Name       string
	Players    int
	CanJoin    bool
	Width      int32
	Height     int32
	FoodStatic int32
	DelayMs    int32
}

type View struct {
	app         fyne.App
	window      fyne.Window
	EventChan   chan ViewEvent

	mainContainer *fyne.Container
	gamesList     *widget.List
	gamesData     []GameListItem
	gamesMu       sync.RWMutex

	widthEntry      *widget.Entry
	heightEntry     *widget.Entry
	foodStaticEntry *widget.Entry
	delayMsEntry    *widget.Entry
	playerNameEntry *widget.Entry
	gameNameEntry   *widget.Entry

	gameContainer   *fyne.Container
	gameField       *GameFieldWidget
	scoresList      *widget.List
	scoresData      []PlayerScore
	scoresMu        sync.RWMutex

	currentWidth  int32
	currentHeight int32

	isInGame       bool
	dialogShowing  bool 
	mu             sync.RWMutex
}

type PlayerScore struct {
	Name     string
	Score    int32
	Role     string
	RoleRank int  
	IsMe     bool 
}

var (
	colorBackground  = color.RGBA{R: 30, G: 30, B: 40, A: 255}
	colorGrid        = color.RGBA{R: 50, G: 50, B: 60, A: 255}
	colorSnakeHead   = color.RGBA{R: 0, G: 200, B: 100, A: 255}
	colorSnakeBody   = color.RGBA{R: 0, G: 150, B: 80, A: 255}
	colorSnakeOther  = color.RGBA{R: 200, G: 100, B: 50, A: 255}
	colorSnakeZombie = color.RGBA{R: 128, G: 128, B: 128, A: 255}
	colorFood        = color.RGBA{R: 255, G: 50, B: 50, A: 255}
)

type GameFieldWidget struct {
	widget.BaseWidget
	width      int32
	height     int32
	state      *pb.GameState
	myPlayerId int32
	mu         sync.RWMutex
}

func NewGameFieldWidget(width, height int32) *GameFieldWidget {
	w := &GameFieldWidget{
		width:  width,
		height: height,
	}
	w.ExtendBaseWidget(w)
	return w
}

func (g *GameFieldWidget) SetState(state *pb.GameState, myPlayerId int32) {
	g.mu.Lock()
	g.state = state
	g.myPlayerId = myPlayerId
	g.mu.Unlock()
	g.Refresh()
}

func (g *GameFieldWidget) CreateRenderer() fyne.WidgetRenderer {
	return &gameFieldRenderer{field: g}
}

func (g *GameFieldWidget) MinSize() fyne.Size {
	cellSize := float32(12)
	return fyne.NewSize(float32(g.width)*cellSize+2, float32(g.height)*cellSize+2)
}

type gameFieldRenderer struct {
	field *GameFieldWidget
}

func (r *gameFieldRenderer) Layout(size fyne.Size) {}

func (r *gameFieldRenderer) MinSize() fyne.Size {
	return r.field.MinSize()
}

func (r *gameFieldRenderer) Refresh() {}

func (r *gameFieldRenderer) Objects() []fyne.CanvasObject {
	r.field.mu.RLock()
	defer r.field.mu.RUnlock()

	width := r.field.width
	height := r.field.height
	state := r.field.state
	myPlayerId := r.field.myPlayerId

	cellSize := float32(12)

	objects := make([]fyne.CanvasObject, 0)

	bg := canvas.NewRectangle(colorBackground)
	bg.Resize(fyne.NewSize(float32(width)*cellSize, float32(height)*cellSize))
	bg.Move(fyne.NewPos(0, 0))
	objects = append(objects, bg)

	for x := int32(0); x <= width; x++ {
		line := canvas.NewLine(colorGrid)
		line.StrokeWidth = 1
		line.Position1 = fyne.NewPos(float32(x)*cellSize, 0)
		line.Position2 = fyne.NewPos(float32(x)*cellSize, float32(height)*cellSize)
		objects = append(objects, line)
	}
	for y := int32(0); y <= height; y++ {
		line := canvas.NewLine(colorGrid)
		line.StrokeWidth = 1
		line.Position1 = fyne.NewPos(0, float32(y)*cellSize)
		line.Position2 = fyne.NewPos(float32(width)*cellSize, float32(y)*cellSize)
		objects = append(objects, line)
	}

	if state == nil {
		return objects
	}

	for _, food := range state.Foods {
		x := food.GetX()
		y := food.GetY()

		foodRect := canvas.NewRectangle(colorFood)
		foodRect.Resize(fyne.NewSize(cellSize-2, cellSize-2))
		foodRect.Move(fyne.NewPos(float32(x)*cellSize+1, float32(y)*cellSize+1))
		foodRect.CornerRadius = cellSize / 4
		objects = append(objects, foodRect)
	}

	for _, snake := range state.Snakes {
		if len(snake.Points) == 0 {
			continue
		}

		cells := expandSnakeForView(snake, width, height)
		isMySnake := snake.GetPlayerId() == myPlayerId
		isZombie := snake.GetState() == pb.GameState_Snake_ZOMBIE

		for i, cell := range cells {
			x := cell.GetX()
			y := cell.GetY()

			var cellColor color.Color
			if isZombie {
				cellColor = colorSnakeZombie
			} else if isMySnake {
				if i == 0 {
					cellColor = colorSnakeHead
				} else {
					cellColor = colorSnakeBody
				}
			} else {
				cellColor = colorSnakeOther
			}

			snakeRect := canvas.NewRectangle(cellColor)
			snakeRect.Resize(fyne.NewSize(cellSize-1, cellSize-1))
			snakeRect.Move(fyne.NewPos(float32(x)*cellSize+0.5, float32(y)*cellSize+0.5))
			if i == 0 {
				snakeRect.CornerRadius = cellSize / 3
			} else {
				snakeRect.CornerRadius = cellSize / 6
			}
			objects = append(objects, snakeRect)
		}
	}

	return objects
}

func (r *gameFieldRenderer) Destroy() {}

func NewView() *View {
	return &View{
		EventChan:  make(chan ViewEvent, 10),
		gamesData:  make([]GameListItem, 0),
		scoresData: make([]PlayerScore, 0),
	}
}

func (v *View) Init() {
	v.app = app.NewWithID("snake.game")
	v.app.Settings().SetTheme(theme.DarkTheme())
}

func (v *View) StartWindow() {
	v.window = v.app.NewWindow("🐍 Snake Game")
	v.window.Resize(fyne.NewSize(900, 700))
	v.window.CenterOnScreen()
	
	v.window.SetCloseIntercept(func() {
		v.EventChan <- ViewEvent{Type: EventExit}
	})

	v.showMainMenu()

	v.window.Canvas().SetOnTypedKey(v.handleKeyPress)

	v.window.ShowAndRun()
}

func (v *View) showMainMenu() {
	v.mu.Lock()
	v.isInGame = false
	v.mu.Unlock()

	title := canvas.NewText("🐍 SNAKE", color.White)
	title.TextSize = 32
	title.TextStyle = fyne.TextStyle{Bold: true}
	title.Alignment = fyne.TextAlignCenter

	subtitle := canvas.NewText("ЗМЕЙКА", color.RGBA{R: 150, G: 150, B: 150, A: 255})
	subtitle.TextSize = 16
	subtitle.Alignment = fyne.TextAlignCenter

	v.playerNameEntry = widget.NewEntry()
	v.playerNameEntry.SetPlaceHolder("Введите имя игрока")
	v.playerNameEntry.SetText("Miroslav")

	v.gamesList = widget.NewList(
		func() int {
			v.gamesMu.RLock()
			defer v.gamesMu.RUnlock()
			return len(v.gamesData)
		},
		func() fyne.CanvasObject {
			return container.NewHBox(
				widget.NewLabel("Game Name"),
				layout.NewSpacer(),
				widget.NewLabel("Players"),
				widget.NewLabel("Size"),
			)
		},
		func(id widget.ListItemID, obj fyne.CanvasObject) {
			v.gamesMu.RLock()
			defer v.gamesMu.RUnlock()
			if id >= len(v.gamesData) {
				return
			}
			game := v.gamesData[id]
			box, ok := obj.(*fyne.Container)
			if !ok || len(box.Objects) < 4 {
				return
			}
			if label, ok := box.Objects[0].(*widget.Label); ok {
				label.SetText(game.Name)
			}
			if label, ok := box.Objects[2].(*widget.Label); ok {
				label.SetText(fmt.Sprintf("%d игроков", game.Players))
			}
			if label, ok := box.Objects[3].(*widget.Label); ok {
				label.SetText(fmt.Sprintf("%dx%d", game.Width, game.Height))
			}
		},
	)
	v.gamesList.OnSelected = func(id widget.ListItemID) {
		v.gamesMu.RLock()
		if id < len(v.gamesData) {
			game := v.gamesData[id]
			v.gamesMu.RUnlock()
			v.gamesList.UnselectAll()
			v.showJoinDialog(game)
		} else {
			v.gamesMu.RUnlock()
		}
	}

	gamesCard := widget.NewCard("Доступные игры", "Выберите игру для подключения", 
		container.NewVScroll(v.gamesList))

	v.gameNameEntry = widget.NewEntry()
	v.gameNameEntry.SetPlaceHolder("Название игры")
	v.gameNameEntry.SetText("У Мирослава")

	v.widthEntry = widget.NewEntry()
	v.widthEntry.SetText("40")

	v.heightEntry = widget.NewEntry()
	v.heightEntry.SetText("30")

	v.foodStaticEntry = widget.NewEntry()
	v.foodStaticEntry.SetText("100")

	v.delayMsEntry = widget.NewEntry()
	v.delayMsEntry.SetText("200")

	settingsForm := container.NewVBox(
		widget.NewLabel("Название игры:"),
		v.gameNameEntry,
		container.NewGridWithColumns(2,
			container.NewVBox(widget.NewLabel("Ширина:"), v.widthEntry),
			container.NewVBox(widget.NewLabel("Высота:"), v.heightEntry),
		),
		container.NewGridWithColumns(2,
			container.NewVBox(widget.NewLabel("Базовая еда:"), v.foodStaticEntry),
			container.NewVBox(widget.NewLabel("Задержка (мс):"), v.delayMsEntry),
		),
	)

	newGameBtn := widget.NewButton("🎮 Создать игру", func() {
		v.onNewGame()
	})
	newGameBtn.Importance = widget.HighImportance

	exitBtn := widget.NewButton("🚪 Выход", func() {
		v.EventChan <- ViewEvent{Type: EventExit}
	})

	newGameCard := widget.NewCard("Новая игра", "Настройки игры", 
		container.NewVBox(settingsForm, newGameBtn))

	leftPanel := container.NewVBox(
		container.NewVBox(
			widget.NewLabel("Имя игрока:"),
			v.playerNameEntry,
		),
		widget.NewSeparator(),
		newGameCard,
		layout.NewSpacer(),
		exitBtn,
	)

	rightPanel := container.NewMax(gamesCard)

	content := container.NewBorder(
		container.NewVBox(title, subtitle, widget.NewSeparator()),
		nil, nil, nil,
		container.NewHSplit(
			container.NewPadded(leftPanel),
			container.NewPadded(rightPanel),
		),
	)

	v.mainContainer = content
	v.window.SetContent(container.NewPadded(content))
}

func (v *View) showJoinDialog(game GameListItem) {
	if !game.CanJoin {
		dialog.ShowInformation("Игра заполнена", 
			"В этой игре нет свободного места. Вы можете присоединиться как наблюдатель.",
			v.window)
	}

	nameEntry := widget.NewEntry()
	nameEntry.SetPlaceHolder("Введите имя")
	nameEntry.SetText(v.playerNameEntry.Text) 

	var d *dialog.CustomDialog

	joinBtn := widget.NewButton("Присоединиться как игрок", func() {
		playerName := nameEntry.Text
		if playerName == "" {
			playerName = "Player"
		}
		d.Hide()
		v.EventChan <- ViewEvent{
			Type:       EventJoinGame,
			GameName:   game.Name,
			AsViewer:   false,
			PlayerName: playerName,
		}
	})
	joinBtn.Importance = widget.HighImportance
	if !game.CanJoin {
		joinBtn.Disable()
	}

	viewerBtn := widget.NewButton("Присоединиться как наблюдатель", func() {
		playerName := nameEntry.Text
		if playerName == "" {
			playerName = "Viewer"
		}
		d.Hide()
		v.EventChan <- ViewEvent{
			Type:       EventJoinGame,
			GameName:   game.Name,
			AsViewer:   true,
			PlayerName: playerName,
		}
	})

	// cancelBtn := widget.NewButton("Отмена", func() {
	// 	d.Hide()
	// })

	content := container.NewVBox(
		widget.NewLabel(fmt.Sprintf("Игра: %s", game.Name)),
		widget.NewLabel(fmt.Sprintf("Размер поля: %dx%d", game.Width, game.Height)),
		widget.NewLabel(fmt.Sprintf("Игроков: %d", game.Players)),
		widget.NewLabel(fmt.Sprintf("Задержка: %d мс", game.DelayMs)),
		widget.NewSeparator(),
		widget.NewLabel("Имя игрока:"),
		nameEntry,
		widget.NewSeparator(),
		joinBtn,
		viewerBtn,
		//cancelBtn,
	)

	d = dialog.NewCustom("Присоединиться к игре", "Закрыть", content, v.window)
	d.Show()
}

func (v *View) onNewGame() {
	var width, height, foodStatic, delayMs int32
	fmt.Sscanf(v.widthEntry.Text, "%d", &width)
	fmt.Sscanf(v.heightEntry.Text, "%d", &height)
	fmt.Sscanf(v.foodStaticEntry.Text, "%d", &foodStatic)
	fmt.Sscanf(v.delayMsEntry.Text, "%d", &delayMs)

	if width < 10 || width > 100 {
		width = 40
	}
	if height < 10 || height > 100 {
		height = 30
	}
	if foodStatic < 0 || foodStatic > 100 {
		foodStatic = 1
	}
	if delayMs < 100 || delayMs > 3000 {
		delayMs = 300
	}

	config := &pb.GameConfig{
		Width:        model.Int32Ptr(width),
		Height:       model.Int32Ptr(height),
		FoodStatic:   model.Int32Ptr(foodStatic),
		StateDelayMs: model.Int32Ptr(delayMs),
	}

	v.EventChan <- ViewEvent{
		Type:     EventNewGame,
		Config:   config,
		GameName: v.gameNameEntry.Text,
	}
}

func (v *View) ShowGameScreen(width, height int32) {
	v.mu.Lock()
	v.isInGame = true
	v.currentWidth = width
	v.currentHeight = height
	v.mu.Unlock()

	fyne.Do(func() {
		v.gameField = NewGameFieldWidget(width, height)

		v.scoresList = widget.NewList(
			func() int {
				v.scoresMu.RLock()
				defer v.scoresMu.RUnlock()
				return len(v.scoresData)
			},
			func() fyne.CanvasObject {
				text := canvas.NewText("Name [ROLE] - 0", color.White)
				text.TextSize = 14
				return text
			},
			func(id widget.ListItemID, obj fyne.CanvasObject) {
				v.scoresMu.RLock()
				defer v.scoresMu.RUnlock()
				if id >= len(v.scoresData) {
					return
				}
				ps := v.scoresData[id]
				text, ok := obj.(*canvas.Text)
				if !ok {
					return
				}
				text.Text = fmt.Sprintf("%s %s - %d", ps.Name, ps.Role, ps.Score)
				if ps.IsMe {
					text.Color = color.RGBA{R: 50, G: 255, B: 50, A: 255} 
				} else {
					text.Color = color.White
				}
				text.Refresh()
			},
		)

		scoresWrapper := container.NewStack(v.scoresList)
		scoresWrapper.Resize(fyne.NewSize(200, 200))
		scoresCard := widget.NewCard("Игроки", "", scoresWrapper)

		leaveBtn := widget.NewButton("🚪 Покинуть игру", func() {
			v.EventChan <- ViewEvent{Type: EventLeaveGame}
		})

		controlsLabel := widget.NewLabel("Управление: WASD или стрелки")
		controlsLabel.TextStyle = fyne.TextStyle{Italic: true}

		bottomControls := container.NewVBox(controlsLabel, leaveBtn)
		rightPanel := container.NewBorder(
			nil,
			bottomControls,
			nil, nil,
			scoresCard, 
		)
		rightPanel.Resize(fyne.NewSize(220, 400))

		gameScreen := container.NewBorder(
			nil,
			nil,
			nil,
			container.NewPadded(rightPanel),
			container.NewCenter(v.gameField),
		)

		v.gameContainer = gameScreen
		v.window.SetContent(container.NewPadded(gameScreen))
		v.window.Canvas().Focus(nil)
	})
}

func (v *View) RenderState(state *pb.GameState, myPlayerId int32) {
	v.mu.RLock()
	if !v.isInGame || v.gameField == nil {
		v.mu.RUnlock()
		return
	}
	v.mu.RUnlock()

	fyne.Do(func() {
		v.gameField.SetState(state, myPlayerId)
	})

	v.updateScores(state, myPlayerId)
}

func (v *View) updateScores(state *pb.GameState, myPlayerId int32) {
	v.scoresMu.Lock()
	v.scoresData = make([]PlayerScore, 0)
	
	aliveSnakes := make(map[int32]bool)
	for _, snake := range state.Snakes {
		if snake.GetState() == pb.GameState_Snake_ALIVE {
			aliveSnakes[snake.GetPlayerId()] = true
		}
	}
	
	if state.Players != nil {
		for _, player := range state.Players.Players {
			if player.GetRole() == pb.NodeRole_VIEWER {
				continue
			}
			
			if !aliveSnakes[player.GetId()] {
				continue
			}
			
			role := ""
			roleRank := 2
			switch player.GetRole() {
			case pb.NodeRole_MASTER:
				role = "[MASTER]"
				roleRank = 0
			case pb.NodeRole_DEPUTY:
				role = "[DEPUTY]"
				roleRank = 1
			case pb.NodeRole_NORMAL:
				role = "[NORMAL]"
				roleRank = 2
			}
			v.scoresData = append(v.scoresData, PlayerScore{
				Name:     player.GetName(),
				Score:    player.GetScore(),
				Role:     role,
				RoleRank: roleRank,
				IsMe:     player.GetId() == myPlayerId,
			})
		}
	}

	sort.Slice(v.scoresData, func(i, j int) bool {
		if v.scoresData[i].RoleRank != v.scoresData[j].RoleRank {
			return v.scoresData[i].RoleRank < v.scoresData[j].RoleRank
		}
		return v.scoresData[i].Score > v.scoresData[j].Score
	})
	
	v.scoresMu.Unlock()
	
	if v.scoresList != nil {
		fyne.Do(func() {
			v.scoresList.Refresh()
		})
	}
}

func (v *View) UpdateGamesList(games []GameListItem) {
	v.gamesMu.Lock()
	v.gamesData = games
	v.gamesMu.Unlock()

	if v.gamesList != nil {
		fyne.Do(func() {
			v.gamesList.Refresh()
		})
	}
}

func (v *View) ShowError(message string) {
	v.mu.Lock()
	if v.dialogShowing {
		v.mu.Unlock()
		return
	}
	v.dialogShowing = true
	v.mu.Unlock()

	fyne.Do(func() {
		d := dialog.NewError(fmt.Errorf(message), v.window)
		d.SetOnClosed(func() {
			v.mu.Lock()
			v.dialogShowing = false
			v.mu.Unlock()
		})
		d.Show()
	})
}

func (v *View) ShowInfo(title, message string) {
	v.mu.Lock()
	if v.dialogShowing {
		v.mu.Unlock()
		return
	}
	v.dialogShowing = true
	v.mu.Unlock()

	fyne.Do(func() {
		d := dialog.NewInformation(title, message, v.window)
		d.SetOnClosed(func() {
			v.mu.Lock()
			v.dialogShowing = false
			v.mu.Unlock()
		})
		d.Show()
	})
}

func (v *View) BackToMenu() {
	v.mu.Lock()
	v.isInGame = false
	v.gameField = nil
	v.dialogShowing = false
	v.mu.Unlock()
	fyne.Do(func() {
		v.showMainMenu()
	})
}

func (v *View) GetPlayerName() string {
	if v.playerNameEntry != nil {
		return v.playerNameEntry.Text
	}
	return "Player"
}

func (v *View) handleKeyPress(key *fyne.KeyEvent) {
	v.mu.RLock()
	inGame := v.isInGame
	v.mu.RUnlock()

	if !inGame {
		return
	}

	var dir pb.Direction
	switch key.Name {
	case fyne.KeyW, fyne.KeyUp:
		dir = pb.Direction_UP
	case fyne.KeyS, fyne.KeyDown:
		dir = pb.Direction_DOWN
	case fyne.KeyA, fyne.KeyLeft:
		dir = pb.Direction_LEFT
	case fyne.KeyD, fyne.KeyRight:
		dir = pb.Direction_RIGHT
	default:
		return
	}

	v.EventChan <- ViewEvent{
		Type:      EventDirection,
		Direction: dir,
	}
}

func (v *View) Exit() {
	v.window.Close()
	v.app.Quit()
}

func expandSnakeForView(snake *pb.GameState_Snake, width, height int32) []pb.GameState_Coord {
	if len(snake.Points) == 0 {
		return nil
	}

	var cells []pb.GameState_Coord
	
	headX := snake.Points[0].GetX()
	headY := snake.Points[0].GetY()
	cells = append(cells, pb.GameState_Coord{X: model.Int32Ptr(headX), Y: model.Int32Ptr(headY)})

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
			curX = normalizeCoord(curX+stepX, width)
			curY = normalizeCoord(curY+stepY, height)
			cells = append(cells, pb.GameState_Coord{X: model.Int32Ptr(curX), Y: model.Int32Ptr(curY)})
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

func normalizeCoord(x, max int32) int32 {
	x = x % max
	if x < 0 {
		x += max
	}
	return x
}

func min(a, b float32) float32 {
	if a < b {
		return a
	}
	return b
}

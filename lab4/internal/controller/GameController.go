package controller

import (
	"log"
	"net"
	"sync"

	pb "lab4/internal/config"
	"lab4/internal/network"
	"lab4/internal/node"
	"lab4/internal/view"
)

type Controller struct {
	view       *view.View
	masterNode *node.MasterNode
	normalNode *node.NormalNode

	myPlayerId int32
	isMaster   bool
	isRunning  bool

	mu       sync.RWMutex
	stopChan chan struct{}
}

func NewController() *Controller {
	return &Controller{
		stopChan: make(chan struct{}),
	}
}

func (c *Controller) Start() {
	c.view = view.NewView()
	c.view.Init()

	var err error
	c.normalNode, err = node.NewNormalNode("Player")
	if err != nil {
		log.Printf("Failed to create normal node: %v", err)
	} else {
		c.setupNormalNodeCallbacks()
		c.normalNode.Start()
	}

	go c.eventLoop()
	c.view.StartWindow()
}

func (c *Controller) setupNormalNodeCallbacks() {
	c.normalNode.SetOnGamesUpdate(func(games map[string]*node.GameInfo) {
		gamesList := make([]view.GameListItem, 0)
		for _, g := range games {
			gamesList = append(gamesList, view.GameListItem{
				Name:       g.GameName,
				Players:    len(g.Players.Players),
				CanJoin:    g.CanJoin,
				Width:      g.Config.GetWidth(),
				Height:     g.Config.GetHeight(),
				FoodStatic: g.Config.GetFoodStatic(),
				DelayMs:    g.Config.GetStateDelayMs(),
			})
		}
		c.view.UpdateGamesList(gamesList)
	})

	c.normalNode.SetOnStateUpdate(func(state *pb.GameState) {
		c.view.RenderState(state, c.normalNode.GetPlayerId())
	})

	c.normalNode.SetOnJoinSuccess(func(playerId int32) {
		c.mu.Lock()
		c.myPlayerId = playerId
		c.isRunning = true
		c.mu.Unlock()

		config := c.normalNode.GetConfig()
		c.view.ShowGameScreen(config.GetWidth(), config.GetHeight())
	})

	c.normalNode.SetOnJoinError(func(errMsg string) {
		c.view.ShowError(errMsg)
	})

	c.normalNode.SetOnDisconnect(func() {
		c.mu.Lock()
		if !c.isRunning {
			c.mu.Unlock()
			return 
		}
		c.isRunning = false
		c.mu.Unlock()
		c.view.ShowInfo("Отключение", "Соединение с сервером потеряно")
		c.view.BackToMenu()
	})

	c.normalNode.SetOnBecomeMaster(func(state *pb.GameState, config *pb.GameConfig, nm *network.NetworkManager) {
		c.promoteToMaster(state, config, nm)
	})
}

func (c *Controller) eventLoop() {
	for {
		select {
		case <-c.stopChan:
			return
		case event := <-c.view.EventChan:
			c.handleEvent(event)
		}
	}
}

func (c *Controller) handleEvent(event view.ViewEvent) {
	switch event.Type {
	case view.EventNewGame:
		c.startNewGame(event.Config, event.GameName)
	case view.EventJoinGame:
		c.joinGame(event.GameName, event.AsViewer, event.PlayerName)
	case view.EventDirection:
		c.setDirection(event.Direction)
	case view.EventLeaveGame:
		c.leaveGame()
	case view.EventExit:
		c.exit()
	}
}

func (c *Controller) startNewGame(config *pb.GameConfig, gameName string) {
	if c.normalNode != nil {
		c.normalNode.Stop()
		c.normalNode = nil
	}

	playerName := c.view.GetPlayerName()
	if playerName == "" {
		playerName = "Player"
	}

	var err error
	c.masterNode, err = node.NewMasterNode(config, gameName, playerName)
	if err != nil {
		c.view.ShowError("Не удалось создать игру: " + err.Error())
		c.normalNode, _ = node.NewNormalNode(playerName)
		if c.normalNode != nil {
			c.setupNormalNodeCallbacks()
			c.normalNode.Start()
		}
		return
	}

	c.masterNode.SetOnStateUpdate(func(state *pb.GameState) {
		c.view.RenderState(state, c.masterNode.GetPlayerId())
	})

	c.masterNode.SetOnGameOver(func() {
		go func() {
			c.view.ShowInfo("Игра окончена", "Все змейки погибли!")
			c.leaveGame()
		}()
	})
	
	c.masterNode.SetOnBecomeViewer(func(state *pb.GameState, config *pb.GameConfig, newMasterAddr *net.UDPAddr) {
		log.Printf("onBecomeViewer callback: switching to new master at %v", newMasterAddr)
		
		c.mu.Lock()
		oldMaster := c.masterNode
		playerId := c.myPlayerId
		c.masterNode = nil
		c.isMaster = false
		c.mu.Unlock()
		
		log.Printf("onBecomeViewer: stopping old master")
		if oldMaster != nil {
			oldMaster.Stop()
		}
		log.Printf("onBecomeViewer: old master stopped")
		
		log.Printf("onBecomeViewer: creating viewer node")
		nn, err := node.NewNormalNodeAsViewer(playerName, state, config, newMasterAddr, playerId)
		if err != nil {
			log.Printf("Failed to create viewer node: %v", err)
			c.leaveGame()
			return
		}
		log.Printf("onBecomeViewer: viewer node created")
		
		c.mu.Lock()
		c.normalNode = nn
		c.mu.Unlock()
		
		c.setupNormalNodeCallbacks()
		nn.Start()
		
		log.Printf("onBecomeViewer: Successfully switched to VIEWER role")
	})

	c.masterNode.Start()

	c.mu.Lock()
	c.myPlayerId = c.masterNode.GetPlayerId()
	c.isMaster = true
	c.isRunning = true
	c.mu.Unlock()

	c.view.ShowGameScreen(config.GetWidth(), config.GetHeight())
	log.Printf("Started new game: %s", gameName)
}

func (c *Controller) joinGame(gameName string, asViewer bool, playerName string) {
	if c.normalNode == nil {
		c.view.ShowError("Сетевой модуль не инициализирован")
		return
	}

	if playerName == "" {
		playerName = "Player"
	}
	c.normalNode.SetPlayerName(playerName)

	err := c.normalNode.JoinGame(gameName, asViewer)
	if err != nil {
		c.view.ShowError("Не удалось присоединиться: " + err.Error())
		return
	}

	c.mu.Lock()
	c.isMaster = false
	c.mu.Unlock()
}

func (c *Controller) setDirection(dir pb.Direction) {
	c.mu.RLock()
	isMaster := c.isMaster
	isRunning := c.isRunning
	c.mu.RUnlock()

	if !isRunning {
		return
	}

	if isMaster && c.masterNode != nil {
		c.masterNode.SetDirection(dir)
	} else if c.normalNode != nil {
		c.normalNode.SetDirection(dir)
	}
}

func (c *Controller) leaveGame() {
	c.mu.Lock()
	c.isRunning = false
	c.myPlayerId = 0
	isMaster := c.isMaster
	c.mu.Unlock()

	if isMaster && c.masterNode != nil {
		c.masterNode.Stop()
		c.masterNode = nil
	}

	playerName := c.view.GetPlayerName()
	if c.normalNode != nil {
		c.normalNode.Stop()
		c.normalNode = nil
	}

	var err error
	c.normalNode, err = node.NewNormalNode(playerName)
	if err != nil {
		log.Printf("Failed to restart normal node: %v", err)
	} else {
		c.setupNormalNodeCallbacks()
		c.normalNode.Start()
	}

	c.mu.Lock()
	c.isMaster = false
	c.mu.Unlock()

	c.view.BackToMenu()
}

func (c *Controller) promoteToMaster(state *pb.GameState, config *pb.GameConfig, nm *network.NetworkManager) {
	log.Printf("promoteToMaster: starting with port %d", nm.GetLocalPort())
	
	if state == nil || config == nil {
		log.Printf("Cannot promote to master: no state or config")
		c.view.ShowError("Не удалось стать мастером: нет состояния игры")
		c.leaveGame()
		return
	}

	playerId := c.normalNode.GetPlayerId()
	playerName := c.view.GetPlayerName()
	
	log.Printf("promoteToMaster: stopping NormalNode")
	c.normalNode.StopWithoutNetwork()
	c.normalNode = nil
	log.Printf("promoteToMaster: NormalNode stopped")

	log.Printf("promoteToMaster: creating MasterNode")
	var err error
	c.masterNode, err = node.NewMasterNodeFromState(config, state, playerName, playerId, nm)
	if err != nil {
		log.Printf("Failed to create master node from state: %v", err)
		c.view.ShowError("Не удалось стать мастером: " + err.Error())
		nm.Stop() 
		c.leaveGame()
		return
	}
	log.Printf("promoteToMaster: MasterNode created")

	masterNode := c.masterNode 
	c.masterNode.SetOnStateUpdate(func(state *pb.GameState) {
		c.view.RenderState(state, masterNode.GetPlayerId())
	})

	c.masterNode.SetOnGameOver(func() {
		go func() {
			c.view.ShowInfo("Игра окончена", "Все змейки погибли!")
			c.leaveGame()
		}()
	})
	
	c.masterNode.SetOnBecomeViewer(func(state *pb.GameState, config *pb.GameConfig, newMasterAddr *net.UDPAddr) {
		log.Printf("promoteToMaster.onBecomeViewer: switching to new master at %v", newMasterAddr)
		
		c.mu.Lock()
		oldMaster := c.masterNode
		myPlayerId := c.myPlayerId
		c.masterNode = nil
		c.isMaster = false
		c.mu.Unlock()
		
		log.Printf("promoteToMaster.onBecomeViewer: stopping old master")
		if oldMaster != nil {
			oldMaster.Stop()
		}
		log.Printf("promoteToMaster.onBecomeViewer: old master stopped")
		
		log.Printf("promoteToMaster.onBecomeViewer: creating viewer node")
		nn, err := node.NewNormalNodeAsViewer(playerName, state, config, newMasterAddr, myPlayerId)
		if err != nil {
			log.Printf("Failed to create viewer node: %v", err)
			c.leaveGame()
			return
		}
		log.Printf("promoteToMaster.onBecomeViewer: viewer node created")
		
		c.mu.Lock()
		c.normalNode = nn
		c.mu.Unlock()
		
		c.setupNormalNodeCallbacks()
		nn.Start()
		
		log.Printf("promoteToMaster.onBecomeViewer: Successfully switched to VIEWER role")
	})

	log.Printf("promoteToMaster: starting MasterNode")
	c.masterNode.Start()

	c.mu.Lock()
	c.myPlayerId = playerId
	c.isMaster = true
	c.mu.Unlock()

	log.Printf("promoteToMaster: Successfully promoted to MASTER on port %d", nm.GetLocalPort())
}

func (c *Controller) exit() {
	if c.masterNode != nil {
		c.masterNode.Stop()
	}
	if c.normalNode != nil {
		c.normalNode.Stop()
	}
	close(c.stopChan)
	c.view.Exit()
}
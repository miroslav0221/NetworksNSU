package node

import (
	"fmt"
	"log"
	"net"
	"strings"
	"sync"
	"time"

	pb "lab4/internal/config"
	"lab4/internal/model"
	"lab4/internal/network"
	"google.golang.org/protobuf/proto"
)

const (
	TimeOutGame = 3
	TimePingCheck = 100
	DefaultDelay = 1000
)

type GameInfo struct {
	GameName   string
	Config     *pb.GameConfig
	Players    *pb.GamePlayers
	CanJoin    bool
	MasterAddr *net.UDPAddr
	LastSeen   time.Time
}

type NormalNode struct {
	network *network.NetworkManager

	myPlayerId int32
	myRole     pb.NodeRole
	playerName string

	masterAddr *net.UDPAddr
	deputyAddr *net.UDPAddr

	state      *pb.GameState
	stateMu    sync.RWMutex
	stateOrder int32

	config *pb.GameConfig

	availableGames map[string]*GameInfo
	gamesMu        sync.RWMutex

	stopChan chan struct{}
	stopped  bool
	stoppedMu sync.Mutex
	wg       sync.WaitGroup

	becomingMaster   bool
	becomingMasterMu sync.Mutex

	onStateUpdate  func(*pb.GameState)
	onGamesUpdate  func(map[string]*GameInfo)
	onJoinSuccess  func(int32)
	onJoinError    func(string)
	onDisconnect   func()
	onBecomeMaster func(*pb.GameState, *pb.GameConfig, *network.NetworkManager)
}

func NewNormalNode(playerName string) (*NormalNode, error) {
	nm, err := network.NewNetworkManager(DefaultDelay)
	if err != nil {
		return nil, fmt.Errorf("failed to create network manager: %w", err)
	}

	nn := &NormalNode{
		network:        nm,
		myPlayerId:     0,
		myRole:         pb.NodeRole_NORMAL,
		playerName:     playerName,
		availableGames: make(map[string]*GameInfo),
		stopChan:       make(chan struct{}),
	}

	return nn, nil
}

func NewNormalNodeAsViewer(playerName string, state *pb.GameState, config *pb.GameConfig, masterAddr *net.UDPAddr, playerId int32) (*NormalNode, error) {
	nm, err := network.NewNetworkManager(config.GetStateDelayMs())
	if err != nil {
		return nil, fmt.Errorf("failed to create network manager: %w", err)
	}

	nn := &NormalNode{
		network:        nm,
		myPlayerId:     playerId,
		myRole:         pb.NodeRole_VIEWER,
		playerName:     playerName,
		masterAddr:     masterAddr,
		state:          state,
		config:         config,
		availableGames: make(map[string]*GameInfo),
		stopChan:       make(chan struct{}),
	}

	log.Printf("Created NormalNode as VIEWER connected to master at %v, playerId=%d", masterAddr, playerId)
	return nn, nil
}

func (nn *NormalNode) Start() {
	log.Printf("NormalNode.Start() role=%v playerId=%d masterAddr=%v", nn.myRole, nn.myPlayerId, nn.masterAddr)
	nn.network.Start()

	nn.wg.Add(2)
	go nn.messageHandler()
	go nn.pingLoop()
}

func (nn *NormalNode) Stop() {
	nn.stoppedMu.Lock()
	if nn.stopped {
		nn.stoppedMu.Unlock()
		return
	}
	nn.stopped = true
	nn.stoppedMu.Unlock()

	if nn.masterAddr != nil && nn.myPlayerId != 0 {
		leaveMsg := &pb.GameMessage{
			MsgSeq:     model.Int64Ptr(nn.network.NextMsgSeq()),
			SenderId:   model.Int32Ptr(nn.myPlayerId),
			ReceiverId: model.Int32Ptr(0),
			Type: &pb.GameMessage_RoleChange{
				RoleChange: &pb.GameMessage_RoleChangeMsg{
					SenderRole: pb.NodeRole_VIEWER.Enum(),
				},
			},
		}
		nn.network.SendMessage(leaveMsg, nn.masterAddr)
	}

	close(nn.stopChan)
	nn.network.Stop()
	nn.wg.Wait()
}

func (nn *NormalNode) StopWithoutNetwork() {
	nn.stoppedMu.Lock()
	if nn.stopped {
		nn.stoppedMu.Unlock()
		log.Printf("NormalNode.StopWithoutNetwork: already stopped")
		return
	}
	nn.stopped = true
	nn.stoppedMu.Unlock()

	log.Printf("NormalNode.StopWithoutNetwork: closing stopChan")
	close(nn.stopChan)
	log.Printf("NormalNode.StopWithoutNetwork: waiting for goroutines")
	nn.wg.Wait()
	log.Printf("NormalNode.StopWithoutNetwork: done")
}

func (nn *NormalNode) GetNetwork() *network.NetworkManager {
	return nn.network
}

func (nn *NormalNode) SetOnStateUpdate(callback func(*pb.GameState)) {
	nn.onStateUpdate = callback
}

func (nn *NormalNode) SetOnGamesUpdate(callback func(map[string]*GameInfo)) {
	nn.onGamesUpdate = callback
}

func (nn *NormalNode) SetOnJoinSuccess(callback func(int32)) {
	nn.onJoinSuccess = callback
}

func (nn *NormalNode) SetOnJoinError(callback func(string)) {
	nn.onJoinError = callback
}

func (nn *NormalNode) SetOnDisconnect(callback func()) {
	nn.onDisconnect = callback
}

func (nn *NormalNode) SetOnBecomeMaster(callback func(*pb.GameState, *pb.GameConfig, *network.NetworkManager)) {
	nn.onBecomeMaster = callback
}

func (nn *NormalNode) SetPlayerName(name string) {
	nn.playerName = name
}

func (nn *NormalNode) GetAvailableGames() map[string]*GameInfo {
	nn.gamesMu.RLock()
	defer nn.gamesMu.RUnlock()

	result := make(map[string]*GameInfo)
	for k, v := range nn.availableGames {
		result[k] = v
	}
	return result
}

func (nn *NormalNode) GetState() *pb.GameState {
	nn.stateMu.RLock()
	defer nn.stateMu.RUnlock()
	return nn.state
}

func (nn *NormalNode) GetConfig() *pb.GameConfig {
	return nn.config
}

func (nn *NormalNode) GetPlayerId() int32 {
	return nn.myPlayerId
}

func (nn *NormalNode) GetRole() pb.NodeRole {
	return nn.myRole
}

func (nn *NormalNode) cleanupOldGames() {
	nn.gamesMu.Lock()
	defer nn.gamesMu.Unlock()
	
	now := time.Now()
	changed := false
	for name, info := range nn.availableGames {
		if now.Sub(info.LastSeen) > TimeOutGame*time.Second {
			delete(nn.availableGames, name)
			changed = true
			log.Printf("Removed stale game from list: %s", name)
		}
	}
	
	if changed && nn.onGamesUpdate != nil {
		gamesCopy := make(map[string]*GameInfo)
		for k, v := range nn.availableGames {
			gamesCopy[k] = v
		}
		go nn.onGamesUpdate(gamesCopy)
	}
}

func (nn *NormalNode) GetLocalPort() int {
	return nn.network.GetLocalPort()
}

func (nn *NormalNode) JoinGame(gameName string, asViewer bool) error {
	log.Printf("JoinGame called: gameName=%s, asViewer=%v, myPlayerId=%d", gameName, asViewer, nn.myPlayerId)
	
	nn.gamesMu.RLock()
	gameInfo, exists := nn.availableGames[gameName]
	nn.gamesMu.RUnlock()

	if !exists {
		log.Printf("JoinGame: game not found: %s", gameName)
		return fmt.Errorf("game not found: %s", gameName)
	}

	if !asViewer && !gameInfo.CanJoin {
		log.Printf("JoinGame: game is full: %s", gameName)
		return fmt.Errorf("game is full")
	}

	nn.masterAddr = gameInfo.MasterAddr
	nn.config = gameInfo.Config
	nn.network.SetStateDelayMs(gameInfo.Config.GetStateDelayMs())
	
	log.Printf("JoinGame: connecting to master at %v", nn.masterAddr)

	requestedRole := pb.NodeRole_NORMAL
	if asViewer {
		requestedRole = pb.NodeRole_VIEWER
	}

	joinMsg := &pb.GameMessage{
		MsgSeq: model.Int64Ptr(nn.network.NextMsgSeq()),
		Type: &pb.GameMessage_Join{
			Join: &pb.GameMessage_JoinMsg{
				PlayerType:    pb.PlayerType_HUMAN.Enum(),
				PlayerName:    model.StringPtr(nn.playerName),
				GameName:      model.StringPtr(gameName),
				RequestedRole: requestedRole.Enum(),
			},
		},
	}

	return nn.network.SendMessageWithAck(joinMsg, nn.masterAddr)
}

func (nn *NormalNode) SetDirection(dir pb.Direction) {
	if nn.masterAddr == nil || nn.myPlayerId == 0 {
		return
	}

	steerMsg := &pb.GameMessage{
		MsgSeq:   model.Int64Ptr(nn.network.NextMsgSeq()),
		SenderId: model.Int32Ptr(nn.myPlayerId),
		Type: &pb.GameMessage_Steer{
			Steer: &pb.GameMessage_SteerMsg{
				Direction: dir.Enum(),
			},
		},
	}

	nn.network.SendMessageWithAck(steerMsg, nn.masterAddr)
}

func (nn *NormalNode) messageHandler() {
	defer nn.wg.Done()

	for {
		select {
		case <-nn.stopChan:
			return
		case received := <-nn.network.GetReceiveChan():
			if received == nil {
				continue
			}
			nn.handleMessage(received.Message, received.Addr)
			
			nn.becomingMasterMu.Lock()
			if nn.becomingMaster {
				nn.becomingMasterMu.Unlock()
				log.Printf("messageHandler: becomingMaster flag set after handling message, exiting")
				return
			}
			nn.becomingMasterMu.Unlock()
		}
	}
}

func (nn *NormalNode) handleMessage(msg *pb.GameMessage, addr *net.UDPAddr) {
	nn.becomingMasterMu.Lock()
	if nn.becomingMaster {
		nn.becomingMasterMu.Unlock()
		return 
	}
	nn.becomingMasterMu.Unlock()

	switch t := msg.Type.(type) {
	case *pb.GameMessage_Announcement:
		nn.handleAnnouncement(t.Announcement, addr)
	case *pb.GameMessage_State:
		nn.handleState(msg, t.State, addr)
	case *pb.GameMessage_Ack:
		nn.handleAck(msg)
	case *pb.GameMessage_Error:
		nn.handleError(t.Error)
	case *pb.GameMessage_RoleChange:
		nn.handleRoleChange(msg, t.RoleChange, addr)
	case *pb.GameMessage_Ping:
		nn.handlePing(msg, addr)
	}
}

func (nn *NormalNode) handleAnnouncement(announcement *pb.GameMessage_AnnouncementMsg, addr *net.UDPAddr) {
	nn.gamesMu.Lock()

	for _, game := range announcement.Games {
		nn.availableGames[game.GetGameName()] = &GameInfo{
			GameName:   game.GetGameName(),
			Config:     game.Config,
			Players:    game.Players,
			CanJoin:    game.GetCanJoin(),
			MasterAddr: addr,
			LastSeen:   time.Now(),
		}
	}

	now := time.Now()
	for name, info := range nn.availableGames {
		if now.Sub(info.LastSeen) > 3*time.Second {
			delete(nn.availableGames, name)
		}
	}

	nn.gamesMu.Unlock()

	if nn.onGamesUpdate != nil {
		nn.onGamesUpdate(nn.GetAvailableGames())
	}
}

func (nn *NormalNode) handleState(msg *pb.GameMessage, stateMsg *pb.GameMessage_StateMsg, addr *net.UDPAddr) {
	state := stateMsg.State

	snakeInfo := make([]string, 0)
	for _, s := range state.Snakes {
		snakeInfo = append(snakeInfo, fmt.Sprintf("p%d:%s:len%d", s.GetPlayerId(), s.GetState().String(), len(s.Points)))
	}
	playerInfo := make([]string, 0)
	if state.Players != nil {
		for _, p := range state.Players.Players {
			playerInfo = append(playerInfo, fmt.Sprintf("p%d:%s", p.GetId(), p.GetRole().String()))
		}
	}
	log.Printf("[RECV STATE] myId=%d myRole=%v order=%d snakes=[%s] players=[%s]", 
		nn.myPlayerId, nn.myRole, state.GetStateOrder(), strings.Join(snakeInfo, ","), strings.Join(playerInfo, ","))

	nn.stateMu.Lock()
	if state.GetStateOrder() > nn.stateOrder {
		nn.state = state
		nn.stateOrder = state.GetStateOrder()

		if state.Players != nil {
			for _, p := range state.Players.Players {
				if p.GetId() == nn.myPlayerId {
					newRole := p.GetRole()
					if newRole != nn.myRole {
						log.Printf("[RECV STATE] Role updated from state: %v -> %v", nn.myRole, newRole)
						nn.myRole = newRole
					}
					break
				}
			}

			for _, p := range state.Players.Players {
				if p.GetRole() == pb.NodeRole_DEPUTY && p.GetId() != nn.myPlayerId {
					if p.GetIpAddress() != "" {
						deputyAddr, err := net.ResolveUDPAddr("udp", fmt.Sprintf("%s:%d", p.GetIpAddress(), p.GetPort()))
						if err == nil {
							nn.deputyAddr = deputyAddr
						}
					}
					break
				}
			}
		}
	} else {
		log.Printf("[RECV STATE] Ignoring old state order=%d (current=%d)", state.GetStateOrder(), nn.stateOrder)
	}
	nn.stateMu.Unlock()

	ackMsg := &pb.GameMessage{
		MsgSeq:     model.Int64Ptr(msg.GetMsgSeq()),
		SenderId:   model.Int32Ptr(nn.myPlayerId),
		ReceiverId: model.Int32Ptr(msg.GetSenderId()),
		Type: &pb.GameMessage_Ack{
			Ack: &pb.GameMessage_AckMsg{},
		},
	}
	nn.network.SendMessage(ackMsg, addr)

	if nn.onStateUpdate != nil {
		nn.onStateUpdate(nn.GetState())
	}
}

func (nn *NormalNode) handleAck(msg *pb.GameMessage) {
	nn.network.AcknowledgeMessage(msg.GetMsgSeq())

	if nn.myPlayerId == 0 && msg.GetReceiverId() != 0 {
		nn.myPlayerId = msg.GetReceiverId()
		log.Printf("Joined game with player ID: %d", nn.myPlayerId)

		if nn.onJoinSuccess != nil {
			nn.onJoinSuccess(nn.myPlayerId)
		}
	}
}

func (nn *NormalNode) handleError(errorMsg *pb.GameMessage_ErrorMsg) {
	log.Printf("Error from server: %s", errorMsg.GetErrorMessage())

	if nn.onJoinError != nil {
		nn.onJoinError(errorMsg.GetErrorMessage())
	}
}

func (nn *NormalNode) handleRoleChange(msg *pb.GameMessage, roleChange *pb.GameMessage_RoleChangeMsg, addr *net.UDPAddr) {
	if roleChange.GetSenderRole() == pb.NodeRole_VIEWER && roleChange.GetReceiverRole() == pb.NodeRole_VIEWER {
		log.Printf("Received game over notification from master")
		ackMsg := &pb.GameMessage{
			MsgSeq:     model.Int64Ptr(msg.GetMsgSeq()),
			SenderId:   model.Int32Ptr(nn.myPlayerId),
			ReceiverId: model.Int32Ptr(msg.GetSenderId()),
			Type: &pb.GameMessage_Ack{
				Ack: &pb.GameMessage_AckMsg{},
			},
		}
		nn.network.SendMessage(ackMsg, addr)

		nn.stoppedMu.Lock()
		if nn.stopped {
			nn.stoppedMu.Unlock()
			return
		}
		nn.stopped = true
		nn.stoppedMu.Unlock()
		
		if nn.onDisconnect != nil {
			go nn.onDisconnect()
		}
		return
	}

	if roleChange.GetSenderRole() == pb.NodeRole_VIEWER && roleChange.ReceiverRole == nil {
		log.Printf("Old master is exiting, switching to deputy")
		if nn.deputyAddr != nil {
			nn.masterAddr = nn.deputyAddr
			nn.deputyAddr = nil
			log.Printf("Switched to new master at: %v", nn.masterAddr)
		}
	}

	if roleChange.GetSenderRole() == pb.NodeRole_MASTER {
		nn.masterAddr = addr
		senderId := msg.GetSenderId()
		log.Printf("New master %d at: %v", senderId, addr)

		nn.stateMu.Lock()
		if nn.state != nil && nn.state.Players != nil {
			for _, p := range nn.state.Players.Players {
				if p.GetId() == senderId {
					p.Role = pb.NodeRole_MASTER.Enum()
				}
			}
		}
		nn.stateMu.Unlock()
	}

	if roleChange.ReceiverRole != nil {
		oldRole := nn.myRole
		nn.myRole = roleChange.GetReceiverRole()
		log.Printf("Role changed: %v -> %v", oldRole, nn.myRole)

		nn.stateMu.Lock()
		if nn.state != nil && nn.state.Players != nil {
			for _, p := range nn.state.Players.Players {
				if p.GetId() == nn.myPlayerId {
					p.Role = nn.myRole.Enum()
				}
			}
		}
		nn.stateMu.Unlock()

		if nn.myRole == pb.NodeRole_MASTER {
			log.Printf("Promoted to MASTER - starting master responsibilities")
			nn.becomingMasterMu.Lock()
			nn.becomingMaster = true
			nn.becomingMasterMu.Unlock()
			
			ackMsg := &pb.GameMessage{
				MsgSeq:     model.Int64Ptr(msg.GetMsgSeq()),
				SenderId:   model.Int32Ptr(nn.myPlayerId),
				ReceiverId: model.Int32Ptr(msg.GetSenderId()),
				Type: &pb.GameMessage_Ack{
					Ack: &pb.GameMessage_AckMsg{},
				},
			}
			nn.network.SendMessage(ackMsg, addr)
			

			if nn.onBecomeMaster != nil {
				nn.stateMu.RLock()
				stateCopy := proto.Clone(nn.state).(*pb.GameState)
				nn.stateMu.RUnlock()
				log.Printf("Passing network manager (port %d) to new master", nn.network.GetLocalPort())
				go nn.onBecomeMaster(stateCopy, nn.config, nn.network)
			}
			return 
		}

		if nn.myRole == pb.NodeRole_DEPUTY {
			log.Printf("Assigned as DEPUTY")
		}

		if nn.onStateUpdate != nil {
			nn.stateMu.RLock()
			state := nn.state
			nn.stateMu.RUnlock()
			if state != nil {
				nn.onStateUpdate(state)
			}
		}
	}

	ackMsg := &pb.GameMessage{
		MsgSeq:     model.Int64Ptr(msg.GetMsgSeq()),
		SenderId:   model.Int32Ptr(nn.myPlayerId),
		ReceiverId: model.Int32Ptr(msg.GetSenderId()),
		Type: &pb.GameMessage_Ack{
			Ack: &pb.GameMessage_AckMsg{},
		},
	}
	nn.network.SendMessage(ackMsg, addr)
}

func (nn *NormalNode) handlePing(msg *pb.GameMessage, addr *net.UDPAddr) {
	ackMsg := &pb.GameMessage{
		MsgSeq:     model.Int64Ptr(msg.GetMsgSeq()),
		SenderId:   model.Int32Ptr(nn.myPlayerId),
		ReceiverId: model.Int32Ptr(msg.GetSenderId()),
		Type: &pb.GameMessage_Ack{
			Ack: &pb.GameMessage_AckMsg{},
		},
	}
	nn.network.SendMessage(ackMsg, addr)
}

func (nn *NormalNode) pingLoop() {
	defer nn.wg.Done()

	ticker := time.NewTicker(TimePingCheck * time.Millisecond)
	defer ticker.Stop()

	lastPingSent := time.Now()

	for {
		select {
		case <-nn.stopChan:
			return
		case <-ticker.C:
			nn.becomingMasterMu.Lock()
			if nn.becomingMaster {
				nn.becomingMasterMu.Unlock()
				continue
			}
			nn.becomingMasterMu.Unlock()

			nn.cleanupOldGames()

			if nn.masterAddr == nil || nn.myPlayerId == 0 {
				continue
			}

			if nn.config != nil {
				pingInterval := time.Duration(nn.config.GetStateDelayMs()/10) * time.Millisecond
				if time.Since(lastPingSent) > pingInterval {
					pingMsg := &pb.GameMessage{
						MsgSeq:   model.Int64Ptr(nn.network.NextMsgSeq()),
						SenderId: model.Int32Ptr(nn.myPlayerId),
						Type: &pb.GameMessage_Ping{
							Ping: &pb.GameMessage_PingMsg{},
						},
					}
					log.Printf("[PING] Sending ping from player %d (role=%v) to master at %v", nn.myPlayerId, nn.myRole, nn.masterAddr)
					nn.network.SendMessageWithAck(pingMsg, nn.masterAddr)
					lastPingSent = time.Now()
				}
			}

			if nn.config != nil {
				timeout := time.Duration(float64(nn.config.GetStateDelayMs())*0.8) * time.Millisecond 
				timedOut := nn.network.GetTimedOutNodes(timeout)

				for _, addrStr := range timedOut {
					if nn.masterAddr != nil && nn.masterAddr.String() == addrStr {
						log.Printf("Master timed out, switching to deputy")
						nn.switchToDeputy()
						break
					}
				}
			}
		}
	}
}

func (nn *NormalNode) switchToDeputy() {
	if nn.myRole == pb.NodeRole_DEPUTY {
		log.Printf("I am DEPUTY and MASTER timed out, becoming MASTER")
		
		nn.becomingMasterMu.Lock()
		nn.becomingMaster = true
		nn.becomingMasterMu.Unlock()
		
		if nn.onBecomeMaster != nil {
			nn.stateMu.RLock()
			stateCopy := proto.Clone(nn.state).(*pb.GameState)
			nn.stateMu.RUnlock()
			log.Printf("Passing network manager (port %d) to new master", nn.network.GetLocalPort())
			go nn.onBecomeMaster(stateCopy, nn.config, nn.network)
		}
		return
	}

	if nn.deputyAddr == nil {
		log.Printf("No deputy available, disconnecting")
		if nn.onDisconnect != nil {
			nn.onDisconnect()
		}
		return
	}

	nn.network.GetPendingMessagesForAddr(nn.masterAddr, nn.deputyAddr)
	nn.masterAddr = nn.deputyAddr
	nn.deputyAddr = nil

	log.Printf("Switched to deputy at: %v", nn.masterAddr)
}
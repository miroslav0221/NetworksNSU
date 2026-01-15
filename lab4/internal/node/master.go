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
)

const (
	TimeOutKoef = 0.8
)

type MasterNode struct {
	engine *model.GameEngine
	network *network.NetworkManager

	myPlayerId int32
	gameName   string

	pendingTurns map[int32]pb.Direction
	pendingMu    sync.Mutex

	playerAddrs   map[int32]*net.UDPAddr
	playerAddrsMu sync.RWMutex

	deputyId int32

	stopChan   chan struct{}
	stopped    bool
	stoppedMu  sync.Mutex
	wg         sync.WaitGroup

	onStateUpdate    func(*pb.GameState)
	onGameOver       func() 
	onBecomeViewer   func(*pb.GameState, *pb.GameConfig, *net.UDPAddr) 
	gameOverCalled   bool
	mySnakeDead      bool
	isTransferring   bool  
	roleTransferred  bool   
}

func NewMasterNode(config *pb.GameConfig, gameName, playerName string) (*MasterNode, error) {
	nm, err := network.NewNetworkManager(config.GetStateDelayMs())
	if err != nil {
		return nil, fmt.Errorf("failed to create network manager: %w", err)
	}

	engine := model.NewGameEngine(config, gameName)

	mn := &MasterNode{
		engine:       engine,
		network:      nm,
		gameName:     gameName,
		pendingTurns: make(map[int32]pb.Direction),
		playerAddrs:  make(map[int32]*net.UDPAddr),
		deputyId:     0,
		stopChan:     make(chan struct{}),
	}

	mn.myPlayerId = engine.AddPlayer(playerName, pb.NodeRole_MASTER, "", int32(nm.GetLocalPort()))

	if !engine.CreateSnakeForPlayer(mn.myPlayerId) {
		nm.Stop()
		return nil, fmt.Errorf("failed to create snake for master")
	}

	return mn, nil
}

func NewMasterNodeFromState(config *pb.GameConfig, state *pb.GameState, playerName string, myPlayerId int32, nm *network.NetworkManager) (*MasterNode, error) {
	log.Printf("NewMasterNodeFromState: reusing network manager on port %d", nm.GetLocalPort())

	gameName := "Game"

	engine := model.NewGameEngineFromState(config, state, gameName)

	var oldMasterId int32 = 0
	for _, player := range state.Players.Players {
		if player.GetRole() == pb.NodeRole_MASTER && player.GetId() != myPlayerId {
			oldMasterId = player.GetId()
			break
		}
	}

	oldMasterSnakeAlive := false
	if oldMasterId != 0 {
		for _, snake := range state.Snakes {
			if snake.GetPlayerId() == oldMasterId && snake.GetState() == pb.GameState_Snake_ALIVE {
				oldMasterSnakeAlive = true
				break
			}
		}
	}

	if oldMasterId != 0 {
		if oldMasterSnakeAlive {
			engine.RemovePlayer(oldMasterId)
			log.Printf("Removed timed out old master %d from players list (snake became ZOMBIE)", oldMasterId)
		} else {
			engine.SetPlayerRole(oldMasterId, pb.NodeRole_VIEWER)
			log.Printf("Old master %d became VIEWER (snake was dead)", oldMasterId)
		}
	}

	engine.SetPlayerRole(myPlayerId, pb.NodeRole_MASTER)

	mn := &MasterNode{
		engine:       engine,
		network:      nm,
		gameName:     gameName,
		myPlayerId:   myPlayerId,
		pendingTurns: make(map[int32]pb.Direction),
		playerAddrs:  make(map[int32]*net.UDPAddr),
		deputyId:     0,
		stopChan:     make(chan struct{}),
	}

	mn.assignNewDeputy()

	for _, player := range engine.GetState().Players.Players {
		if player.GetId() != myPlayerId && player.GetIpAddress() != "" {
			addr, err := net.ResolveUDPAddr("udp", fmt.Sprintf("%s:%d", player.GetIpAddress(), player.GetPort()))
			if err == nil {
				mn.playerAddrs[player.GetId()] = addr
				log.Printf("Restored player %d address: %v", player.GetId(), addr)
			}
		}
	}

	log.Printf("Created MasterNode from state with %d players", len(engine.GetState().Players.Players))
	return mn, nil
}

func (mn *MasterNode) Start() {
	mn.network.Start()

	mn.notifyAllPlayersAboutNewMaster()

	mn.wg.Add(3)
	go mn.gameLoop()
	go mn.announcementLoop()
	go mn.messageHandler()
}

func (mn *MasterNode) notifyAllPlayersAboutNewMaster() {
	mn.playerAddrsMu.RLock()
	defer mn.playerAddrsMu.RUnlock()

	for playerId, addr := range mn.playerAddrs {
		if playerId == mn.myPlayerId {
			continue
		}

		var receiverRole *pb.NodeRole
		if playerId == mn.deputyId {
			receiverRole = pb.NodeRole_DEPUTY.Enum()
		}

		roleChangeMsg := &pb.GameMessage{
			MsgSeq:     model.Int64Ptr(mn.network.NextMsgSeq()),
			SenderId:   model.Int32Ptr(mn.myPlayerId),
			ReceiverId: model.Int32Ptr(playerId),
			Type: &pb.GameMessage_RoleChange{
				RoleChange: &pb.GameMessage_RoleChangeMsg{
					SenderRole:   pb.NodeRole_MASTER.Enum(),
					ReceiverRole: receiverRole,
				},
			},
		}
		mn.network.SendMessageWithAck(roleChangeMsg, addr)
		log.Printf("Notified player %d about new master (me)", playerId)
	}
}

func (mn *MasterNode) Stop() {
	log.Printf("MasterNode.Stop() called")
	mn.stoppedMu.Lock()
	if mn.stopped {
		mn.stoppedMu.Unlock()
		log.Printf("MasterNode.Stop() already stopped, returning")
		return
	}
	mn.stopped = true
	mn.stoppedMu.Unlock()

	if !mn.isTransferring && !mn.roleTransferred {
		mn.isTransferring = true
		log.Printf("MasterNode.Stop() transferring role")
		mn.transferMasterRole()
	}

	log.Printf("MasterNode.Stop() closing stopChan")
	close(mn.stopChan)
	log.Printf("MasterNode.Stop() stopping network")
	mn.network.Stop()
	log.Printf("MasterNode.Stop() waiting for goroutines")
	mn.wg.Wait()
	log.Printf("MasterNode.Stop() done")
}

func (mn *MasterNode) transferMasterRole() {
	mn.playerAddrsMu.RLock()
	defer mn.playerAddrsMu.RUnlock()

	if mn.deputyId != 0 {
		if deputyAddr, ok := mn.playerAddrs[mn.deputyId]; ok {
			mn.engine.SetPlayerRole(mn.myPlayerId, pb.NodeRole_VIEWER)
			mn.engine.SetPlayerRole(mn.deputyId, pb.NodeRole_MASTER)
			
			mn.engine.MakeSnakeZombie(mn.myPlayerId)

			roleChangeMsg := &pb.GameMessage{
				MsgSeq:   model.Int64Ptr(mn.network.NextMsgSeq()),
				SenderId: model.Int32Ptr(mn.myPlayerId),
				Type: &pb.GameMessage_RoleChange{
					RoleChange: &pb.GameMessage_RoleChangeMsg{
						SenderRole:   pb.NodeRole_VIEWER.Enum(),
						ReceiverRole: pb.NodeRole_MASTER.Enum(), 
					},
				},
			}
			mn.network.SendMessage(roleChangeMsg, deputyAddr)
			log.Printf("Transferred MASTER role to deputy %d at %v", mn.deputyId, deputyAddr)

			for playerId, addr := range mn.playerAddrs {
				if playerId == mn.myPlayerId || playerId == mn.deputyId {
					continue
				}

				notifyMsg := &pb.GameMessage{
					MsgSeq:   model.Int64Ptr(mn.network.NextMsgSeq()),
					SenderId: model.Int32Ptr(mn.myPlayerId),
					Type: &pb.GameMessage_RoleChange{
						RoleChange: &pb.GameMessage_RoleChangeMsg{
							SenderRole: pb.NodeRole_VIEWER.Enum(), 
						},
					},
				}
				mn.network.SendMessage(notifyMsg, addr)
			}
			return
		}
	}

	log.Printf("No deputy available, ending game for all players")
	for playerId, addr := range mn.playerAddrs {
		if playerId == mn.myPlayerId {
			continue
		}

		roleChangeMsg := &pb.GameMessage{
			MsgSeq:   model.Int64Ptr(mn.network.NextMsgSeq()),
			SenderId: model.Int32Ptr(mn.myPlayerId),
			Type: &pb.GameMessage_RoleChange{
				RoleChange: &pb.GameMessage_RoleChangeMsg{
					SenderRole:   pb.NodeRole_VIEWER.Enum(),
					ReceiverRole: pb.NodeRole_VIEWER.Enum(),
				},
			},
		}
		mn.network.SendMessage(roleChangeMsg, addr)
		log.Printf("Sent game over notification to player %d", playerId)
	}
}

func (mn *MasterNode) GetEngine() *model.GameEngine {
	return mn.engine
}

func (mn *MasterNode) GetPlayerId() int32 {
	return mn.myPlayerId
}

func (mn *MasterNode) SetOnStateUpdate(callback func(*pb.GameState)) {
	mn.onStateUpdate = callback
}

func (mn *MasterNode) SetOnGameOver(callback func()) {
	mn.onGameOver = callback
}

func (mn *MasterNode) SetOnBecomeViewer(callback func(*pb.GameState, *pb.GameConfig, *net.UDPAddr)) {
	mn.onBecomeViewer = callback
}

func (mn *MasterNode) IsMySnakeDead() bool {
	return mn.mySnakeDead
}

func (mn *MasterNode) SetDirection(dir pb.Direction) {
	mn.pendingMu.Lock()
	mn.pendingTurns[mn.myPlayerId] = dir
	mn.pendingMu.Unlock()
}

func (mn *MasterNode) gameLoop() {
	defer mn.wg.Done()

	log.Printf("GameLoop started with delay %d ms", mn.engine.Config.GetStateDelayMs())

	ticker := time.NewTicker(time.Duration(mn.engine.Config.GetStateDelayMs()) * time.Millisecond)
	defer ticker.Stop()

	for {
		select {
		case <-mn.stopChan:
			log.Printf("GameLoop stopped")
			return
		case <-ticker.C:
			if mn.roleTransferred {
				log.Printf("GameLoop: role transferred, skipping tick")
				continue
			}
			mn.tick()
		}
	}
}

func (mn *MasterNode) tick() {
	alivePlayersBefore := mn.engine.GetAlivePlayerIds()
	aliveBeforeMap := make(map[int32]bool)
	for _, id := range alivePlayersBefore {
		aliveBeforeMap[id] = true
	}

	hadSnakes := mn.engine.HasSnakes()
	state := mn.engine.GetState()
	
	snakeStates := make([]string, 0)
	for _, s := range state.Snakes {
		snakeStates = append(snakeStates, fmt.Sprintf("p%d:%s", s.GetPlayerId(), s.GetState().String()))
	}
	playerRoles := make([]string, 0)
	if state.Players != nil {
		for _, p := range state.Players.Players {
			playerRoles = append(playerRoles, fmt.Sprintf("p%d:%s", p.GetId(), p.GetRole().String()))
		}
	}
	log.Printf("[TICK] StateOrder=%d, Snakes=[%s], Players=[%s]", 
		state.GetStateOrder(), strings.Join(snakeStates, ","), strings.Join(playerRoles, ","))

	mn.pendingMu.Lock()
	for playerId, dir := range mn.pendingTurns {
		mn.engine.SetSnakeDirection(playerId, dir)
	}
	mn.pendingTurns = make(map[int32]pb.Direction)
	mn.pendingMu.Unlock()

	mn.engine.UpdateState()

	alivePlayersAfter := mn.engine.GetAlivePlayerIds()
	aliveAfterMap := make(map[int32]bool)
	for _, id := range alivePlayersAfter {
		aliveAfterMap[id] = true
	}

	masterDied := false
	for _, playerId := range alivePlayersBefore {
		if !aliveAfterMap[playerId] {
			log.Printf("[TICK] Player %d died this tick", playerId)
			if playerId == mn.myPlayerId {
				masterDied = true
				mn.mySnakeDead = true
				log.Printf("[TICK] My snake (MASTER id=%d) died!", mn.myPlayerId)
			} else {
				mn.handlePlayerDeath(playerId)
			}
		}
	}

	mn.broadcastState()

	mn.checkTimeouts()

	if mn.onStateUpdate != nil {
		mn.onStateUpdate(mn.engine.GetState())
	}

	if masterDied {
		log.Printf("[TICK] MASTER died, transferring role to deputy after broadcast")
		mn.transferMasterRoleOnDeath()
	}

	if hadSnakes && !mn.engine.HasAnyAlivePlayers() && mn.onGameOver != nil && !mn.gameOverCalled {
		mn.gameOverCalled = true
		log.Printf("[TICK] Game over! All snakes died.")
		mn.notifyGameOver()

		go func() {
			time.Sleep(500 * time.Millisecond)
			mn.onGameOver()
		}()
	}
}

func (mn *MasterNode) handlePlayerDeath(playerId int32) {
	if playerId == mn.myPlayerId {
		return
	}

	log.Printf("[DEATH] Handling death of player %d (deputy=%d)", playerId, mn.deputyId)

	if playerId == mn.deputyId {
		log.Printf("[DEATH] DEPUTY %d died, assigning new deputy", playerId)
		mn.deputyId = 0
		mn.assignNewDeputy()
	}

	mn.notifyPlayerDeath(playerId)
	mn.engine.SetPlayerRole(playerId, pb.NodeRole_VIEWER)
	log.Printf("[DEATH] Player %d role changed to VIEWER", playerId)
}

func (mn *MasterNode) transferMasterRoleOnDeath() {
	if mn.deputyId == 0 {
		log.Printf("MASTER died but no deputy available - continuing as dead master")
		return
	}

	mn.playerAddrsMu.RLock()
	deputyAddr := mn.playerAddrs[mn.deputyId]
	mn.playerAddrsMu.RUnlock()

	if deputyAddr == nil {
		log.Printf("MASTER died but deputy address not found")
		return
	}

	mn.roleTransferred = true

	roleChangeMsg := &pb.GameMessage{
		MsgSeq:     model.Int64Ptr(mn.network.NextMsgSeq()),
		SenderId:   model.Int32Ptr(mn.myPlayerId),
		ReceiverId: model.Int32Ptr(mn.deputyId),
		Type: &pb.GameMessage_RoleChange{
			RoleChange: &pb.GameMessage_RoleChangeMsg{
				SenderRole:   pb.NodeRole_VIEWER.Enum(), 
				ReceiverRole: pb.NodeRole_MASTER.Enum(),
			},
		},
	}
	mn.network.SendMessageWithAck(roleChangeMsg, deputyAddr)
	log.Printf("MASTER: Sent role transfer to DEPUTY %d", mn.deputyId)

	mn.engine.SetPlayerRole(mn.myPlayerId, pb.NodeRole_VIEWER)
	mn.engine.SetPlayerRole(mn.deputyId, pb.NodeRole_MASTER)

	mn.playerAddrsMu.RLock()
	for playerId, addr := range mn.playerAddrs {
		if playerId == mn.myPlayerId || playerId == mn.deputyId {
			continue
		}
		notifyMsg := &pb.GameMessage{
			MsgSeq:     model.Int64Ptr(mn.network.NextMsgSeq()),
			SenderId:   model.Int32Ptr(mn.myPlayerId),
			ReceiverId: model.Int32Ptr(playerId),
			Type: &pb.GameMessage_RoleChange{
				RoleChange: &pb.GameMessage_RoleChangeMsg{
					SenderRole: pb.NodeRole_VIEWER.Enum(),
				},
			},
		}
		mn.network.SendMessage(notifyMsg, addr)
	}
	deputyAddrCopy := *deputyAddr 
	mn.playerAddrsMu.RUnlock()

	log.Printf("MASTER: Role transferred to DEPUTY %d, I am now VIEWER", mn.deputyId)
	
	if mn.onBecomeViewer != nil {
		log.Printf("MASTER: Calling onBecomeViewer callback to switch to VIEWER role")
		go mn.onBecomeViewer(mn.engine.GetState(), mn.engine.Config, &deputyAddrCopy)
	}
}

func (mn *MasterNode) notifyPlayerDeath(playerId int32) {
	mn.playerAddrsMu.RLock()
	addr := mn.playerAddrs[playerId]
	mn.playerAddrsMu.RUnlock()

	if addr != nil {
		roleChangeMsg := &pb.GameMessage{
			MsgSeq:     model.Int64Ptr(mn.network.NextMsgSeq()),
			SenderId:   model.Int32Ptr(mn.myPlayerId),
			ReceiverId: model.Int32Ptr(playerId),
			Type: &pb.GameMessage_RoleChange{
				RoleChange: &pb.GameMessage_RoleChangeMsg{
					SenderRole:   pb.NodeRole_MASTER.Enum(),
					ReceiverRole: pb.NodeRole_VIEWER.Enum(),
				},
			},
		}
		mn.network.SendMessage(roleChangeMsg, addr)
		log.Printf("Notified player %d that they are now VIEWER (snake died)", playerId)
	}
}

func (mn *MasterNode) notifyGameOver() {
	mn.playerAddrsMu.RLock()
	defer mn.playerAddrsMu.RUnlock()

	for playerId, addr := range mn.playerAddrs {
		if playerId == mn.myPlayerId {
			continue
		}

		roleChangeMsg := &pb.GameMessage{
			MsgSeq:     model.Int64Ptr(mn.network.NextMsgSeq()),
			SenderId:   model.Int32Ptr(mn.myPlayerId),
			ReceiverId: model.Int32Ptr(playerId),
			Type: &pb.GameMessage_RoleChange{
				RoleChange: &pb.GameMessage_RoleChangeMsg{
					SenderRole:   pb.NodeRole_VIEWER.Enum(),
					ReceiverRole: pb.NodeRole_VIEWER.Enum(),
				},
			},
		}
		mn.network.SendMessageWithAck(roleChangeMsg, addr)
		log.Printf("Sent game over notification to player %d", playerId)
	}
}

func (mn *MasterNode) broadcastState() {
	state := mn.engine.GetStateCopy()

	snakeInfo := make([]string, 0)
	for _, s := range state.Snakes {
		snakeInfo = append(snakeInfo, fmt.Sprintf("p%d:%s:len%d", s.GetPlayerId(), s.GetState().String(), len(s.Points)))
	}
	log.Printf("[BROADCAST] StateOrder=%d, Snakes=[%s]", state.GetStateOrder(), strings.Join(snakeInfo, ","))

	stateMsg := &pb.GameMessage{
		MsgSeq:   model.Int64Ptr(mn.network.NextMsgSeq()),
		SenderId: model.Int32Ptr(mn.myPlayerId),
		Type: &pb.GameMessage_State{
			State: &pb.GameMessage_StateMsg{
				State: state,
			},
		},
	}

	mn.playerAddrsMu.RLock()
	defer mn.playerAddrsMu.RUnlock()

	recipients := make([]int32, 0)
	for playerId, addr := range mn.playerAddrs {
		if playerId == mn.myPlayerId {
			continue
		}
		msg := *stateMsg
		msg.ReceiverId = model.Int32Ptr(playerId)
		msg.MsgSeq = model.Int64Ptr(mn.network.NextMsgSeq())
		mn.network.SendMessageWithAck(&msg, addr)
		recipients = append(recipients, playerId)
	}
	log.Printf("[BROADCAST] Sent to players: %v", recipients)
}

func (mn *MasterNode) checkTimeouts() {
	timeout := time.Duration(float64(mn.engine.Config.GetStateDelayMs())*TimeOutKoef) * time.Millisecond
	timedOut := mn.network.GetTimedOutNodes(timeout)

	for _, addrStr := range timedOut {
		mn.playerAddrsMu.RLock()
		var timedOutPlayerId int32 = -1
		for playerId, addr := range mn.playerAddrs {
			if addr.String() == addrStr {
				timedOutPlayerId = playerId
				break
			}
		}
		mn.playerAddrsMu.RUnlock()

		if timedOutPlayerId > 0 && timedOutPlayerId != mn.myPlayerId {
			log.Printf("Player %d timed out", timedOutPlayerId)
			mn.handlePlayerTimeout(timedOutPlayerId)
		}
	}
}

func (mn *MasterNode) handlePlayerTimeout(playerId int32) {
	if playerId == mn.deputyId {
		log.Printf("DEPUTY %d timed out, assigning new deputy", playerId)
		mn.deputyId = 0
		mn.assignNewDeputy()
	}

	mn.engine.RemovePlayer(playerId)

	mn.playerAddrsMu.Lock()
	addr := mn.playerAddrs[playerId]
	delete(mn.playerAddrs, playerId)
	mn.playerAddrsMu.Unlock()

	if addr != nil {
		mn.network.RemoveNode(addr.String())
	}
}

func (mn *MasterNode) assignNewDeputy() {
	normalPlayer := mn.engine.FindNormalPlayer()
	if normalPlayer == nil {
		log.Printf("No NORMAL players available for DEPUTY")
		mn.deputyId = 0
		return
	}

	mn.deputyId = normalPlayer.GetId()
	mn.engine.SetPlayerRole(mn.deputyId, pb.NodeRole_DEPUTY)

	mn.playerAddrsMu.RLock()
	addr := mn.playerAddrs[mn.deputyId]
	mn.playerAddrsMu.RUnlock()

	if addr != nil {
		roleChangeMsg := &pb.GameMessage{
			MsgSeq:     model.Int64Ptr(mn.network.NextMsgSeq()),
			SenderId:   model.Int32Ptr(mn.myPlayerId),
			ReceiverId: model.Int32Ptr(mn.deputyId),
			Type: &pb.GameMessage_RoleChange{
				RoleChange: &pb.GameMessage_RoleChangeMsg{
					SenderRole:   pb.NodeRole_MASTER.Enum(),
					ReceiverRole: pb.NodeRole_DEPUTY.Enum(),
				},
			},
		}
		mn.network.SendMessageWithAck(roleChangeMsg, addr)
	}

	log.Printf("Assigned new deputy: %d", mn.deputyId)
}

func (mn *MasterNode) announcementLoop() {
	defer mn.wg.Done()

	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-mn.stopChan:
			return
		case <-ticker.C:
			if mn.gameOverCalled || mn.isTransferring || mn.roleTransferred || mn.stopped {
				continue
			}
			mn.sendAnnouncement()
		}
	}
}

func (mn *MasterNode) sendAnnouncement() {
	if mn.isTransferring || mn.stopped {
		return
	}

	if !mn.engine.HasAnyAlivePlayers() {
		log.Printf("No alive players, skipping announcement")
		return
	}

	canJoin := true
	_, _, found := mn.engine.FindFreeSquare()
	if !found {
		canJoin = false
	}

	stateCopy := mn.engine.GetStateCopy()

	announcement := &pb.GameAnnouncement{
		Players:  stateCopy.Players,
		Config:   mn.engine.Config,
		CanJoin:  model.BoolPtr(canJoin),
		GameName: model.StringPtr(mn.gameName),
	}

	msg := &pb.GameMessage{
		MsgSeq: model.Int64Ptr(mn.network.NextMsgSeq()),
		Type: &pb.GameMessage_Announcement{
			Announcement: &pb.GameMessage_AnnouncementMsg{
				Games: []*pb.GameAnnouncement{announcement},
			},
		},
	}

	mn.network.SendMulticast(msg)
}

func (mn *MasterNode) messageHandler() {
	defer mn.wg.Done()

	for {
		select {
		case <-mn.stopChan:
			return
		case received := <-mn.network.GetReceiveChan():
			if received == nil {
				continue
			}
			mn.handleMessage(received.Message, received.Addr)
		}
	}
}

func (mn *MasterNode) handleMessage(msg *pb.GameMessage, addr *net.UDPAddr) {
	switch t := msg.Type.(type) {
	case *pb.GameMessage_Join:
		mn.handleJoin(msg, t.Join, addr)
	case *pb.GameMessage_Steer:
		mn.handleSteer(msg, t.Steer, addr)
	case *pb.GameMessage_Ack:
		mn.handleAck(msg)
	case *pb.GameMessage_Ping:
		mn.handlePing(msg, addr)
	case *pb.GameMessage_RoleChange:
		mn.handleRoleChange(msg, t.RoleChange, addr)
	}
}

func (mn *MasterNode) handleJoin(msg *pb.GameMessage, join *pb.GameMessage_JoinMsg, addr *net.UDPAddr) {
	playerName := join.GetPlayerName()
	requestedRole := join.GetRequestedRole()

	mn.playerAddrsMu.RLock()
	for existingId, existingAddr := range mn.playerAddrs {
		if existingAddr.String() == addr.String() {
			mn.playerAddrsMu.RUnlock()
			log.Printf("Player from %s already connected with ID %d, resending ACK", addr, existingId)
			ackMsg := &pb.GameMessage{
				MsgSeq:     model.Int64Ptr(msg.GetMsgSeq()),
				SenderId:   model.Int32Ptr(mn.myPlayerId),
				ReceiverId: model.Int32Ptr(existingId),
				Type: &pb.GameMessage_Ack{
					Ack: &pb.GameMessage_AckMsg{},
				},
			}
			mn.network.SendMessage(ackMsg, addr)
			return
		}
	}
	mn.playerAddrsMu.RUnlock()

	if requestedRole != pb.NodeRole_VIEWER {
		_, _, found := mn.engine.FindFreeSquare()
		if !found {
			errMsg := &pb.GameMessage{
				MsgSeq:     model.Int64Ptr(mn.network.NextMsgSeq()),
				SenderId:   model.Int32Ptr(mn.myPlayerId),
				ReceiverId: model.Int32Ptr(msg.GetSenderId()),
				Type: &pb.GameMessage_Error{
					Error: &pb.GameMessage_ErrorMsg{
						ErrorMessage: model.StringPtr("No space available on the field"),
					},
				},
			}
			mn.network.SendMessage(errMsg, addr)
			return
		}
	}

	role := pb.NodeRole_NORMAL
	if requestedRole == pb.NodeRole_VIEWER {
		role = pb.NodeRole_VIEWER
	}

	playerId := mn.engine.AddPlayer(playerName, role, addr.IP.String(), int32(addr.Port))

	if role != pb.NodeRole_VIEWER {
		if !mn.engine.CreateSnakeForPlayer(playerId) {
			errMsg := &pb.GameMessage{
				MsgSeq:     model.Int64Ptr(mn.network.NextMsgSeq()),
				SenderId:   model.Int32Ptr(mn.myPlayerId),
				ReceiverId: model.Int32Ptr(playerId),
				Type: &pb.GameMessage_Error{
					Error: &pb.GameMessage_ErrorMsg{
						ErrorMessage: model.StringPtr("Failed to create snake"),
					},
				},
			}
			mn.network.SendMessage(errMsg, addr)
			return
		}

		if mn.deputyId == 0 && role == pb.NodeRole_NORMAL {
			mn.deputyId = playerId
			mn.engine.SetPlayerRole(playerId, pb.NodeRole_DEPUTY)

			roleChangeMsg := &pb.GameMessage{
				MsgSeq:   model.Int64Ptr(mn.network.NextMsgSeq()),
				SenderId: model.Int32Ptr(mn.myPlayerId),
				Type: &pb.GameMessage_RoleChange{
					RoleChange: &pb.GameMessage_RoleChangeMsg{
						SenderRole:   pb.NodeRole_MASTER.Enum(),
						ReceiverRole: pb.NodeRole_DEPUTY.Enum(),
					},
				},
			}
			mn.network.SendMessage(roleChangeMsg, addr)
			log.Printf("Assigned player %d as deputy", playerId)
		}
	}

	mn.playerAddrsMu.Lock()
	mn.playerAddrs[playerId] = addr
	mn.playerAddrsMu.Unlock()

	ackMsg := &pb.GameMessage{
		MsgSeq:     model.Int64Ptr(msg.GetMsgSeq()),
		SenderId:   model.Int32Ptr(mn.myPlayerId),
		ReceiverId: model.Int32Ptr(playerId),
		Type: &pb.GameMessage_Ack{
			Ack: &pb.GameMessage_AckMsg{},
		},
	}
	mn.network.SendMessage(ackMsg, addr)

	log.Printf("Player joined: %s (ID: %d)", playerName, playerId)
}

func (mn *MasterNode) handleSteer(msg *pb.GameMessage, steer *pb.GameMessage_SteerMsg, addr *net.UDPAddr) {
	playerId := msg.GetSenderId()
	dir := steer.GetDirection()

	mn.playerAddrsMu.Lock()
	if oldAddr, exists := mn.playerAddrs[playerId]; !exists || oldAddr.String() != addr.String() {
		mn.playerAddrs[playerId] = addr
	}
	mn.playerAddrsMu.Unlock()

	mn.pendingMu.Lock()
	mn.pendingTurns[playerId] = dir
	mn.pendingMu.Unlock()

	ackMsg := &pb.GameMessage{
		MsgSeq:     model.Int64Ptr(msg.GetMsgSeq()),
		SenderId:   model.Int32Ptr(mn.myPlayerId),
		ReceiverId: model.Int32Ptr(playerId),
		Type: &pb.GameMessage_Ack{
			Ack: &pb.GameMessage_AckMsg{},
		},
	}
	mn.network.SendMessage(ackMsg, addr)
}

func (mn *MasterNode) handleAck(msg *pb.GameMessage) {
	mn.network.AcknowledgeMessage(msg.GetMsgSeq())
}

func (mn *MasterNode) handlePing(msg *pb.GameMessage, addr *net.UDPAddr) {
	senderId := msg.GetSenderId()
	
	mn.playerAddrsMu.Lock()
	if oldAddr, exists := mn.playerAddrs[senderId]; exists && oldAddr.String() != addr.String() {
		log.Printf("Player %d address updated: %v -> %v", senderId, oldAddr, addr)
		mn.playerAddrs[senderId] = addr
	} else if !exists && senderId != mn.myPlayerId {
		mn.playerAddrs[senderId] = addr
		log.Printf("Player %d address registered: %v", senderId, addr)
	}
	mn.playerAddrsMu.Unlock()

	ackMsg := &pb.GameMessage{
		MsgSeq:     model.Int64Ptr(msg.GetMsgSeq()),
		SenderId:   model.Int32Ptr(mn.myPlayerId),
		ReceiverId: model.Int32Ptr(senderId),
		Type: &pb.GameMessage_Ack{
			Ack: &pb.GameMessage_AckMsg{},
		},
	}
	mn.network.SendMessage(ackMsg, addr)
}


func (mn *MasterNode) handleRoleChange(msg *pb.GameMessage, roleChange *pb.GameMessage_RoleChangeMsg, addr *net.UDPAddr) {
	playerId := msg.GetSenderId()

	if playerId == 0 {
		mn.playerAddrsMu.RLock()
		for id, pAddr := range mn.playerAddrs {
			if pAddr.String() == addr.String() {
				playerId = id
				break
			}
		}
		mn.playerAddrsMu.RUnlock()
	}

	if playerId == 0 {
		log.Printf("RoleChange from unknown player at %s", addr)
		return
	}

	if roleChange.GetSenderRole() == pb.NodeRole_VIEWER {
		log.Printf("Player %d is leaving the game", playerId)
		
		if playerId == mn.deputyId {
			log.Printf("DEPUTY %d is leaving, assigning new deputy", playerId)
			mn.deputyId = 0
			mn.assignNewDeputy()
		}

		mn.engine.RemovePlayer(playerId)

		mn.playerAddrsMu.Lock()
		delete(mn.playerAddrs, playerId)
		mn.playerAddrsMu.Unlock()
	}

	ackMsg := &pb.GameMessage{
		MsgSeq:     model.Int64Ptr(msg.GetMsgSeq()),
		SenderId:   model.Int32Ptr(mn.myPlayerId),
		ReceiverId: model.Int32Ptr(playerId),
		Type: &pb.GameMessage_Ack{
			Ack: &pb.GameMessage_AckMsg{},
		},
	}
	mn.network.SendMessage(ackMsg, addr)
}
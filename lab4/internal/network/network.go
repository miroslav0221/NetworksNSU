package network

import (
	"fmt"
	"log"
	"net"
	"sync"
	"time"

	pb "lab4/internal/config"

	"google.golang.org/protobuf/proto"
)

const (
	MulticastAddr = "239.192.0.4:9192"
	MaxPacketSize = 65535
)

type PendingMessage struct {
	Message   *pb.GameMessage
	Addr      *net.UDPAddr
	SentTime  time.Time
	RetryTime time.Duration
}

type NetworkManager struct {
	multicastConn *net.UDPConn
	unicastConn   *net.UDPConn
	multicastAddr *net.UDPAddr

	pendingMessages map[int64]*PendingMessage
	pendingMu       sync.RWMutex

	nodeLastSeen   map[string]time.Time
	nodeLastSeenMu sync.RWMutex

	msgSeq   int64
	msgSeqMu sync.Mutex

	stateDelayMs int32

	receiveChan chan *ReceivedMessage
	stopChan    chan struct{}
	wg          sync.WaitGroup

	started   bool
	startedMu sync.Mutex
}

type ReceivedMessage struct {
	Message *pb.GameMessage
	Addr    *net.UDPAddr
}

func NewNetworkManager(stateDelayMs int32) (*NetworkManager, error) {
	return NewNetworkManagerWithPort(stateDelayMs, 0)
}

func NewNetworkManagerWithPort(stateDelayMs int32, port int) (*NetworkManager, error) {
	nm := &NetworkManager{
		pendingMessages: make(map[int64]*PendingMessage),
		nodeLastSeen:    make(map[string]time.Time),
		msgSeq:          1,
		stateDelayMs:    stateDelayMs,
		receiveChan:     make(chan *ReceivedMessage, 100),
		stopChan:        make(chan struct{}),
	}

	multicastAddr, err := net.ResolveUDPAddr("udp4", MulticastAddr)
	if err != nil {
		return nil, fmt.Errorf("failed to resolve multicast address: %w", err)
	}
	nm.multicastAddr = multicastAddr

	multicastConn, err := net.ListenMulticastUDP("udp4", nil, multicastAddr)
	if err != nil {
		return nil, fmt.Errorf("failed to listen multicast: %w", err)
	}
	multicastConn.SetReadBuffer(MaxPacketSize)
	nm.multicastConn = multicastConn

	portStr := fmt.Sprintf(":%d", port)
	unicastAddr, err := net.ResolveUDPAddr("udp4", portStr)
	if err != nil {
		multicastConn.Close()
		return nil, fmt.Errorf("failed to resolve unicast address: %w", err)
	}

	unicastConn, err := net.ListenUDP("udp4", unicastAddr)
	if err != nil {
		multicastConn.Close()
		return nil, fmt.Errorf("failed to listen unicast: %w", err)
	}
	unicastConn.SetReadBuffer(MaxPacketSize)
	nm.unicastConn = unicastConn

	return nm, nil
}

func (nm *NetworkManager) Start() {
	nm.startedMu.Lock()
	if nm.started {
		nm.startedMu.Unlock()
		return
	}
	nm.started = true
	nm.startedMu.Unlock()

	nm.wg.Add(3)
	go nm.receiveMulticast()
	go nm.receiveUnicast()
	go nm.retryPendingMessages()
}

func (nm *NetworkManager) Stop() {
	log.Printf("NetworkManager.Stop: closing stopChan")
	close(nm.stopChan)
	log.Printf("NetworkManager.Stop: closing multicastConn")
	if nm.multicastConn != nil {
		nm.multicastConn.Close()
	}
	log.Printf("NetworkManager.Stop: closing unicastConn")
	if nm.unicastConn != nil {
		nm.unicastConn.Close()
	}
	log.Printf("NetworkManager.Stop: waiting for goroutines")
	nm.wg.Wait()
	log.Printf("NetworkManager.Stop: closing receiveChan")
	close(nm.receiveChan)
	log.Printf("NetworkManager.Stop: done")
}

func (nm *NetworkManager) GetReceiveChan() <-chan *ReceivedMessage {
	return nm.receiveChan
}

func (nm *NetworkManager) GetLocalPort() int {
	if nm.unicastConn != nil {
		return nm.unicastConn.LocalAddr().(*net.UDPAddr).Port
	}
	return 0
}

func (nm *NetworkManager) NextMsgSeq() int64 {
	nm.msgSeqMu.Lock()
	defer nm.msgSeqMu.Unlock()
	seq := nm.msgSeq
	nm.msgSeq++
	return seq
}

func (nm *NetworkManager) SendMessage(msg *pb.GameMessage, addr *net.UDPAddr) error {
	data, err := proto.Marshal(msg)
	if err != nil {
		return fmt.Errorf("failed to marshal message: %w", err)
	}

	_, err = nm.unicastConn.WriteToUDP(data, addr)
	if err != nil {
		return fmt.Errorf("failed to send message: %w", err)
	}

	nm.nodeLastSeenMu.Lock()
	nm.nodeLastSeen[addr.String()] = time.Now()
	nm.nodeLastSeenMu.Unlock()

	return nil
}

func (nm *NetworkManager) SendMessageWithAck(msg *pb.GameMessage, addr *net.UDPAddr) error {
	err := nm.SendMessage(msg, addr)
	if err != nil {
		return err
	}

	nm.pendingMu.Lock()
	nm.pendingMessages[msg.GetMsgSeq()] = &PendingMessage{
		Message:   msg,
		Addr:      addr,
		SentTime:  time.Now(),
		RetryTime: time.Duration(nm.stateDelayMs/10) * time.Millisecond,
	}
	nm.pendingMu.Unlock()

	return nil
}

func (nm *NetworkManager) SendMulticast(msg *pb.GameMessage) error {
	data, err := proto.Marshal(msg)
	if err != nil {
		return fmt.Errorf("failed to marshal message: %w", err)
	}

	_, err = nm.unicastConn.WriteToUDP(data, nm.multicastAddr)
	if err != nil {
		return fmt.Errorf("failed to send multicast: %w", err)
	}

	return nil
}

func (nm *NetworkManager) AcknowledgeMessage(msgSeq int64) {
	nm.pendingMu.Lock()
	delete(nm.pendingMessages, msgSeq)
	nm.pendingMu.Unlock()
}

func (nm *NetworkManager) UpdateNodeLastSeen(addr *net.UDPAddr) {
	nm.nodeLastSeenMu.Lock()
	nm.nodeLastSeen[addr.String()] = time.Now()
	nm.nodeLastSeenMu.Unlock()
}

func (nm *NetworkManager) GetTimedOutNodes(timeout time.Duration) []string {
	nm.nodeLastSeenMu.RLock()
	defer nm.nodeLastSeenMu.RUnlock()

	var timedOut []string
	now := time.Now()
	for addr, lastSeen := range nm.nodeLastSeen {
		if now.Sub(lastSeen) > timeout {
			timedOut = append(timedOut, addr)
		}
	}
	return timedOut
}

func (nm *NetworkManager) RemoveNode(addr string) {
	nm.nodeLastSeenMu.Lock()
	delete(nm.nodeLastSeen, addr)
	nm.nodeLastSeenMu.Unlock()
}

func (nm *NetworkManager) receiveMulticast() {
	defer nm.wg.Done()
	buffer := make([]byte, MaxPacketSize)

	for {
		select {
		case <-nm.stopChan:
			return
		default:
			nm.multicastConn.SetReadDeadline(time.Now().Add(100 * time.Millisecond))
			n, addr, err := nm.multicastConn.ReadFromUDP(buffer)
			if err != nil {
				if netErr, ok := err.(net.Error); ok && netErr.Timeout() {
					continue
				}
				continue
			}

			msg := &pb.GameMessage{}
			if err := proto.Unmarshal(buffer[:n], msg); err != nil {
				continue
			}

			select {
			case nm.receiveChan <- &ReceivedMessage{Message: msg, Addr: addr}:
			default:
			}
		}
	}
}

func (nm *NetworkManager) receiveUnicast() {
	defer nm.wg.Done()
	buffer := make([]byte, MaxPacketSize)

	for {
		select {
		case <-nm.stopChan:
			return
		default:
			nm.unicastConn.SetReadDeadline(time.Now().Add(100 * time.Millisecond))
			n, addr, err := nm.unicastConn.ReadFromUDP(buffer)
			if err != nil {
				if netErr, ok := err.(net.Error); ok && netErr.Timeout() {
					continue
				}
				continue
			}

			msg := &pb.GameMessage{}
			if err := proto.Unmarshal(buffer[:n], msg); err != nil {
				continue
			}

			nm.UpdateNodeLastSeen(addr)

			select {
			case nm.receiveChan <- &ReceivedMessage{Message: msg, Addr: addr}:
			default:
			}
		}
	}
}

func (nm *NetworkManager) retryPendingMessages() {
	defer nm.wg.Done()
	ticker := time.NewTicker(time.Duration(nm.stateDelayMs/10) * time.Millisecond)
	defer ticker.Stop()

	for {
		select {
		case <-nm.stopChan:
			return
		case <-ticker.C:
			nm.pendingMu.Lock()
			now := time.Now()
			for seq, pending := range nm.pendingMessages {
				if now.Sub(pending.SentTime) > pending.RetryTime {
					data, err := proto.Marshal(pending.Message)
					if err == nil {
						nm.unicastConn.WriteToUDP(data, pending.Addr)
					}
					pending.SentTime = now
				}
				if now.Sub(pending.SentTime) > pending.RetryTime*10 {
					delete(nm.pendingMessages, seq)
				}
			}
			nm.pendingMu.Unlock()
		}
	}
}

func (nm *NetworkManager) GetPendingMessagesForAddr(oldAddr, newAddr *net.UDPAddr) {
	nm.pendingMu.Lock()
	defer nm.pendingMu.Unlock()
	for _, pending := range nm.pendingMessages {
		if pending.Addr.String() == oldAddr.String() {
			pending.Addr = newAddr
		}
	}
}

func (nm *NetworkManager) SetStateDelayMs(stateDelayMs int32) {
	nm.stateDelayMs = stateDelayMs
}

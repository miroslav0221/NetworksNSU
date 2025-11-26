package src

import "sync"


type State int
const (
    StateHello State = iota
    StateRequest
    StateConnecting
    StateProxy
)


type Conn struct {
    mu               sync.Mutex
    fd               int
    rfd              int
    state            State
    host             string
    domain           string
    port             uint16
    resolving        bool
    writeBufToRemote []byte 
    writeBufToClient []byte
    bytesToRemote    uint64 
    bytesToClient    uint64 
}



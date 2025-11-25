package src

import "sync"


type State int
const (
    StateHello State = iota
    StateRequest
    StateConnecting
    StateProxy
)

// type Conn struct {
//     mu     sync.Mutex 

//     fd     int     
//     rfd    int     
// 	state  State
//     domain string
//     host string
//     port uint16
//     resolving bool

//     writeBuf []byte

// }

type Conn struct {
    mu               sync.Mutex
    fd               int
    rfd              int
    state            State
    host             string
    domain           string
    port             uint16
    resolving        bool
    writeBufToRemote []byte // очередь байт, которые нужно дописать в remote (rfd)
    writeBufToClient []byte // очередь байт, которые нужно дописать в client (fd)
    bytesToRemote    uint64 // счётчик байт, отправленных client->remote
    bytesToClient    uint64 // счётчик байт, отправленных remote->client
}



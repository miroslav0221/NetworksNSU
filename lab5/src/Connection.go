package src

type State int
const (
    StateHello State = iota
    StateRequest
    StateConnecting
    StateProxy
)

type Conn struct {
    fd     int     
    rfd    int     
    // state  ConnState
	state  State
    inBuf  []byte  
    outBuf []byte   

    host string
    port uint16
}
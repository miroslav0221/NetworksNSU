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
	state  State
    domain string
    host string
    port uint16
    resolving bool
}
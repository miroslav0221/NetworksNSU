package src

import (
	"bytes"
	"encoding/binary"
	"errors"
	"fmt"
	"net"
	"sync"

	"golang.org/x/sys/unix"
)

const (
	ZERO  = 0x00
	ONE   = 0x01
	TWO   = 0x02
	THREE = 0x03
	FOUR  = 0x04
	FIVE  = 0x05
)

const (
	MIN_COUNT_AUTH = 1
	MIN_LEN_REQUEST = 7
	MIN_LEN_REQ_DOMAIN = 10

	START_BYTE_IP = 4
	FINISH_BYTE_IP = 8

	START_BYTE_PORT = 8
	FINISH_BYTE_PORT = 10

	INDEX_LEN_DOMAIN = 4

	START_DOMAIN_BYTE = 5
	PORT_LEN = 2
)

const (
	localhost   = "127.0.0.1"
	countClient = 128
	bufsize     = 65536
)


type Server struct {
	listenFD    int
	selecter    int
	IP          string
	connections map[int]*Conn
	mu          sync.Mutex
}

func NewServer() *Server {
	return &Server{
		listenFD:    0,
		selecter:    0,
		connections: make(map[int]*Conn),
	}
}

func getInterface(name string) (string, error) {
	interfaces, err := net.Interfaces()
	if err != nil {
		return "", err
	}
	for _, i := range interfaces {
		if i.Name == name {
			addrs, err := i.Addrs()
			if err != nil {
				return "", err
			}
			for _, addr := range addrs {
				ipnet, ok := addr.(*net.IPNet)
				if !ok || ipnet.IP.IsLoopback() {
					continue
				}
				if ipnet.IP.To4() != nil {
					return ipnet.IP.String(), nil
				}
			}
		}
	}
	return "", fmt.Errorf("interface %s not found or has no IPv4 address", name)
}

func (s *Server) InitSocket(port int) {
	ifaceIP, err := getInterface("en0")
	if err != nil {
		fmt.Printf("getInterface failed: %v; falling back to localhost\n", err)
		ifaceIP = localhost
	}

	s.listenFD, err = unix.Socket(unix.AF_INET, unix.SOCK_STREAM, 0)
	if err != nil {
		fmt.Println("Failed init tcp socket")
		panic(err)
	}

	addr := &unix.SockaddrInet4{Port: port}

	ip := net.ParseIP(ifaceIP)
	if ip == nil {
		panic(fmt.Errorf("invalid IP address: %s", ifaceIP))
	} else {
		ip4 := ip.To4()
		if ip4 == nil {
			panic(fmt.Errorf("not an IPv4 address: %s", ifaceIP))
		}
		copy(addr.Addr[:], ip4)
	}


	err = unix.SetsockoptInt(s.listenFD, unix.SOL_SOCKET, unix.SO_REUSEADDR, 1)

	 if err != nil {
        unix.Close(s.listenFD)
        panic(err)
    }

	if err = unix.Bind(s.listenFD, addr); err != nil {
		fmt.Println("Failed bind")
		panic(err)
	}

	if err = unix.Listen(s.listenFD, countClient); err != nil {
		panic(err)
	}

	if err = unix.SetNonblock(s.listenFD, true); err != nil {
		panic(err)
	}

	s.IP = ifaceIP
	fmt.Printf("Listening on %s:%d\n", ifaceIP, port)
}

func (s *Server) InitSelecter() {
	var err error
	s.selecter, err = unix.Kqueue()
	if err != nil {
		panic(err)
	}

	change := unix.Kevent_t{
		Ident:  uint64(s.listenFD),
		Filter: unix.EVFILT_READ,
		Flags:  unix.EV_ADD | unix.EV_CLEAR,
	}
	if _, err := unix.Kevent(s.selecter, []unix.Kevent_t{change}, nil, nil); err != nil {
		panic(err)
	}
}

func (s *Server) newConnection(listenFD int) error {
	connFD, _, err := unix.Accept(listenFD)
	if err != nil {
		return err
	}
	_ = unix.SetNonblock(connFD, true)

	change := unix.Kevent_t{
		Ident:  uint64(connFD),
		Filter: unix.EVFILT_READ,
		Flags:  unix.EV_ADD | unix.EV_CLEAR,
	}
	unix.Kevent(s.selecter, []unix.Kevent_t{change}, nil, nil)

	c := &Conn{
		fd:       connFD,
		state:    StateHello,
		rfd:      0,
		host:     "",
		domain:   "",
		resolving: false,
	}

	s.mu.Lock()
	s.connections[connFD] = c
	s.mu.Unlock()
	return nil
}

func (s *Server) readFD(fd int, conn *Conn) ([]byte, error, int) {
	buf := make([]byte, bufsize)
	n, err := unix.Read(fd, buf)
	if n == 0 || (err != nil && errors.Is(err, unix.ECONNRESET)) {
		s.closeConn(conn)
		return nil, err, 0
	}
	if err != nil {
		if errors.Is(err, unix.EAGAIN) || errors.Is(err, unix.EWOULDBLOCK) {
			return nil, err, 0
		}
		s.closeConn(conn)
		return nil, err, 0
	}
	return buf, nil, n
}

func (s *Server) handleRequest(fd int, conn *Conn, buf []byte, n int) error {
	if conn.state == StateHello {
		s.processHello(fd, buf[:n])
		conn.state = StateRequest
		s.mu.Lock()
		s.connections[fd] = conn
		s.mu.Unlock()
	} else if conn.state == StateRequest {
		s.processRequest(conn, buf[:n])
		s.connectToHost(conn)
		s.mu.Lock()
		s.connections[fd] = conn
		s.mu.Unlock()
	} else if conn.state == StateProxy {
		if fd == conn.fd {
			if conn.rfd <= 0 {
				return errors.New("remote fd <= 0")
			}
			if err := writeFull(conn.rfd, buf[:n]); err != nil {
				s.closeConn(conn)
			}
		} else if conn.rfd == fd {
			if conn.fd <= 0 {
				return errors.New("client fd <= 0")
			}
			if err := writeFull(conn.fd, buf[:n]); err != nil {
				s.closeConn(conn)
			}
		}
	}
	return nil
}

func (s *Server) WaitEvents() {
	events := make([]unix.Kevent_t, countClient)
	for {
		count, err := unix.Kevent(s.selecter, nil, events, nil)
		if err != nil {
			if err == unix.EINTR {
				continue
			}
			panic(err)
		}
		for i := 0; i < count; i++ {
			fd := int(events[i].Ident)
			filter := events[i].Filter

			if fd == s.listenFD && filter == unix.EVFILT_READ {
				_ = s.newConnection(fd)
				continue
			}

			if filter == unix.EVFILT_READ {
				s.mu.Lock()
				conn, ok := s.connections[fd]
				s.mu.Unlock()
				if !ok {
					unix.Close(fd)
					continue
				}

				buf, err, n := s.readFD(fd, conn)
				if err != nil {
					if errors.Is(err, unix.EAGAIN) || errors.Is(err, unix.EWOULDBLOCK) {
						continue
					}
					continue
				}
				if n == 0 || buf == nil {
					continue
				}

				_ = s.handleRequest(fd, conn, buf, n)
			} else if filter == unix.EVFILT_WRITE {
				s.mu.Lock()
				conn, ok := s.connections[fd]
				s.mu.Unlock()
				if !ok {
					continue
				}
				if conn.state == StateConnecting && fd == conn.rfd {
					serr, err := unix.GetsockoptInt(fd, unix.SOL_SOCKET, unix.SO_ERROR)
					if err != nil || serr != 0 {
						s.closeConn(conn)
						continue
					}

					s.answerHello(ZERO, conn.fd, nil, 0, "")
					conn.state = StateProxy

					change := unix.Kevent_t{
						Ident:  uint64(conn.rfd),
						Filter: unix.EVFILT_READ,
						Flags:  unix.EV_ADD | unix.EV_CLEAR,
					}
					unix.Kevent(s.selecter, []unix.Kevent_t{change}, nil, nil)

					s.mu.Lock()
					s.connections[conn.rfd] = conn
					s.connections[conn.fd] = conn
					s.mu.Unlock()

					fmt.Printf("Connected to host %s:%d\n", conn.host, conn.port)
				}
			}
		}
	}
}

func (s *Server) Close() {
	if s.listenFD > 0 {
		unix.Close(s.listenFD)
	}
	if s.selecter > 0 {
		unix.Close(s.selecter)
	}
	s.mu.Lock()
	for _, c := range s.connections {
		if c != nil {
			if c.fd > 0 {
				unix.Close(c.fd)
			}
			if c.rfd > 0 {
				unix.Close(c.rfd)
			}
		}
	}
	s.mu.Unlock()
}

func (s *Server) answerHello(answer byte, fd int, ip net.IP, port uint16, domain string) {
	var buf bytes.Buffer
	buf.Write([]byte{byte(FIVE), answer, ZERO})
	buf.WriteByte(byte(ONE))
	buf.Write([]byte{0, 0, 0, 0})
	binary.Write(&buf, binary.BigEndian, uint16(0))
	_, _ = unix.Write(fd, buf.Bytes())
}

func (s *Server) processRequest(conn *Conn, data []byte) {
	if len(data) == 0 || len(data) < MIN_LEN_REQUEST {
		fmt.Println("len client message = 0")
		return
	}

	if data[0] != FIVE {
		fmt.Println("Version SOCKS != 5")
		return
	}

	if data[1] != ONE {
		fmt.Println("Command != 1 (CONNECT TCP/IP)")
		return
	}

	if data[2] != 0 {
		fmt.Println("Reserved != 0")
		return
	}

	if data[3] != ONE && data[3] != THREE {
		fmt.Println("Address type != 1 (IPV4) or 3 (DOMAIN NAME)")
		return
	}

	if data[3] == ONE {
		if len(data) < MIN_LEN_REQ_DOMAIN {
			fmt.Println("invalid IPv4 request length")
			return
		}
		ip := net.IP(data[START_BYTE_IP:FINISH_BYTE_IP])
		port := binary.BigEndian.Uint16(data[START_BYTE_PORT:FINISH_BYTE_PORT])
		conn.host = ip.String()
		conn.port = port
		fmt.Printf("IP: %s\n", ip.String())
		fmt.Printf("Port: %v\n", port)
	}

	if data[3] == THREE {
		domainLen := int(data[INDEX_LEN_DOMAIN])
		if MIN_LEN_REQUEST + domainLen > len(data) {
			fmt.Println("invalid domain request length")
			return
		}
		domain := string(data[START_DOMAIN_BYTE : START_DOMAIN_BYTE + domainLen])
		port := binary.BigEndian.Uint16(data[START_DOMAIN_BYTE + domainLen : START_DOMAIN_BYTE + domainLen + PORT_LEN])
		conn.domain = domain
		conn.host = domain 
		conn.port = port
		fmt.Printf("Domain: %s\n", domain)
		fmt.Printf("Port: %v\n", port)
	}
}

func (s *Server) processHello(fd int, data []byte) {
	if len(data) == 0 {
		fmt.Println("len client message = 0")
		return
	}
	if data[0] != FIVE {
		fmt.Println("Version SOCKS != 5")
		return
	}
	if int(data[1]) < 1 {
		fmt.Println("No auth methods")
		return
	}

	var buf bytes.Buffer
	buf.Write([]byte{byte(FIVE), byte(ZERO)})
	_, _ = unix.Write(fd, buf.Bytes())
}


func (s *Server) queryDNS(conn *Conn) {
	s.mu.Lock()
	if conn.resolving {
		s.mu.Unlock()
		return
	}
	conn.resolving = true
	s.mu.Unlock()

	go func() {
		addrs, err := net.LookupIP(conn.host)
		s.mu.Lock()

		current, ok := s.connections[conn.fd]
		if !ok || current != conn {
			return
		}

		conn.resolving = false

		if err != nil || len(addrs) == 0 {
			fmt.Printf("Failed to resolve host %s: %v\n", conn.host, err)
			if conn.rfd > 0 {
				unix.Close(conn.rfd)
				delete(s.connections, conn.rfd)
				conn.rfd = 0
			}
			s.closeConn(conn)
			return
		}

		ip := addrs[0]
		conn.host = ip.String()

		s.mu.Unlock()
		s.connectToHost(conn)
	}()
}

func (s *Server) connectToHost(conn *Conn) {
	s.mu.Lock()
	if conn.rfd > 0 || conn.state == StateConnecting || conn.state == StateProxy {
		s.mu.Unlock()
		return
	}
	s.mu.Unlock()

	rfd, err := unix.Socket(unix.AF_INET, unix.SOCK_STREAM, 0)
	if err != nil {
		return
	}
	_ = unix.SetNonblock(rfd, true)

	ip := net.ParseIP(conn.host)
	if ip == nil {
		s.queryDNS(conn)
		unix.Close(rfd)
		return
	}

	ipv4 := ip.To4()
	if ipv4 == nil {
		fmt.Printf("Not an IPv4 address: %s\n", conn.host)
		unix.Close(rfd)
		return
	}

	addr := &unix.SockaddrInet4{Port: int(conn.port)}
	copy(addr.Addr[:], ipv4)
	err = unix.Connect(rfd, addr)
	if err != nil && err != unix.EINPROGRESS {
		unix.Close(rfd)
		return
	}

	change := unix.Kevent_t{
		Ident:  uint64(rfd),
		Filter: unix.EVFILT_WRITE,
		Flags:  unix.EV_ADD | unix.EV_CLEAR,
	}
	unix.Kevent(s.selecter, []unix.Kevent_t{change}, nil, nil)

	s.mu.Lock()
	conn.rfd = rfd
	conn.state = StateConnecting
	s.connections[rfd] = conn
	s.connections[conn.fd] = conn
	s.mu.Unlock()
}

func (s *Server) closeConn(conn *Conn) {
	if conn == nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if conn.fd > 0 {
		unix.Close(conn.fd)
		delete(s.connections, conn.fd)
	}
	if conn.rfd > 0 {
		unix.Close(conn.rfd)
		delete(s.connections, conn.rfd)
	}
}

func writeFull(fd int, buf []byte) error {
	off := 0
	for off < len(buf) {
		n, err := unix.Write(fd, buf[off:])
		if n > 0 {
			off += n
			continue
		}
		if err != nil {
			return err
		}
		return fmt.Errorf("write returned 0")
	}
	return nil
}

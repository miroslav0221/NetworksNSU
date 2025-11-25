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
	SAVEBYTE      = 0x00
	OK 		      = 0x00
	IPV4BYTE      = 0x01
	CONNECT       = 0x01
	DOMAINBYTE 	  = 0x03
	VERSIONSOCKS  = 0x05
)

const (
	INDEX_VERSION_SOCKS = 0
	INDEX_TYPE_COMMAND  = 1
	COUNT_AUTH          = 1
	INDEX_SAVE_BYTE     = 2
	INDEX_TYPE_HOST     = 3

	MIN_COUNT_AUTH     = 1
	MIN_LEN_REQUEST    = 7
	MIN_LEN_REQUEST_   = 4
	MIN_LEN_REQ_DOMAIN = 10

	START_BYTE_IP  = 4
	FINISH_BYTE_IP = 8

	START_BYTE_PORT  = 8
	FINISH_BYTE_PORT = 10

	INDEX_LEN_DOMAIN = 4

	START_DOMAIN_BYTE = 5
	PORT_LEN          = 2
)

const (
	localhost   = "127.0.0.1"
	countClient = 256
	bufsize     = 256*1024
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
	if _, err := unix.Kevent(s.selecter, []unix.Kevent_t{change}, nil, nil); err != nil {
		unix.Close(connFD)
		return err
	}

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

var bufPool = sync.Pool {
	New: func() interface{} {
		b := make([]byte, bufsize)
		return &b
	},
}

func (s *Server) readFD(fd int, conn *Conn) ([]byte, error, int) {
	pb := bufPool.Get().(*[]byte)
	buf := *pb
	n, err := unix.Read(fd, buf)
	if n == 0 || (err != nil && errors.Is(err, unix.ECONNRESET)) {
		bufPool.Put(pb)
		s.closeConn(conn)
		return nil, err, 0
	}
	if err != nil {
		if errors.Is(err, unix.EAGAIN) || errors.Is(err, unix.EWOULDBLOCK) {
			bufPool.Put(pb)
			return nil, err, 0
		}
		bufPool.Put(pb)
		s.closeConn(conn)
		return nil, err, 0
	}
	data := make([]byte, n)
	copy(data, buf[:n])
	bufPool.Put(pb)
	return data, nil, n
}

func (s *Server) handleRequest(fd int, conn *Conn, buf []byte, n int) error {
	if conn.state == StateHello {
		s.processHello(fd, buf[:n])
		conn.mu.Lock()
		conn.state = StateRequest
		conn.mu.Unlock()
		s.mu.Lock()
		s.connections[fd] = conn
		s.mu.Unlock()
	} else if conn.state == StateRequest {
		if err := s.processRequest(conn, buf[:n]); err != nil {
			fmt.Printf("processRequest error: %v\n", err)
			s.closeConn(conn)
        	return nil
    	}
		s.connectToHost(conn)
		s.mu.Lock()
		s.connections[fd] = conn
		s.mu.Unlock()
	} else if conn.state == StateProxy {
		if fd == conn.fd {
			conn.mu.Lock()
			rfd := conn.rfd
			conn.mu.Unlock()
			if rfd <= 0 {
				return errors.New("remote fd <= 0")
			}
			if err := s.writeMessage(rfd, conn, buf[:n]); err != nil {
				s.closeConn(conn)
			}
		} else if conn.rfd == fd {
			conn.mu.Lock()
			cfd := conn.fd
			conn.mu.Unlock()
			if cfd <= 0 {
				return errors.New("client fd <= 0")
			}
			if err := s.writeMessage(cfd, conn, buf[:n]); err != nil {
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
				if n == 0 {
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

				conn.mu.Lock()
				var buf *[]byte
				if fd == conn.fd {
					buf = &conn.writeBufToClient
				} else if fd == conn.rfd {
					buf = &conn.writeBufToRemote
				} else {
					conn.mu.Unlock()
					continue
				}

				if len(*buf) > 0 {
					n, err := unix.Write(fd, *buf)
					if n > 0 {
						if n >= len(*buf) {
							*buf = (*buf)[:0]
						} else {
							*buf = (*buf)[n:]
						}
						if fd == conn.rfd {
							conn.bytesToRemote += uint64(n)
						} else {
							conn.bytesToClient += uint64(n)
						}
					}
					empty := len(*buf) == 0
					conn.mu.Unlock()

					if empty {
						change := unix.Kevent_t{
							Ident:  uint64(fd),
							Filter: unix.EVFILT_WRITE,
							Flags:  unix.EV_DELETE,
						}
						_, _ = unix.Kevent(s.selecter, []unix.Kevent_t{change}, nil, nil)
					}
					if err != nil && err != unix.EAGAIN {
						s.closeConn(conn)
					}
				} else {
					conn.mu.Unlock()
				}


				conn.mu.Lock()
				state := conn.state
				rfd := conn.rfd
				conn.mu.Unlock()

				if state == StateConnecting && fd == rfd {
					serr, err := unix.GetsockoptInt(fd, unix.SOL_SOCKET, unix.SO_ERROR)
					if err != nil || serr != 0 {
						s.closeConn(conn)
						continue
					}

					conn.mu.Lock()
					s.answerHello(OK, conn.fd, net.ParseIP(conn.host), conn.port, "")
					conn.mu.Unlock()
					conn.mu.Lock()
					conn.state = StateProxy
					conn.mu.Unlock()

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
			c.mu.Lock()
			if c.fd > 0 {
				unix.Close(c.fd)
			}
			if c.rfd > 0 {
				unix.Close(c.rfd)
			}
			c.mu.Unlock()
		}
	}
	s.connections = make(map[int]*Conn)
	s.mu.Unlock()
}


func (s *Server) answerHello(answer byte, fd int, ip net.IP, port uint16, domain string) {
    var buf bytes.Buffer
    buf.Write([]byte{byte(VERSIONSOCKS), answer, SAVEBYTE})
    
    if ip != nil {
        buf.WriteByte(byte(IPV4BYTE))
        buf.Write(ip.To4())
    } else if domain != "" {
        buf.WriteByte(byte(DOMAINBYTE))
        buf.WriteByte(byte(len(domain)))
        buf.WriteString(domain)
    } else {
        buf.WriteByte(byte(IPV4BYTE))
        buf.Write([]byte{0, 0, 0, 0})
    }
    
    binary.Write(&buf, binary.BigEndian, port)
    _, _ = unix.Write(fd, buf.Bytes())
}

func (s *Server) processRequest(conn *Conn, data []byte) error {
    if len(data) < MIN_LEN_REQUEST_ {
        return fmt.Errorf("request too short")
    }
    if data[INDEX_VERSION_SOCKS] != VERSIONSOCKS {
        return fmt.Errorf("bad socks version")
    }
    if data[INDEX_TYPE_COMMAND] != CONNECT {
        return fmt.Errorf("unsupported command %d", data[INDEX_TYPE_COMMAND])
    }
    if data[INDEX_SAVE_BYTE] != SAVEBYTE {
        return fmt.Errorf("reserved byte != 0")
    }

    atyp := data[INDEX_TYPE_HOST]
    switch atyp {
    case IPV4BYTE:
        if len(data) < MIN_LEN_REQ_DOMAIN {
            return fmt.Errorf("ipv4 request too short")
        }
        ip := net.IP(data[START_BYTE_IP:FINISH_BYTE_IP]).To4()
        port := binary.BigEndian.Uint16(data[START_BYTE_PORT:FINISH_BYTE_PORT])

        conn.mu.Lock()
        conn.host = ip.String()
        conn.port = port
        conn.mu.Unlock()

        fmt.Printf("IP: %s\n", ip.String())
        fmt.Printf("Port: %v\n", port)
    case DOMAINBYTE:
        if len(data) < START_DOMAIN_BYTE {
            return fmt.Errorf("domain request too short")
        }
        dlen := int(data[INDEX_LEN_DOMAIN])
        if len(data) < START_DOMAIN_BYTE+dlen+PORT_LEN {
            return fmt.Errorf("domain request length mismatch")
        }
        domain := string(data[START_DOMAIN_BYTE : START_DOMAIN_BYTE+dlen])
        port := binary.BigEndian.Uint16(data[START_DOMAIN_BYTE+dlen : START_DOMAIN_BYTE+dlen+PORT_LEN])

        conn.mu.Lock()
        conn.domain = domain
        conn.host = domain
        conn.port = port
        conn.mu.Unlock()

        fmt.Printf("Domain: %s\n", domain)
        fmt.Printf("Port: %v\n", port)
    default:
        return fmt.Errorf("unsupported atyp %d", atyp)
    }
    return nil
}


func (s *Server) processHello(fd int, data []byte) {
	if len(data) == 0 {
		fmt.Println("len client message = 0")
		return
	}
	if data[INDEX_VERSION_SOCKS] != VERSIONSOCKS {
		fmt.Println("Version SOCKS != 5")
		return
	}
	if int(data[COUNT_AUTH]) < MIN_COUNT_AUTH  {
		fmt.Println("No auth methods")
		return
	}

	var buf bytes.Buffer
	buf.Write([]byte{byte(VERSIONSOCKS), byte(SAVEBYTE)})
	_, _ = unix.Write(fd, buf.Bytes())
}

func (s *Server) queryDNS(conn *Conn) {
	conn.mu.Lock()
	if conn.resolving {
		conn.mu.Unlock()
		return
	}
	conn.resolving = true
	conn.mu.Unlock()

	go func() {
		addrs, err := net.LookupIP(conn.host)

		s.mu.Lock()
		current, ok := s.connections[conn.fd]
		if !ok || current != conn {
			conn.mu.Lock()
			conn.resolving = false
			conn.mu.Unlock()
			s.mu.Unlock()
			return
		}

		conn.mu.Lock()
		conn.resolving = false
		conn.mu.Unlock()

		if err != nil || len(addrs) == 0 {
			s.mu.Unlock()
			fmt.Printf("Failed to resolve host %s: %v\n", conn.host, err)
			s.closeConn(conn)
			return
		}

		ip := addrs[0].To4()
		if ip == nil {
			s.mu.Unlock()
			fmt.Printf("Resolved not IPv4: %s\n", addrs[0].String())
			s.closeConn(conn)
			return
		}

		conn.mu.Lock()
		conn.host = ip.String()
		conn.mu.Unlock()
		s.mu.Unlock()

		s.connectToHost(conn)
	}()
}

func (s *Server) connectToHost(conn *Conn) {
	conn.mu.Lock()
	if conn.rfd > 0 || conn.state == StateConnecting || conn.state == StateProxy {
		conn.mu.Unlock()
		return
	}
	conn.mu.Unlock()

	rfd, err := unix.Socket(unix.AF_INET, unix.SOCK_STREAM, 0)
	if err != nil {
		return
	}
	_ = unix.SetNonblock(rfd, true)

	conn.mu.Lock()
	host := conn.host
	port := conn.port
	conn.mu.Unlock()

	ip := net.ParseIP(host)
	if ip == nil {
		s.queryDNS(conn)
		unix.Close(rfd)
		return
	}

	ipv4 := ip.To4()
	if ipv4 == nil {
		fmt.Printf("Not an IPv4 address: %s\n", host)
		unix.Close(rfd)
		return
	}

	addr := &unix.SockaddrInet4{Port: int(port)}
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

	if _, err := unix.Kevent(s.selecter, []unix.Kevent_t{change}, nil, nil); err != nil {
		unix.Close(rfd)
		return
	}

	conn.mu.Lock()
	conn.rfd = rfd
	conn.state = StateConnecting
	conn.mu.Unlock()

	s.mu.Lock()
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
	conn.mu.Lock()
	conn.fd = 0
	conn.rfd = 0
	conn.mu.Unlock()
}

func (s *Server) writeMessage(fd int, conn *Conn, data []byte) error {
    conn.mu.Lock()
    var buf *[]byte
    if fd == conn.rfd {
        buf = &conn.writeBufToRemote
    } else if fd == conn.fd {
        buf = &conn.writeBufToClient
    } else {
        conn.mu.Unlock()
        return fmt.Errorf("writeMessage: unknown fd %d", fd)
    }

    if len(*buf) > 0 {
        *buf = append(*buf, data...)
        conn.mu.Unlock()
        change := unix.Kevent_t{
            Ident:  uint64(fd),
            Filter: unix.EVFILT_WRITE,
            Flags:  unix.EV_ADD | unix.EV_CLEAR,
        }
        _, _ = unix.Kevent(s.selecter, []unix.Kevent_t{change}, nil, nil)
        return nil
    }
    conn.mu.Unlock()

    n, err := unix.Write(fd, data)
    if n > 0 {
        conn.mu.Lock()
        if fd == conn.rfd {
            conn.bytesToRemote += uint64(n)
        } else {
            conn.bytesToClient += uint64(n)
        }
        conn.mu.Unlock()

        if n == len(data) {
            return nil
        }
        remaining := make([]byte, len(data)-n)
        copy(remaining, data[n:])
        conn.mu.Lock()
        *buf = append(*buf, remaining...)
        conn.mu.Unlock()

        change := unix.Kevent_t{
            Ident:  uint64(fd),
            Filter: unix.EVFILT_WRITE,
            Flags:  unix.EV_ADD | unix.EV_CLEAR,
        }
        if _, err2 := unix.Kevent(s.selecter, []unix.Kevent_t{change}, nil, nil); err2 != nil {
            return err2
        }
        return nil
    }

    if err != nil {
        if err == unix.EAGAIN || err == unix.EWOULDBLOCK {
            conn.mu.Lock()
            *buf = append(*buf, data...)
            conn.mu.Unlock()

            change := unix.Kevent_t{
                Ident:  uint64(fd),
                Filter: unix.EVFILT_WRITE,
                Flags:  unix.EV_ADD | unix.EV_CLEAR,
            }
            if _, err2 := unix.Kevent(s.selecter, []unix.Kevent_t{change}, nil, nil); err2 != nil {
                return err2
            }
            return nil
        }
        return err
    }

    return nil
}


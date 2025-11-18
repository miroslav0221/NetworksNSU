package src

import (
	"bytes"
	"encoding/binary"
	"errors"
	"fmt"
	"net"
	"time"

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

type Server struct {
	listenFD    int
	selecter    int
	connections map[int]*Conn
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
		fmt.Printf("getInterface failed: %v; falling back to 0.0.0.0\n", err)
		ifaceIP = "0.0.0.0"
	}

	s.listenFD, err = unix.Socket(unix.AF_INET, unix.SOCK_STREAM, 0)
	if err != nil {
		panic(err)
	}

	addr := &unix.SockaddrInet4{Port: port}

	ip := net.ParseIP(ifaceIP)
	if ip == nil {
		if ifaceIP == "0.0.0.0" {
		} else {
			panic(fmt.Errorf("invalid IP address: %s", ifaceIP))
		}
	} else {
		ip4 := ip.To4()
		if ip4 == nil {
			panic(fmt.Errorf("not an IPv4 address: %s", ifaceIP))
		}
		copy(addr.Addr[:], ip4)
	}

	if err := unix.Bind(s.listenFD, addr); err != nil {
		fmt.Println("Failed bind")
		panic(err)
	}
	if err := unix.Listen(s.listenFD, 128); err != nil {
		panic(err)
	}
	if err := unix.SetNonblock(s.listenFD, true); err != nil {
		panic(err)
	}
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
		Flags:  unix.EV_ADD,
	}
	if _, err := unix.Kevent(s.selecter, []unix.Kevent_t{change}, nil, nil); err != nil {
		panic(err)
	}
}

func (s *Server) WaitEvents() {
	events := make([]unix.Kevent_t, 128)
	for {
		n, err := unix.Kevent(s.selecter, nil, events, nil)
		if err != nil {
			if err == unix.EINTR {
				continue
			}
			panic(err)
		}
		for i := 0; i < n; i++ {
			fd := int(events[i].Ident)
			filter := events[i].Filter

			if fd == s.listenFD && filter == unix.EVFILT_READ {
				connFD, _, err := unix.Accept(fd)
				if err != nil {
					continue
				}
				_ = unix.SetNonblock(connFD, true)

				change := unix.Kevent_t{
					Ident:  uint64(connFD),
					Filter: unix.EVFILT_READ,
					Flags:  unix.EV_ADD,
				}
				unix.Kevent(s.selecter, []unix.Kevent_t{change}, nil, nil)

				s.connections[connFD] = &Conn{
					fd:    connFD,
					state: StateHello,
					rfd:   0,
				}

			} else if filter == unix.EVFILT_READ {
				conn, ok := s.connections[fd]
				if !ok {
					unix.Close(fd)
					continue
				}

				buf := make([]byte, 65536)
				nn, err := unix.Read(fd, buf)
				if nn == 0 || (err != nil && errors.Is(err, unix.ECONNRESET)) {
					s.closeConn(conn)
					continue
				}
				if err != nil {
					if errors.Is(err, unix.EAGAIN) || errors.Is(err, unix.EWOULDBLOCK) {
						continue
					}
					s.closeConn(conn)
					continue
				}

				if conn.state == StateHello {
					s.processHello(fd, buf[:nn])
					conn.state = StateRequest
					s.connections[fd] = conn
				} else if conn.state == StateRequest {
					s.processRequest(conn, buf[:nn])
					s.connectToHost(conn)
					s.connections[fd] = conn
				} else if conn.state == StateProxy {
					if conn.fd == fd {
						if conn.rfd <= 0 {
							continue
						}
						if err := writeFull(conn.rfd, buf[:nn]); err != nil {
							s.closeConn(conn)
						}
					} else if conn.rfd == fd {
						if conn.fd <= 0 {
							continue
						}
						if err := writeFull(conn.fd, buf[:nn]); err != nil {
							s.closeConn(conn)
						}
					}
				}

			} else if filter == unix.EVFILT_WRITE {
				conn, ok := s.connections[fd]
				if !ok {
					continue
				}
				if conn.state == StateConnecting && fd == conn.rfd {
					serr, err := unix.GetsockoptInt(fd, unix.SOL_SOCKET, unix.SO_ERROR)
					if err != nil || serr != 0 {
						s.closeConn(conn)
						continue
					}

					s.answerHello(ZERO, conn.fd, nil, 0, nil)

					conn.state = StateProxy
					s.connections[fd] = conn

					change := unix.Kevent_t{
						Ident:  uint64(conn.rfd),
						Filter: unix.EVFILT_READ,
						Flags:  unix.EV_ADD,
					}
					unix.Kevent(s.selecter, []unix.Kevent_t{change}, nil, nil)

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
}

func (s *Server) answerHello(answer byte, fd int, ip *net.IP, port uint16, domain *string) {
	var buf bytes.Buffer
	binary.Write(&buf, binary.BigEndian, byte(FIVE))
	binary.Write(&buf, binary.BigEndian, answer)
	binary.Write(&buf, binary.BigEndian, byte(0)) // RSV

	if ip != nil {
		binary.Write(&buf, binary.BigEndian, byte(ONE))
		ip4 := (*ip).To4()
		if ip4 == nil {
			ip4 = net.IPv4(0, 0, 0, 0).To4()
		}
		buf.Write(ip4)
	} else if domain != nil {
		binary.Write(&buf, binary.BigEndian, byte(THREE))
		binary.Write(&buf, binary.BigEndian, byte(len(*domain)))
		buf.Write([]byte(*domain))
	} else {
		binary.Write(&buf, binary.BigEndian, byte(ONE))
		buf.Write([]byte{0, 0, 0, 0})
	}

	binary.Write(&buf, binary.BigEndian, port)
	_, _ = unix.Write(fd, buf.Bytes())
}

func (s *Server) processRequest(conn *Conn, data []byte) {
	var port uint16
	var domain string

	if len(data) == 0 || len(data) < 7 {
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
		if len(data) < 10 {
			fmt.Println("invalid IPv4 request length")
			return
		}
		ip := net.IP(data[4:8])
		port = binary.BigEndian.Uint16(data[8:10])
		conn.host = ip.String()
		conn.port = port
		fmt.Printf("IP: %s\n", ip.String())
		fmt.Printf("Port: %v\n", port)
	}

	if data[3] == THREE {
		domainLen := int(data[4])
		if 5+domainLen+2 > len(data) {
			fmt.Println("invalid domain request length")
			return
		}
		domain = string(data[5 : 5+domainLen])
		fmt.Printf("Domain: %s\n", domain)
		port = binary.BigEndian.Uint16(data[5+domainLen : 5+domainLen+2])
		conn.host = domain
		conn.port = port
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
	binary.Write(&buf, binary.BigEndian, byte(FIVE))
	binary.Write(&buf, binary.BigEndian, byte(ZERO)) 

	_, _ = unix.Write(fd, buf.Bytes())
}

func (s *Server) connectToHost(conn *Conn) {
	if conn.rfd > 0 {
		return
	}

	rfd, err := unix.Socket(unix.AF_INET, unix.SOCK_STREAM, 0)
	if err != nil {
		return
	}
	_ = unix.SetNonblock(rfd, true)

	ip := net.ParseIP(conn.host)
	if ip == nil {
		addrs, err := net.LookupIP(conn.host)
		if err != nil || len(addrs) == 0 {
			fmt.Printf("Failed to resolve host: %v\n", err)
			unix.Close(rfd)
			return
		}
		ip = addrs[0]
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
	conn.rfd = rfd
	conn.state = StateConnecting

	change := unix.Kevent_t{
		Ident:  uint64(rfd),
		Filter: unix.EVFILT_WRITE,
		Flags:  unix.EV_ADD,
	}
	unix.Kevent(s.selecter, []unix.Kevent_t{change}, nil, nil)

	s.connections[rfd] = conn
}

func (s *Server) closeConn(c *Conn) {
	if c == nil {
		return
	}
	if c.fd > 0 {
		unix.Close(c.fd)
		delete(s.connections, c.fd)
	}
	if c.rfd > 0 {
		unix.Close(c.rfd)
		delete(s.connections, c.rfd)
	}
}

func writeFull(fd int, b []byte) error {
	off := 0
	for off < len(b) {
		n, err := unix.Write(fd, b[off:])
		if n > 0 {
			off += n
			continue
		}
		if err != nil {
			if errors.Is(err, unix.EAGAIN) || errors.Is(err, unix.EWOULDBLOCK) {
				time.Sleep(5 * time.Millisecond)
				continue
			}
			return err
		}
		return fmt.Errorf("write returned 0")
	}
	return nil
}

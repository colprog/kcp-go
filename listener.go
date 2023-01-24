package kcp

import (
	"encoding/binary"
	"hash/crc32"
	"io"
	"net"
	"sync"
	"sync/atomic"
	"time"

	"github.com/pkg/errors"
	"golang.org/x/net/ipv4"
	"golang.org/x/net/ipv6"
)

type (
	// Listener defines a server which will be waiting to accept incoming connections
	Listener struct {
		block        BlockCrypt     // block encryption
		dataShards   int            // FEC data shard
		parityShards int            // FEC parity shard
		conn         net.PacketConn // the underlying packet connection
		ownConn      bool           // true if we created conn internally, false if provided by caller

		sessions        map[string]*UDPSession // all sessions accepted by this Listener
		sessionAlias    map[string]*UDPSession // all sessions accepted by this Listener
		sessionLock     sync.RWMutex
		chAccepts       chan *UDPSession // Listen() backlog
		chSessionClosed chan net.Addr    // session close queue

		die     chan struct{} // notify the listener has closed
		dieOnce sync.Once

		// socket error handling
		socketReadError     atomic.Value
		chSocketReadError   chan struct{}
		socketReadErrorOnce sync.Once

		rd atomic.Value // read deadline for Accept()

		dropKcpAckRate float64
		dropOn         bool

		contollerServer *ControllerServer
	}
)

func (listen *Listener) isDropOpen() bool {
	return listen.dropOn
}

func (listen *Listener) dropOpen() {
	listen.dropOn = true
	for _, session := range listen.sessions {
		session.kcp.setDropRate(listen.dropKcpAckRate)
		session.kcp.dropOpen()
	}
}

func (listen *Listener) dropOff() {
	listen.dropOn = false
	for _, session := range listen.sessions {
		session.kcp.dropOff()
	}
}

// packet input stage
func (l *Listener) packetInput(data []byte, addr net.Addr) {
	decrypted := false
	if l.block != nil && len(data) >= cryptHeaderSize {
		l.block.Decrypt(data, data)
		data = data[nonceSize:]
		checksum := crc32.ChecksumIEEE(data[crcSize:])
		if checksum == binary.LittleEndian.Uint32(data) {
			data = data[crcSize:]
			decrypted = true
		} else {
			atomic.AddUint64(&DefaultSnmp.InCsumErrors, 1)
		}
	} else if l.block == nil {
		decrypted = true
	}

	if decrypted && len(data) >= IKCP_OVERHEAD {
		isFromMeteredIP := false
		l.sessionLock.RLock()
		s, ok := l.sessions[addr.String()]

		if !ok {
			if s, ok = l.sessionAlias[addr.(*net.UDPAddr).IP.String()]; ok {
				addr = s.remote
				isFromMeteredIP = true
			}
		}
		l.sessionLock.RUnlock()

		var conv, sn uint32
		convRecovered := false
		fecFlag := binary.LittleEndian.Uint16(data[4:])
		if fecFlag == typeData || fecFlag == typeParity { // 16bit kcp cmd [81-84] and frg [0-255] will not overlap with FEC type 0x00f1 0x00f2
			// packet with FEC
			if fecFlag == typeData && len(data) >= fecHeaderSizePlus2+IKCP_OVERHEAD {
				conv = binary.LittleEndian.Uint32(data[fecHeaderSizePlus2:])
				sn = binary.LittleEndian.Uint32(data[fecHeaderSizePlus2+IKCP_SN_OFFSET:])
				convRecovered = true
			}
		} else {
			// packet without FEC
			conv = binary.LittleEndian.Uint32(data)
			sn = binary.LittleEndian.Uint32(data[IKCP_SN_OFFSET:])
			convRecovered = true
		}

		if ok { // existing connection
			if !convRecovered || conv == s.kcp.conv { // parity data or valid conversation
				s.kcpInput(data, isFromMeteredIP)
			} else if sn == 0 { // should replace current connection
				s.Close()
				s = nil
			}
		}

		if s == nil && convRecovered { // new session
			if len(l.chAccepts) < cap(l.chAccepts) { // do not let the new sessions overwhelm accept queue
				s := newUDPSession(conv, l.dataShards, l.parityShards, l, l.conn, false, addr, l.block)

				if l.contollerServer != nil {
					s.SetControllerServer(l.contollerServer)
				}
				s.kcpInput(data, isFromMeteredIP)
				l.sessionLock.Lock()
				l.sessions[addr.String()] = s
				l.sessionLock.Unlock()
				l.chAccepts <- s
			}
		}
	}
}

func (l *Listener) notifyReadError(err error) {
	l.socketReadErrorOnce.Do(func() {
		l.socketReadError.Store(err)
		close(l.chSocketReadError)

		// propagate read error to all sessions
		l.sessionLock.RLock()
		for _, s := range l.sessions {
			s.notifyReadError(err)
		}
		l.sessionLock.RUnlock()
	})
}

func (l *Listener) NewControllerConfig(controllerConfig *ControllerServerConfig) (err error) {
	if len(l.sessions) != 0 || len(l.sessionAlias) != 0 {
		return errors.New("already exist session, should create controller server before session in.")
	}
	l.contollerServer = NewSessionControllerServer(controllerConfig, true)
	return nil
}

// SetReadBuffer sets the socket read buffer for the Listener
func (l *Listener) SetReadBuffer(bytes int) error {
	if nc, ok := l.conn.(setReadBuffer); ok {
		return nc.SetReadBuffer(bytes)
	}
	return errInvalidOperation
}

// SetWriteBuffer sets the socket write buffer for the Listener
func (l *Listener) SetWriteBuffer(bytes int) error {
	if nc, ok := l.conn.(setWriteBuffer); ok {
		return nc.SetWriteBuffer(bytes)
	}
	return errInvalidOperation
}

// SetDSCP sets the 6bit DSCP field in IPv4 header, or 8bit Traffic Class in IPv6 header.
//
// if the underlying connection has implemented `func SetDSCP(int) error`, SetDSCP() will invoke
// this function instead.
func (l *Listener) SetDSCP(dscp int) error {
	// interface enabled
	if ts, ok := l.conn.(setDSCP); ok {
		return ts.SetDSCP(dscp)
	}

	if nc, ok := l.conn.(net.Conn); ok {
		var succeed bool
		if err := ipv4.NewConn(nc).SetTOS(dscp << 2); err == nil {
			succeed = true
		}
		if err := ipv6.NewConn(nc).SetTrafficClass(dscp); err == nil {
			succeed = true
		}

		if succeed {
			return nil
		}
	}
	return errInvalidOperation
}

// Accept implements the Accept method in the Listener interface; it waits for the next call and returns a generic Conn.
func (l *Listener) Accept() (net.Conn, error) {
	return l.AcceptKCP()
}

// AcceptKCP accepts a KCP connection
func (l *Listener) AcceptKCP() (*UDPSession, error) {
	var timeout <-chan time.Time
	if tdeadline, ok := l.rd.Load().(time.Time); ok && !tdeadline.IsZero() {
		timeout = time.After(time.Until(tdeadline))
	}

	select {
	case <-timeout:
		return nil, errors.WithStack(errTimeout)
	case c := <-l.chAccepts:
		return c, nil
	case <-l.chSocketReadError:
		return nil, l.socketReadError.Load().(error)
	case <-l.die:
		return nil, errors.WithStack(io.ErrClosedPipe)
	}
}

// SetDeadline sets the deadline associated with the listener. A zero time value disables the deadline.
func (l *Listener) SetDeadline(t time.Time) error {
	l.SetReadDeadline(t)
	l.SetWriteDeadline(t)
	return nil
}

// SetReadDeadline implements the Conn SetReadDeadline method.
func (l *Listener) SetReadDeadline(t time.Time) error {
	l.rd.Store(t)
	return nil
}

// SetWriteDeadline implements the Conn SetWriteDeadline method.
func (l *Listener) SetWriteDeadline(t time.Time) error { return errInvalidOperation }

// Close stops listening on the UDP address, and closes the socket
func (l *Listener) Close() error {
	var once bool
	l.dieOnce.Do(func() {
		close(l.die)
		once = true
	})

	var err error
	if once {
		if l.ownConn {
			err = l.conn.Close()
		}
	} else {
		err = errors.WithStack(io.ErrClosedPipe)
	}
	return err
}

func (l *Listener) AddAlternativeIP(remote net.Addr, s *UDPSession) {
	l.sessionLock.Lock()
	defer l.sessionLock.Unlock()
	l.sessionAlias[remote.(*net.UDPAddr).IP.String()] = s
}

func (l *Listener) AddAlternativeIPForce(remote string, s *UDPSession) {
	l.sessionLock.Lock()
	defer l.sessionLock.Unlock()
	l.sessionAlias[remote] = s
}

// closeSession notify the listener that a session has closed
func (l *Listener) closeSession(remote net.Addr, alternativeIP *net.UDPAddr) (ret bool) {
	l.sessionLock.Lock()
	defer l.sessionLock.Unlock()
	if alternativeIP != nil {
		ip := alternativeIP.IP.String()
		if _, ok := l.sessionAlias[ip]; ok {
			delete(l.sessionAlias, ip)
		}
	}
	if _, ok := l.sessions[remote.String()]; ok {
		delete(l.sessions, remote.String())
		return true
	}
	return false
}

// Addr returns the listener's network address, The Addr returned is shared by all invocations of Addr, so do not modify it.
func (l *Listener) Addr() net.Addr { return l.conn.LocalAddr() }

// Listen listens for incoming KCP packets addressed to the local address laddr on the network "udp",
func Listen(laddr string) (*Listener, error) {
	return ListenWithOptions(laddr, nil, 0, 0, nil, InfoLevelLog)
}

func ListenWithDrop(laddr string, dropRate float64) (*Listener, error) {
	l, err := ListenWithOptions(laddr, nil, 0, 0, nil, DebugLevelLog)
	if l == nil || err != nil {
		return l, err
	}
	l.dropKcpAckRate = dropRate
	l.dropOpen()
	return l, err
}

// ListenWithOptions listens for incoming KCP packets addressed to the local address laddr on the network "udp" with packet encryption.
//
// 'block' is the block encryption algorithm to encrypt packets.
//
// 'dataShards', 'parityShards' specify how many parity packets will be generated following the data packets.
//
// Check https://github.com/klauspost/reedsolomon for details
func ListenWithOptions(laddr string, block BlockCrypt, dataShards, parityShards int, outputFile *string, logLevel int) (*Listener, error) {
	err := LoggerInit(outputFile, logLevel)
	if err != nil {
		return nil, errors.WithStack(err)
	}

	udpaddr, err := net.ResolveUDPAddr("udp", laddr)
	if err != nil {
		return nil, errors.WithStack(err)
	}
	conn, err := net.ListenUDP("udp", udpaddr)
	if err != nil {
		return nil, errors.WithStack(err)
	}

	return serveConn(block, dataShards, parityShards, conn, true)
}

// ServeConn serves KCP protocol for a single packet connection.
func ServeConn(block BlockCrypt, dataShards, parityShards int, conn net.PacketConn) (*Listener, error) {
	return serveConn(block, dataShards, parityShards, conn, false)
}

func serveConn(block BlockCrypt, dataShards, parityShards int, conn net.PacketConn, ownConn bool) (*Listener, error) {
	l := new(Listener)
	l.conn = conn
	l.ownConn = ownConn
	l.sessions = make(map[string]*UDPSession)
	l.sessionAlias = make(map[string]*UDPSession)
	l.chAccepts = make(chan *UDPSession, acceptBacklog)
	l.chSessionClosed = make(chan net.Addr)
	l.die = make(chan struct{})
	l.dataShards = dataShards
	l.parityShards = parityShards
	l.block = block
	l.chSocketReadError = make(chan struct{})
	go l.monitor()
	return l, nil
}

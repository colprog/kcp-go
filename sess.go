// Package kcp-go is a Reliable-UDP library for golang.
//
// This library intents to provide a smooth, resilient, ordered,
// error-checked and anonymous delivery of streams over UDP packets.
//
// The interfaces of this package aims to be compatible with
// net.Conn in standard library, but offers powerful features for advanced users.
package kcp

import (
	"crypto/rand"
	"encoding/binary"
	"fmt"
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

const (
	// 16-bytes nonce for each packet
	nonceSize = 16

	// 4-bytes packet checksum
	crcSize = 4

	// overall crypto header size
	cryptHeaderSize = nonceSize + crcSize

	// maximum packet size
	mtuLimit = 1500

	// accept backlog
	acceptBacklog = 128
)

var (
	errInvalidOperation = errors.New("invalid operation")
	errTimeout          = errors.New("timeout")
)

var (
	// a system-wide packet buffer shared among sending, receiving and FEC
	// to mitigate high-frequency memory allocation for packets, bytes from xmitBuf
	// is aligned to 64bit
	xmitBuf sync.Pool
)

const (
	SessionTypeOnlyMeteredMin int32 = SessionTypeNormal

	SessionTypeNormal       int32 = 0
	SessionTypeExistMetered int32 = 1
	SessionTypeOnlyMetered  int32 = 2

	SessionTypeOnlyMeteredMax int32 = SessionTypeOnlyMetered
)

var (
	globalSessionType int32 = SessionTypeNormal
	// no need use atomic value
	GlobalMontorChannel bool = false

	GlobalEnableRetryTimes bool = true
)

func RunningAsNormal() {
	atomic.StoreInt32(&globalSessionType, SessionTypeNormal)
	LogInfo("current session running as SessionTypeNormal")
}

func RunningAsExistMetered() {
	atomic.StoreInt32(&globalSessionType, SessionTypeExistMetered)
	LogInfo("current session running as SessionTypeExistMetered")
}

func RunningAsOnlyMetered() {
	atomic.StoreInt32(&globalSessionType, SessionTypeOnlyMetered)
	LogInfo("current session running as SessionTypeOnlyMetered")
}

func SessionTypeDealImportPackage(shouldAddToMeteredQ bool, existMeterRoute bool) bool {

	if globalSessionType == SessionTypeNormal {
		shouldAddToMeteredQ = false
	} else if globalSessionType == SessionTypeOnlyMetered && existMeterRoute {
		shouldAddToMeteredQ = true
	}

	// if globalSessionType == SessionTypeExistMetered
	// Then shouldAddToMeteredQ should not be changed

	return shouldAddToMeteredQ
}

func init() {
	xmitBuf.New = func() interface{} {
		return make([]byte, mtuLimit)
	}
}

type (
	// UDPSession defines a KCP session implemented by UDP
	UDPSession struct {
		conn    net.PacketConn // the underlying packet connection
		ownConn bool           // true if we created conn internally, false if provided by caller
		kcp     *KCP           // KCP ARQ protocol
		l       *Listener      // pointing to the Listener object if it's been accepted by a Listener
		block   BlockCrypt     // block encryption object

		// kcp receiving is based on packets
		// recvbuf turns packets into stream
		recvbuf []byte
		bufptr  []byte

		// FEC codec
		fecDecoder *fecDecoder
		fecEncoder *fecEncoder

		// settings
		remote     net.Addr  // remote peer address
		rd         time.Time // read deadline
		wd         time.Time // write deadline
		headerSize int       // the header size additional to a KCP frame
		ackNoDelay bool      // send ack immediately for each incoming packet(testing purpose)
		writeDelay bool      // delay kcp.flush() for Write() for bulk transfer
		dup        int       // duplicate udp packets(testing purpose)

		// notifications
		die          chan struct{} // notify current session has Closed
		dieOnce      sync.Once
		chReadEvent  chan struct{} // notify Read() can be called without blocking
		chWriteEvent chan struct{} // notify Write() can be called without blocking

		// socket error handling
		socketReadError      atomic.Value
		socketWriteError     atomic.Value
		chSocketReadError    chan struct{}
		chSocketWriteError   chan struct{}
		socketReadErrorOnce  sync.Once
		socketWriteErrorOnce sync.Once

		// grpc controller server
		controller *SessionController

		// nonce generator
		nonce Entropy

		// packets waiting to be sent on wire
		txqueue         []ipv4.Message
		xconn           batchConn // for x/net
		xconnWriteError error

		// expensive but stable conn that we can fallback to
		meteredTxqueue []ipv4.Message
		meteredRemote  *net.UDPAddr

		mu sync.Mutex
	}

	UDPSessionMonitor struct {
		lastSegmentAcked         uint64
		lastSegmentPromotedAcked uint64
	}

	setReadBuffer interface {
		SetReadBuffer(bytes int) error
	}

	setWriteBuffer interface {
		SetWriteBuffer(bytes int) error
	}

	setDSCP interface {
		SetDSCP(int) error
	}
)

func castToBatchConn(conn net.PacketConn) batchConn {
	if conn == nil {
		return nil
	}

	if _, ok := conn.(*net.UDPConn); ok {
		addr, err := net.ResolveUDPAddr("udp", conn.LocalAddr().String())
		if err == nil {
			if addr.IP.To4() != nil {
				return ipv4.NewPacketConn(conn)
			} else {
				return ipv6.NewPacketConn(conn)
			}
		}
	}
	return nil
}

// newUDPSession create a new udp session for client or server
func newUDPSession(conv uint32, dataShards, parityShards int, l *Listener, conn net.PacketConn, ownConn bool, remote net.Addr, block BlockCrypt) *UDPSession {
	sess := new(UDPSession)
	sess.die = make(chan struct{})
	sess.nonce = new(nonceAES128)
	sess.nonce.Init()
	sess.chReadEvent = make(chan struct{}, 1)
	sess.chWriteEvent = make(chan struct{}, 1)
	sess.chSocketReadError = make(chan struct{})
	sess.chSocketWriteError = make(chan struct{})
	sess.remote = remote
	sess.conn = conn
	sess.ownConn = ownConn
	sess.meteredRemote = nil
	sess.l = l
	sess.block = block
	sess.recvbuf = make([]byte, mtuLimit)

	// cast to writebatch conn
	sess.xconn = castToBatchConn(conn)

	// FEC codec initialization
	sess.fecDecoder = newFECDecoder(dataShards, parityShards)
	if sess.block != nil {
		sess.fecEncoder = newFECEncoder(dataShards, parityShards, cryptHeaderSize)
	} else {
		sess.fecEncoder = newFECEncoder(dataShards, parityShards, 0)
	}

	// calculate additional header size introduced by FEC and encryption
	if sess.block != nil {
		sess.headerSize += cryptHeaderSize
	}
	if sess.fecEncoder != nil {
		sess.headerSize += fecHeaderSizePlus2
	}

	if l != nil {
		sess.kcp = NewKCPWithDrop(conv, func(buf []byte, size int, important bool, retryTimes uint32) {
			if size >= IKCP_OVERHEAD+sess.headerSize {
				sess.output(buf[:size], important, retryTimes)
			}
		}, l.dropKcpAckRate, l.dropOn)
	} else {
		sess.kcp = NewKCP(conv, func(buf []byte, size int, important bool, retryTimes uint32) {
			if size >= IKCP_OVERHEAD+sess.headerSize {
				sess.output(buf[:size], important, retryTimes)
			}
		})
	}

	sess.kcp.ReserveBytes(sess.headerSize)

	if sess.l == nil { // it's a client connection
		go sess.readLoop()
		atomic.AddUint64(&DefaultSnmp.ActiveOpens, 1)
	} else {
		atomic.AddUint64(&DefaultSnmp.PassiveOpens, 1)
	}

	// start per-session updater
	SystemTimedSched.Put(sess.update, time.Now())

	currestab := atomic.AddUint64(&DefaultSnmp.CurrEstab, 1)
	maxconn := atomic.LoadUint64(&DefaultSnmp.MaxConn)
	if currestab > maxconn {
		atomic.CompareAndSwapUint64(&DefaultSnmp.MaxConn, maxconn, currestab)
	}

	return sess
}

func (sess *UDPSession) SetSessionController(controller *SessionController) {
	sess.controller = controller
}

// Read implements net.Conn
func (s *UDPSession) Read(b []byte) (n int, err error) {
	for {
		s.mu.Lock()
		if len(s.bufptr) > 0 { // copy from buffer into b
			n = copy(b, s.bufptr)
			s.bufptr = s.bufptr[n:]
			s.mu.Unlock()
			atomic.AddUint64(&DefaultSnmp.BytesReceived, uint64(n))
			return n, nil
		}

		if size := s.kcp.PeekSize(); size > 0 { // peek data size from kcp
			if len(b) >= size { // receive data into 'b' directly
				s.kcp.Recv(b)
				s.mu.Unlock()
				atomic.AddUint64(&DefaultSnmp.BytesReceived, uint64(size))
				return size, nil
			}

			// if necessary resize the stream buffer to guarantee a sufficient buffer space
			if cap(s.recvbuf) < size {
				s.recvbuf = make([]byte, size)
			}

			// resize the length of recvbuf to correspond to data size
			s.recvbuf = s.recvbuf[:size]
			s.kcp.Recv(s.recvbuf)
			n = copy(b, s.recvbuf)   // copy to 'b'
			s.bufptr = s.recvbuf[n:] // pointer update
			s.mu.Unlock()
			atomic.AddUint64(&DefaultSnmp.BytesReceived, uint64(n))
			return n, nil
		}

		// deadline for current reading operation
		var timeout *time.Timer
		var c <-chan time.Time
		if !s.rd.IsZero() {
			if time.Now().After(s.rd) {
				s.mu.Unlock()
				return 0, errors.WithStack(errTimeout)
			}

			delay := time.Until(s.rd)
			timeout = time.NewTimer(delay)
			c = timeout.C
		}
		s.mu.Unlock()

		// wait for read event or timeout or error
		select {
		case <-s.chReadEvent:
			if timeout != nil {
				timeout.Stop()
			}
		case <-c:
			return 0, errors.WithStack(errTimeout)
		case <-s.chSocketReadError:
			return 0, s.socketReadError.Load().(error)
		case <-s.die:
			return 0, errors.WithStack(io.ErrClosedPipe)
		}
	}
}

// Write implements net.Conn
func (s *UDPSession) Write(b []byte) (n int, err error) { return s.WriteBuffers([][]byte{b}) }

// WriteBuffers write a vector of byte slices to the underlying connection
func (s *UDPSession) WriteBuffers(v [][]byte) (n int, err error) {

	for {
		select {
		case <-s.chSocketWriteError:
			return 0, s.socketWriteError.Load().(error)
		case <-s.die:
			return 0, errors.WithStack(io.ErrClosedPipe)
		default:
		}

		s.mu.Lock()

		// make sure write do not overflow the max sliding window on both side
		waitsnd := s.kcp.WaitSnd()
		if waitsnd < int(s.kcp.snd_wnd) && waitsnd < int(s.kcp.rmt_wnd) {
			for _, b := range v {
				n += len(b)
				for {
					if len(b) <= int(s.kcp.mss) {
						s.kcp.Send(b)
						break
					} else {
						s.kcp.Send(b[:s.kcp.mss])
						b = b[s.kcp.mss:]
					}
				}
			}

			waitsnd = s.kcp.WaitSnd()
			if waitsnd >= int(s.kcp.snd_wnd) || waitsnd >= int(s.kcp.rmt_wnd) || !s.writeDelay {
				s.kcp.flush(false)
				s.uncork()
			}
			s.mu.Unlock()
			atomic.AddUint64(&DefaultSnmp.BytesSent, uint64(n))
			return n, nil
		}

		var timeout *time.Timer
		var c <-chan time.Time
		if !s.wd.IsZero() {
			if time.Now().After(s.wd) {
				s.mu.Unlock()
				return 0, errors.WithStack(errTimeout)
			}
			delay := time.Until(s.wd)
			timeout = time.NewTimer(delay)
			c = timeout.C
		}
		s.mu.Unlock()

		select {
		case <-s.chWriteEvent:
			if timeout != nil {
				timeout.Stop()
			}
		case <-c:
			return 0, errors.WithStack(errTimeout)
		case <-s.chSocketWriteError:
			return 0, s.socketWriteError.Load().(error)
		case <-s.die:
			return 0, errors.WithStack(io.ErrClosedPipe)
		}
	}
}

// uncork sends data in txqueue if there is any
func (s *UDPSession) uncork() {
	if len(s.txqueue) > 0 {
		s.tx()
		// recycle
		for k := range s.meteredTxqueue {
			s.meteredTxqueue[k].Buffers = nil
		}
		s.meteredTxqueue = s.meteredTxqueue[:0]
		for k := range s.txqueue {
			xmitBuf.Put(s.txqueue[k].Buffers[0])
			s.txqueue[k].Buffers = nil
		}
		s.txqueue = s.txqueue[:0]
	}
}

// Close closes the connection.
func (s *UDPSession) Close() error {
	var once bool
	s.dieOnce.Do(func() {
		close(s.die)
		once = true
	})

	if once {
		atomic.AddUint64(&DefaultSnmp.CurrEstab, ^uint64(0))

		// try best to send all queued messages
		s.mu.Lock()
		s.kcp.flush(false)
		s.uncork()
		// release pending segments
		s.kcp.ReleaseTX()
		if s.fecDecoder != nil {
			s.fecDecoder.release()
		}
		s.mu.Unlock()

		if s.l != nil { // belongs to listener
			s.l.closeSession(s.remote, s.meteredRemote)
			return nil
		} else if s.ownConn { // client socket close
			return s.conn.Close()
		} else {
			return nil
		}
	} else {
		return errors.WithStack(io.ErrClosedPipe)
	}
}

// LocalAddr returns the local network address. The Addr returned is shared by all invocations of LocalAddr, so do not modify it.
func (s *UDPSession) LocalAddr() net.Addr { return s.conn.LocalAddr() }

// RemoteAddr returns the remote network address. The Addr returned is shared by all invocations of RemoteAddr, so do not modify it.
func (s *UDPSession) RemoteAddr() net.Addr { return s.remote }

// SetDeadline sets the deadline associated with the listener. A zero time value disables the deadline.
func (s *UDPSession) SetDeadline(t time.Time) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.rd = t
	s.wd = t
	s.notifyReadEvent()
	s.notifyWriteEvent()
	return nil
}

// SetReadDeadline implements the Conn SetReadDeadline method.
func (s *UDPSession) SetReadDeadline(t time.Time) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.rd = t
	s.notifyReadEvent()
	return nil
}

// SetWriteDeadline implements the Conn SetWriteDeadline method.
func (s *UDPSession) SetWriteDeadline(t time.Time) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.wd = t
	s.notifyWriteEvent()
	return nil
}

// SetWriteDelay delays write for bulk transfer until the next update interval
func (s *UDPSession) SetWriteDelay(delay bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.writeDelay = delay
}

// SetWindowSize set maximum window size
func (s *UDPSession) SetWindowSize(sndwnd, rcvwnd int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.kcp.WndSize(sndwnd, rcvwnd)
}

// SetMtu sets the maximum transmission unit(not including UDP header)
func (s *UDPSession) SetMtu(mtu int) bool {
	if mtu > mtuLimit {
		return false
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	s.kcp.SetMtu(mtu)
	return true
}

// SetStreamMode toggles the stream mode on/off
func (s *UDPSession) SetStreamMode(enable bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if enable {
		s.kcp.stream = 1
	} else {
		s.kcp.stream = 0
	}
}

// SetACKNoDelay changes ack flush option, set true to flush ack immediately,
func (s *UDPSession) SetACKNoDelay(nodelay bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.ackNoDelay = nodelay
}

// (deprecated)
//
// SetDUP duplicates udp packets for kcp output.
func (s *UDPSession) SetDUP(dup int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.dup = dup
}

// SetNoDelay calls nodelay() of kcp
// https://github.com/skywind3000/kcp/blob/master/README.en.md#protocol-configuration
func (s *UDPSession) SetNoDelay(nodelay, interval, resend, nc int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.kcp.NoDelay(nodelay, interval, resend, nc)
}

// SetDSCP sets the 6bit DSCP field in IPv4 header, or 8bit Traffic Class in IPv6 header.
//
// if the underlying connection has implemented `func SetDSCP(int) error`, SetDSCP() will invoke
// this function instead.
//
// It has no effect if it's accepted from Listener.
func (s *UDPSession) SetDSCP(dscp int) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.l != nil {
		return errInvalidOperation
	}

	// interface enabled
	if ts, ok := s.conn.(setDSCP); ok {
		return ts.SetDSCP(dscp)
	}

	if nc, ok := s.conn.(net.Conn); ok {
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

// SetReadBuffer sets the socket read buffer, no effect if it's accepted from Listener
func (s *UDPSession) SetReadBuffer(bytes int) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.l == nil {
		if nc, ok := s.conn.(setReadBuffer); ok {
			return nc.SetReadBuffer(bytes)
		}
	}
	return errInvalidOperation
}

// SetWriteBuffer sets the socket write buffer, no effect if it's accepted from Listener
func (s *UDPSession) SetWriteBuffer(bytes int) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.l == nil {
		if nc, ok := s.conn.(setWriteBuffer); ok {
			return nc.SetWriteBuffer(bytes)
		}
	}
	return errInvalidOperation
}

// post-processing for sending a packet from kcp core
// steps:
// 1. FEC packet generation
// 2. CRC32 integrity
// 3. Encryption
// 4. TxQueue
func (s *UDPSession) output(buf []byte, important bool, retryTimes uint32) {
	var ecc [][]byte

	if !GlobalEnableRetryTimes {
		retryTimes = 0
	}

	// 1. FEC encoding
	if s.fecEncoder != nil {
		ecc = s.fecEncoder.encode(buf)
	}

	// 2&3. crc32 & encryption
	if s.block != nil {
		s.nonce.Fill(buf[:nonceSize])
		checksum := crc32.ChecksumIEEE(buf[cryptHeaderSize:])
		binary.LittleEndian.PutUint32(buf[nonceSize:], checksum)
		s.block.Encrypt(buf, buf)

		for k := range ecc {
			s.nonce.Fill(ecc[k][:nonceSize])
			checksum := crc32.ChecksumIEEE(ecc[k][cryptHeaderSize:])
			binary.LittleEndian.PutUint32(ecc[k][nonceSize:], checksum)
			s.block.Encrypt(ecc[k], ecc[k])
		}
	}

	shouldAddToMeteredQ := important && s.meteredRemote != nil
	shouldAddToMeteredQ = SessionTypeDealImportPackage(shouldAddToMeteredQ, s.meteredRemote != nil)

	// if current package added to meter, should not puts it into retry queue.
	if shouldAddToMeteredQ {
		retryTimes = 0
	}

	// 4. TxQueue
	var msg ipv4.Message
	for i := 0; i < s.dup+1; i++ {
		length := len(buf)
		bts := xmitBuf.Get().([]byte)[:length]
		copy(bts, buf)

		msg.Buffers = [][]byte{bts}
		msg.Addr = s.remote
		s.txqueue = append(s.txqueue, msg)

		if globalSessionType == SessionTypeExistMetered {
			// If ack here, retryTimes will always be 0
			for i := 1; uint32(i) < (retryTimes + 1); i++ {
				var msg_dump ipv4.Message
				bts_dump := make([]byte, length)
				copy(bts_dump, buf)
				msg_dump.Buffers = [][]byte{bts_dump}
				msg_dump.Addr = s.remote
				s.txqueue = append(s.txqueue, msg_dump)
			}
		}

		atomic.AddUint64(&DefaultSnmp.BytesSentFromNoMeteredRaw, uint64(length))
		if shouldAddToMeteredQ {
			msg.Addr = s.meteredRemote
			s.meteredTxqueue = append(s.meteredTxqueue, msg)
			atomic.AddUint64(&DefaultSnmp.BytesSentFromMeteredRaw, uint64(length))
		}
	}

	for k := range ecc {
		bts := xmitBuf.Get().([]byte)[:len(ecc[k])]
		copy(bts, ecc[k])
		msg.Buffers = [][]byte{bts}
		msg.Addr = s.remote
		s.txqueue = append(s.txqueue, msg)
		if shouldAddToMeteredQ {
			msg.Addr = s.meteredRemote
			s.meteredTxqueue = append(s.meteredTxqueue, msg)
		}
	}
}

// sess update to trigger protocol
func (s *UDPSession) update() {
	select {
	case <-s.die:
	default:
		s.mu.Lock()
		interval := s.kcp.flush(false)
		waitsnd := s.kcp.WaitSnd()
		if waitsnd < int(s.kcp.snd_wnd) && waitsnd < int(s.kcp.rmt_wnd) {
			s.notifyWriteEvent()
		}
		s.uncork()
		s.mu.Unlock()
		// self-synchronized timed scheduling
		SystemTimedSched.Put(s.update, time.Now().Add(time.Duration(interval)*time.Millisecond))
	}
}

// GetConv gets conversation id of a session
func (s *UDPSession) GetConv() uint32 { return s.kcp.conv }

// GetRTO gets current rto of the session
func (s *UDPSession) GetRTO() uint32 {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.kcp.rx_rto
}

// GetSRTT gets current srtt of the session
func (s *UDPSession) GetSRTT() int32 {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.kcp.rx_srtt
}

// GetRTTVar gets current rtt variance of the session
func (s *UDPSession) GetSRTTVar() int32 {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.kcp.rx_rttvar
}

func (s *UDPSession) notifyReadEvent() {
	select {
	case s.chReadEvent <- struct{}{}:
	default:
	}
}

func (s *UDPSession) notifyWriteEvent() {
	select {
	case s.chWriteEvent <- struct{}{}:
	default:
	}
}

func (s *UDPSession) notifyReadError(err error) {
	s.socketReadErrorOnce.Do(func() {
		s.socketReadError.Store(err)
		close(s.chSocketReadError)
	})
}

func (s *UDPSession) notifyWriteError(err error) {
	s.socketWriteErrorOnce.Do(func() {
		s.socketWriteError.Store(err)
		close(s.chSocketWriteError)
	})
}

// packet input stage
func (s *UDPSession) packetInput(data []byte, isFromMeteredIP bool) {
	decrypted := false
	if s.block != nil && len(data) >= cryptHeaderSize {
		s.block.Decrypt(data, data)
		data = data[nonceSize:]
		checksum := crc32.ChecksumIEEE(data[crcSize:])
		if checksum == binary.LittleEndian.Uint32(data) {
			data = data[crcSize:]
			decrypted = true
		} else {
			atomic.AddUint64(&DefaultSnmp.InCsumErrors, 1)
		}
	} else if s.block == nil {
		decrypted = true
	}

	if decrypted && len(data) >= IKCP_OVERHEAD {
		s.kcpInput(data, isFromMeteredIP)
	}
}

func (s *UDPSession) kcpInput(data []byte, isFromMeteredIP bool) {
	var kcpInErrors, fecErrs, fecRecovered, fecParityShards uint64

	fecFlag := binary.LittleEndian.Uint16(data[4:])
	if fecFlag == typeData || fecFlag == typeParity { // 16bit kcp cmd [81-84] and frg [0-255] will not overlap with FEC type 0x00f1 0x00f2
		if len(data) >= fecHeaderSizePlus2 {
			f := fecPacket(data)
			if f.flag() == typeParity {
				fecParityShards++
			}

			// lock
			s.mu.Lock()
			// if fecDecoder is not initialized, create one with default parameter
			if s.fecDecoder == nil {
				s.fecDecoder = newFECDecoder(1, 1)
			}
			recovers := s.fecDecoder.decode(f)
			if f.flag() == typeData {
				if ret := s.kcp.Input(data[fecHeaderSizePlus2:], true, isFromMeteredIP, s.ackNoDelay); ret != 0 {
					kcpInErrors++
				}
			}

			for _, r := range recovers {
				if len(r) >= 2 { // must be larger than 2bytes
					sz := binary.LittleEndian.Uint16(r)
					if int(sz) <= len(r) && sz >= 2 {
						if ret := s.kcp.Input(r[2:sz], false, isFromMeteredIP, s.ackNoDelay); ret == 0 {
							fecRecovered++
						} else {
							kcpInErrors++
						}
					} else {
						fecErrs++
					}
				} else {
					fecErrs++
				}
				// recycle the recovers
				xmitBuf.Put(r)
			}

			// to notify the readers to receive the data
			if n := s.kcp.PeekSize(); n > 0 {
				s.notifyReadEvent()
			}
			// to notify the writers
			waitsnd := s.kcp.WaitSnd()
			if waitsnd < int(s.kcp.snd_wnd) && waitsnd < int(s.kcp.rmt_wnd) {
				s.notifyWriteEvent()
			}

			s.uncork()
			s.mu.Unlock()
		} else {
			atomic.AddUint64(&DefaultSnmp.InErrs, 1)
		}
	} else {
		s.mu.Lock()
		if ret := s.kcp.Input(data, true, isFromMeteredIP, s.ackNoDelay); ret != 0 {
			kcpInErrors++
		}
		if n := s.kcp.PeekSize(); n > 0 {
			s.notifyReadEvent()
		}
		waitsnd := s.kcp.WaitSnd()
		if waitsnd < int(s.kcp.snd_wnd) && waitsnd < int(s.kcp.rmt_wnd) {
			s.notifyWriteEvent()
		}
		s.uncork()
		s.mu.Unlock()
	}

	atomic.AddUint64(&DefaultSnmp.InPkts, 1)
	atomic.AddUint64(&DefaultSnmp.InBytes, uint64(len(data)))
	if fecParityShards > 0 {
		atomic.AddUint64(&DefaultSnmp.FECParityShards, fecParityShards)
	}
	if kcpInErrors > 0 {
		atomic.AddUint64(&DefaultSnmp.KCPInErrors, kcpInErrors)
	}
	if fecErrs > 0 {
		atomic.AddUint64(&DefaultSnmp.FECErrs, fecErrs)
	}
	if fecRecovered > 0 {
		atomic.AddUint64(&DefaultSnmp.FECRecovered, fecRecovered)
	}

}

func (s *UDPSession) SetMeteredAddr(raddr string, port uint16, force bool) error {
	if raddr != "" {
		s.mu.Lock()
		defer s.mu.Unlock()

		remoteAddr := ""
		if port == 0 {
			remoteAddr = fmt.Sprintf("%s:%d", raddr, s.remote.(*net.UDPAddr).Port)
		} else {
			remoteAddr = fmt.Sprintf("%s:%d", raddr, port)
		}
		addr, err := net.ResolveUDPAddr("udp", remoteAddr)

		if err != nil {
			return err
		}

		s.meteredRemote = addr

		if s.l == nil {
			// If no listener, just return.
			return nil
		}

		if force {
			s.l.AddAlternativeIPForce(raddr, s)
		} else {
			s.l.AddAlternativeIP(addr, s)
		}

	} else {
		return errors.New("invalid raddr")
	}

	return nil
}

func (s *UDPSession) GetMeteredAddr() *net.UDPAddr {
	return s.meteredRemote
}

func (s *UDPSession) EnableMonitor(interval uint64, detectRate float64) {

	if s.meteredRemote == nil {
		LogWarn("Without meter ip assigned.")
		RunningAsNormal()
	} else {
		RunningAsExistMetered()
	}

	if GlobalMontorChannel {
		LogWarn("Monitor already started.")
	} else {
		LogInfo("Enabled monitor, monitor stared. interval=%d,detectRate=%f. global session type is %d \n", interval, detectRate, globalSessionType)
		GlobalMontorChannel = true
		go MonitorStart(s, interval, detectRate, s.controller)
	}
}

// Dial connects to the remote address "raddr" on the network "udp" without encryption and FEC
func Dial(raddr string) (*UDPSession, error) {
	return DialWithOptions(raddr, nil, 0, 0)
}

func DialWithDrop(raddr string, dropRate float64) (*UDPSession, error) {
	s, err := DialWithDetailOptions(raddr, nil, 0, 0, nil, DebugLevelLog)
	s.kcp.setDropRate(dropRate)
	s.kcp.dropOpen()
	return s, err
}

// DialWithOptions connects to the remote address "raddr" on the network "udp" with packet encryption
//
// 'block' is the block encryption algorithm to encrypt packets.
//
// 'dataShards', 'parityShards' specify how many parity packets will be generated following the data packets.
//
// Check https://github.com/klauspost/reedsolomon for details
func DialWithOptions(raddr string, block BlockCrypt, dataShards, parityShards int) (*UDPSession, error) {
	return DialWithDetailOptions(raddr, block, dataShards, parityShards, nil, InfoLevelLog)
}

func DialWithDetailOptions(raddr string, block BlockCrypt, dataShards, parityShards int, outputFile *string, logLevel int) (*UDPSession, error) {
	err := LoggerInit(outputFile, logLevel)
	if err != nil {
		return nil, errors.WithStack(err)
	}

	udpaddr, err := net.ResolveUDPAddr("udp", raddr)
	if err != nil {
		return nil, errors.WithStack(err)
	}

	conn, err := listenAt(raddr)
	if err != nil {
		return nil, errors.WithStack(err)
	}

	var convid uint32
	binary.Read(rand.Reader, binary.LittleEndian, &convid)
	return newUDPSession(convid, dataShards, parityShards, nil, conn, true, udpaddr, block), nil
}

func listenAt(raddr string) (*net.UDPConn, error) {
	// network type detection
	udpaddr, err := net.ResolveUDPAddr("udp", raddr)
	if err != nil {
		return nil, errors.WithStack(err)
	}
	network := "udp4"
	if udpaddr.IP.To4() == nil {
		network = "udp"
	}

	return net.ListenUDP(network, nil)
}

// NewConn3 establishes a session and talks KCP protocol over a packet connection.
func NewConn3(convid uint32, raddr net.Addr, block BlockCrypt, dataShards, parityShards int, conn net.PacketConn) (*UDPSession, error) {
	return newUDPSession(convid, dataShards, parityShards, nil, conn, false, raddr, block), nil
}

// NewConn2 establishes a session and talks KCP protocol over a packet connection.
func NewConn2(raddr net.Addr, block BlockCrypt, dataShards, parityShards int, conn net.PacketConn) (*UDPSession, error) {
	var convid uint32
	binary.Read(rand.Reader, binary.LittleEndian, &convid)
	return NewConn3(convid, raddr, block, dataShards, parityShards, conn)
}

// NewConn establishes a session and talks KCP protocol over a packet connection.
func NewConn(raddr string, block BlockCrypt, dataShards, parityShards int, conn net.PacketConn) (*UDPSession, error) {
	udpaddr, err := net.ResolveUDPAddr("udp", raddr)
	if err != nil {
		return nil, errors.WithStack(err)
	}
	return NewConn2(udpaddr, block, dataShards, parityShards, conn)
}

package kcp

import (
	"encoding/binary"
	"fmt"
	"math/rand"
	"sync"
	"sync/atomic"
	"time"

	"github.com/pkg/errors"
)

const (
	IKCP_RTO_NDL       = 30  // no delay min rto
	IKCP_RTO_MIN       = 100 // normal min rto
	IKCP_RTO_DEF       = 200
	IKCP_RTO_MAX       = 60000
	IKCP_CMD_PUSH      = 81 // cmd: push data
	IKCP_CMD_ACK       = 82 // cmd: ack
	IKCP_CMD_WASK      = 83 // cmd: window probe (ask)
	IKCP_CMD_WINS      = 84 // cmd: window size (tell)
	IKCP_ASK_SEND      = 1  // need to send IKCP_CMD_WASK
	IKCP_ASK_TELL      = 2  // need to send IKCP_CMD_WINS
	IKCP_WND_SND       = 32
	IKCP_WND_RCV       = 32
	IKCP_MTU_DEF       = 1400
	IKCP_ACK_FAST      = 3
	IKCP_INTERVAL      = 100
	IKCP_OVERHEAD      = 24
	IKCP_DEADLINK      = 20
	IKCP_THRESH_INIT   = 2
	IKCP_THRESH_MIN    = 2
	IKCP_PROBE_INIT    = 7000   // 7 secs to probe window size
	IKCP_PROBE_LIMIT   = 120000 // up to 120 secs to probe window
	IKCP_SN_OFFSET     = 12
	IKCP_FASTACK_LIMIT = 8

	IKCP_ALIVE_DETECTION   = 17
	IKCP_ALIVE_DETECT_HEAD = 4
)

// monotonic reference time point
var refTime time.Time = time.Now()

// currentMs returns current elapsed monotonic milliseconds since program startup
func currentMs() uint32 { return uint32(time.Since(refTime) / time.Millisecond) }

// output_callback is a prototype which ought capture conn and call conn.Write
type output_callback func(buf []byte, size int, important bool, retryTimes uint32)

/* encode 8 bits unsigned int */
func ikcp_encode8u(p []byte, c byte) []byte {
	p[0] = c
	return p[1:]
}

/* decode 8 bits unsigned int */
func ikcp_decode8u(p []byte, c *byte) []byte {
	*c = p[0]
	return p[1:]
}

/* encode 16 bits unsigned int (lsb) */
func ikcp_encode16u(p []byte, w uint16) []byte {
	binary.LittleEndian.PutUint16(p, w)
	return p[2:]
}

/* decode 16 bits unsigned int (lsb) */
func ikcp_decode16u(p []byte, w *uint16) []byte {
	*w = binary.LittleEndian.Uint16(p)
	return p[2:]
}

/* encode 32 bits unsigned int (lsb) */
func ikcp_encode32u(p []byte, l uint32) []byte {
	binary.LittleEndian.PutUint32(p, l)
	return p[4:]
}

/* decode 32 bits unsigned int (lsb) */
func ikcp_decode32u(p []byte, l *uint32) []byte {
	*l = binary.LittleEndian.Uint32(p)
	return p[4:]
}

func _imin_(a, b uint32) uint32 {
	if a <= b {
		return a
	}
	return b
}

func _imax_(a, b uint32) uint32 {
	if a >= b {
		return a
	}
	return b
}

func _ibound_(lower, middle, upper uint32) uint32 {
	return _imin_(_imax_(lower, middle), upper)
}

func _itimediff(later, earlier uint32) int32 {
	return (int32)(later - earlier)
}

// segment defines a KCP segment
type segment struct {
	conv     uint32
	cmd      uint8
	frg      uint8
	wnd      uint16
	ts       uint32
	sn       uint32
	una      uint32
	rto      uint32
	xmit     uint32
	resendts uint32
	fastack  uint32
	acked    uint32 // mark if the seg has acked
	data     []byte

	// No need encoded.
	has_promote bool
}

// encode a segment into buffer
func (seg *segment) encode(ptr []byte) []byte {
	ptr = ikcp_encode32u(ptr, seg.conv)
	ptr = ikcp_encode8u(ptr, seg.cmd)
	ptr = ikcp_encode8u(ptr, seg.frg)
	ptr = ikcp_encode16u(ptr, seg.wnd)
	ptr = ikcp_encode32u(ptr, seg.ts)
	ptr = ikcp_encode32u(ptr, seg.sn)
	ptr = ikcp_encode32u(ptr, seg.una)
	ptr = ikcp_encode32u(ptr, uint32(len(seg.data)))
	atomic.AddUint64(&DefaultSnmp.OutSegs, 1)
	return ptr
}

// KCP defines a single KCP connection
type KCP struct {
	conv, mtu, mss, state                  uint32
	snd_una, snd_nxt, rcv_nxt              uint32
	ssthresh                               uint32
	rx_rttvar, rx_srtt                     int32
	rx_rto, rx_minrto                      uint32
	initial_tx_rto                         uint32
	snd_wnd, rcv_wnd, rmt_wnd, cwnd, probe uint32
	interval, ts_flush                     uint32
	nodelay, updated                       uint32
	ts_probe, probe_wait                   uint32
	dead_link, incr                        uint32

	send_all_acks_through_metered_ip bool
	metered_ip_aggressiveness        int

	fastresend     int32
	fastacklimit   int32
	nocwnd, stream int32

	snd_queue []segment
	rcv_queue []segment
	snd_buf   []segment
	rcv_buf   []segment

	acklist []ackItem

	buffer   []byte
	reserved int
	output   output_callback

	// drop
	dropRate float64
	dropOn   bool

	SessionType *int32
}

type ackItem struct {
	sn uint32
	ts uint32
}

// NewKCP create a new kcp state machine
//
// 'conv' must be equal in the connection peers, or else data will be silently rejected.
//
// 'output' function will be called whenever these is data to be sent on wire.
func NewKCP(conv uint32, sessionType *int32, output output_callback) *KCP {
	kcp := new(KCP)
	kcp.conv = conv
	kcp.snd_wnd = IKCP_WND_SND
	kcp.rcv_wnd = IKCP_WND_RCV
	kcp.rmt_wnd = IKCP_WND_RCV
	kcp.mtu = IKCP_MTU_DEF
	kcp.mss = kcp.mtu - IKCP_OVERHEAD
	kcp.buffer = make([]byte, kcp.mtu)
	kcp.rx_rto = IKCP_RTO_DEF
	kcp.rx_minrto = IKCP_RTO_MIN
	kcp.interval = IKCP_INTERVAL
	kcp.ts_flush = IKCP_INTERVAL
	kcp.ssthresh = IKCP_THRESH_INIT
	kcp.dead_link = IKCP_DEADLINK
	kcp.output = output
	kcp.fastacklimit = IKCP_FASTACK_LIMIT
	kcp.send_all_acks_through_metered_ip = false
	kcp.metered_ip_aggressiveness = 0

	kcp.SessionType = sessionType
	return kcp
}

func NewKCPWithDrop(conv uint32, sessionType *int32, output output_callback, dropRate float64, dropOn bool) *KCP {
	kcp := NewKCP(conv, sessionType, output)
	kcp.setDropRate(dropRate)
	if dropOn {
		kcp.dropOpen()
	} else {
		kcp.dropOff()
	}
	return kcp
}

// newSegment creates a KCP segment
func (kcp *KCP) newSegment(size int) (seg segment) {
	seg.data = xmitBuf.Get().([]byte)[:size]
	seg.has_promote = false
	return
}

// delSegment recycles a KCP segment
func (kcp *KCP) delSegment(seg *segment) {
	atomic.AddUint64(&DefaultSnmp.SegmentNumbersACKed, 1)

	if seg.has_promote || *kcp.SessionType == SessionTypeOnlyMetered {
		atomic.AddUint64(&DefaultSnmp.SegmentNumbersPromotedACKed, 1)
	}
	if seg.data != nil {
		xmitBuf.Put(seg.data)
		seg.data = nil
	}
}

// ReserveBytes keeps n bytes untouched from the beginning of the buffer,
// the output_callback function should be aware of this.
//
// Return false if n >= mss
func (kcp *KCP) ReserveBytes(n int) bool {
	if n >= int(kcp.mtu-IKCP_OVERHEAD) || n < 0 {
		return false
	}
	kcp.reserved = n
	kcp.mss = kcp.mtu - IKCP_OVERHEAD - uint32(n)
	return true
}

// PeekSize checks the size of next message in the recv queue
func (kcp *KCP) PeekSize() (length int) {
	if len(kcp.rcv_queue) == 0 {
		return -1
	}

	seg := &kcp.rcv_queue[0]
	if seg.frg == 0 {
		return len(seg.data)
	}

	if len(kcp.rcv_queue) < int(seg.frg+1) {
		return -1
	}

	for k := range kcp.rcv_queue {
		seg := &kcp.rcv_queue[k]
		length += len(seg.data)
		if seg.frg == 0 {
			break
		}
	}
	return
}

// Receive data from kcp state machine
//
// Return number of bytes read.
//
// Return -1 when there is no readable data.
//
// Return -2 if len(buffer) is smaller than kcp.PeekSize().
func (kcp *KCP) Recv(buffer []byte) (n int) {
	peeksize := kcp.PeekSize()
	if peeksize < 0 {
		return -1
	}

	if peeksize > len(buffer) {
		return -2
	}

	var fast_recover bool
	if len(kcp.rcv_queue) >= int(kcp.rcv_wnd) {
		fast_recover = true
	}

	// merge fragment
	count := 0
	for k := range kcp.rcv_queue {
		seg := &kcp.rcv_queue[k]
		copy(buffer, seg.data)
		buffer = buffer[len(seg.data):]
		n += len(seg.data)
		count++
		kcp.delSegment(seg)
		if seg.frg == 0 {
			break
		}
	}
	if count > 0 {
		kcp.rcv_queue = kcp.remove_front(kcp.rcv_queue, count)
	}

	// move available data from rcv_buf -> rcv_queue
	count = 0
	for k := range kcp.rcv_buf {
		seg := &kcp.rcv_buf[k]
		if seg.sn == kcp.rcv_nxt && len(kcp.rcv_queue)+count < int(kcp.rcv_wnd) {
			kcp.rcv_nxt++
			count++
		} else {
			break
		}
	}

	if count > 0 {
		kcp.rcv_queue = append(kcp.rcv_queue, kcp.rcv_buf[:count]...)
		kcp.rcv_buf = kcp.remove_front(kcp.rcv_buf, count)
	}

	// fast recover
	if len(kcp.rcv_queue) < int(kcp.rcv_wnd) && fast_recover {
		// ready to send back IKCP_CMD_WINS in ikcp_flush
		// tell remote my window size
		kcp.probe |= IKCP_ASK_TELL
	}
	return
}

// Send is user/upper level send, returns below zero for error
func (kcp *KCP) Send(buffer []byte) int {
	var count int
	if len(buffer) == 0 {
		return -1
	}

	// append to previous segment in streaming mode (if possible)
	if kcp.stream != 0 {
		n := len(kcp.snd_queue)
		if n > 0 {
			seg := &kcp.snd_queue[n-1]
			if len(seg.data) < int(kcp.mss) {
				capacity := int(kcp.mss) - len(seg.data)
				extend := capacity
				if len(buffer) < capacity {
					extend = len(buffer)
				}

				// grow slice, the underlying cap is guaranteed to
				// be larger than kcp.mss
				oldlen := len(seg.data)
				seg.data = seg.data[:oldlen+extend]
				copy(seg.data[oldlen:], buffer)
				buffer = buffer[extend:]
			}
		}

		if len(buffer) == 0 {
			return 0
		}
	}

	if len(buffer) <= int(kcp.mss) {
		count = 1
	} else {
		count = (len(buffer) + int(kcp.mss) - 1) / int(kcp.mss)
	}

	if count > 255 {
		return -2
	}

	if count == 0 {
		count = 1
	}

	for i := 0; i < count; i++ {
		var size int
		if len(buffer) > int(kcp.mss) {
			size = int(kcp.mss)
		} else {
			size = len(buffer)
		}
		seg := kcp.newSegment(size)
		copy(seg.data, buffer[:size])
		if kcp.stream == 0 { // message mode
			seg.frg = uint8(count - i - 1)
		} else { // stream mode
			seg.frg = 0
		}
		kcp.snd_queue = append(kcp.snd_queue, seg)
		buffer = buffer[size:]
	}
	return 0
}

func (kcp *KCP) update_ack(rtt int32) {
	// https://tools.ietf.org/html/rfc6298
	var rto uint32
	if kcp.rx_srtt == 0 {
		kcp.rx_srtt = rtt
		kcp.rx_rttvar = rtt >> 1
	} else {
		delta := rtt - kcp.rx_srtt
		kcp.rx_srtt += delta >> 3
		if delta < 0 {
			delta = -delta
		}
		if rtt < kcp.rx_srtt-kcp.rx_rttvar {
			// if the new RTT sample is below the bottom of the range of
			// what an RTT measurement is expected to be.
			// give an 8x reduced weight versus its normal weighting
			kcp.rx_rttvar += (delta - kcp.rx_rttvar) >> 5
		} else {
			kcp.rx_rttvar += (delta - kcp.rx_rttvar) >> 2
		}
	}
	rto = uint32(kcp.rx_srtt) + _imax_(kcp.interval, uint32(kcp.rx_rttvar)<<2)
	kcp.rx_rto = _ibound_(kcp.rx_minrto, rto, IKCP_RTO_MAX)
	kcp.initial_tx_rto = _ibound_(kcp.rx_rto, rto+(rto>>3), IKCP_RTO_MAX) // 1.125xRTO for the initial tx, it is vital to avoid unnecessary retransmit
}

func (kcp *KCP) shrink_buf() {
	if len(kcp.snd_buf) > 0 {
		seg := &kcp.snd_buf[0]
		kcp.snd_una = seg.sn
	} else {
		kcp.snd_una = kcp.snd_nxt
	}
}

func (kcp *KCP) parse_ack(sn uint32) {
	if _itimediff(sn, kcp.snd_una) < 0 || _itimediff(sn, kcp.snd_nxt) >= 0 {
		return
	}

	for k := range kcp.snd_buf {
		seg := &kcp.snd_buf[k]

		if sn == seg.sn {
			// mark and free space, but leave the segment here,
			// and wait until `una` to delete this, then we don't
			// have to shift the segments behind forward,
			// which is an expensive operation for large window
			seg.acked = 1
			kcp.delSegment(seg)
			break
		}
		if _itimediff(sn, seg.sn) < 0 {
			break
		}
	}
}

func (kcp *KCP) parse_fastack(sn, ts uint32) {
	if _itimediff(sn, kcp.snd_una) < 0 || _itimediff(sn, kcp.snd_nxt) >= 0 {
		return
	}

	for k := range kcp.snd_buf {
		seg := &kcp.snd_buf[k]
		if _itimediff(sn, seg.sn) < 0 {
			break
		} else if sn != seg.sn && _itimediff(seg.ts, ts) <= 0 {
			seg.fastack++
		}
	}
}

func (kcp *KCP) parse_una(una uint32) int {
	count := 0
	for k := range kcp.snd_buf {
		seg := &kcp.snd_buf[k]
		if _itimediff(una, seg.sn) > 0 {
			kcp.delSegment(seg)
			count++
		} else {
			break
		}
	}

	if count > 0 {
		kcp.snd_buf = kcp.remove_front(kcp.snd_buf, count)
	}
	return count
}

// ack append
func (kcp *KCP) ack_push(sn, ts uint32) {
	kcp.acklist = append(kcp.acklist, ackItem{sn, ts})
}

// returns true if data has repeated
func (kcp *KCP) parse_data(newseg segment) bool {
	sn := newseg.sn
	if _itimediff(sn, kcp.rcv_nxt+kcp.rcv_wnd) >= 0 ||
		_itimediff(sn, kcp.rcv_nxt) < 0 {
		return true
	}

	n := len(kcp.rcv_buf) - 1
	insert_idx := 0
	repeat := false
	for i := n; i >= 0; i-- {
		seg := &kcp.rcv_buf[i]
		if seg.sn == sn {
			repeat = true
			break
		}
		if _itimediff(sn, seg.sn) > 0 {
			insert_idx = i + 1
			break
		}
	}

	if !repeat {
		// replicate the content if it's new
		dataCopy := xmitBuf.Get().([]byte)[:len(newseg.data)]
		copy(dataCopy, newseg.data)
		newseg.data = dataCopy

		if insert_idx == n+1 {
			kcp.rcv_buf = append(kcp.rcv_buf, newseg)
		} else {
			kcp.rcv_buf = append(kcp.rcv_buf, segment{})
			copy(kcp.rcv_buf[insert_idx+1:], kcp.rcv_buf[insert_idx:])
			kcp.rcv_buf[insert_idx] = newseg
		}
	}

	// move available data from rcv_buf -> rcv_queue
	count := 0
	for k := range kcp.rcv_buf {
		seg := &kcp.rcv_buf[k]
		if seg.sn == kcp.rcv_nxt && len(kcp.rcv_queue)+count < int(kcp.rcv_wnd) {
			kcp.rcv_nxt++
			count++
		} else {
			break
		}
	}
	if count > 0 {
		kcp.rcv_queue = append(kcp.rcv_queue, kcp.rcv_buf[:count]...)
		kcp.rcv_buf = kcp.remove_front(kcp.rcv_buf, count)
	}

	return repeat
}

// Input a packet into kcp state machine.
//
// 'regular' indicates it's a real data packet from remote, and it means it's not generated from ReedSolomon
// codecs.
//
// 'ackNoDelay' will trigger immediate ACK, but surely it will not be efficient in bandwidth
func (kcp *KCP) Input(data []byte, regular, fromMetered, ackNoDelay bool) int {
	snd_una := kcp.snd_una

	if len(data) < IKCP_OVERHEAD {
		return -1
	}

	var latest uint32 // the latest ack packet
	var flag int
	var inSegs uint64
	var windowSlides bool

	if !fromMetered {
		shouldDrop := func(rate float64) bool {
			rand.Seed(time.Now().UnixNano())
			r := rand.Intn(1000)
			return r < int(rate*1000)
		}

		if kcp.dropOn && shouldDrop(kcp.dropRate) {
			atomic.AddUint64(&DefaultSnmp.BytesDropt, uint64(len(data)))
			data = data[:0]
		}
	}

	for {
		var ts, sn, length, una, conv uint32
		var wnd uint16
		var cmd, frg uint8

		if len(data) < int(IKCP_OVERHEAD) {
			break
		}

		data = ikcp_decode32u(data, &conv)
		if conv != kcp.conv {
			return -1
		}

		data = ikcp_decode8u(data, &cmd)
		data = ikcp_decode8u(data, &frg)
		data = ikcp_decode16u(data, &wnd)
		data = ikcp_decode32u(data, &ts)
		data = ikcp_decode32u(data, &sn)
		data = ikcp_decode32u(data, &una)
		data = ikcp_decode32u(data, &length)
		if len(data) < int(length) {
			return -2
		}

		if cmd != IKCP_CMD_PUSH && cmd != IKCP_CMD_ACK &&
			cmd != IKCP_CMD_WASK && cmd != IKCP_CMD_WINS {
			return -3
		}

		// only trust window updates from regular packets. i.e: latest update
		if regular {
			kcp.rmt_wnd = uint32(wnd)
		}
		if kcp.parse_una(una) > 0 {
			windowSlides = true
		}
		kcp.shrink_buf()

		if cmd == IKCP_CMD_ACK {
			kcp.parse_ack(sn)
			kcp.parse_fastack(sn, ts)
			flag |= 1
			latest = ts
		} else if cmd == IKCP_CMD_PUSH {
			repeat := true
			if _itimediff(sn, kcp.rcv_nxt+kcp.rcv_wnd) < 0 {
				kcp.ack_push(sn, ts)
				if _itimediff(sn, kcp.rcv_nxt) >= 0 {
					var seg segment
					seg.conv = conv
					seg.cmd = cmd
					seg.frg = frg
					seg.wnd = wnd
					seg.ts = ts
					seg.sn = sn
					seg.una = una
					seg.data = data[:length] // delayed data copying
					repeat = kcp.parse_data(seg)
				}
			}
			if regular && !fromMetered && repeat {
				atomic.AddUint64(&DefaultSnmp.RepeatSegs, 1)
			}
		} else if cmd == IKCP_CMD_WASK {
			// ready to send back IKCP_CMD_WINS in Ikcp_flush
			// tell remote my window size
			kcp.probe |= IKCP_ASK_TELL
		} else if cmd == IKCP_CMD_WINS {
			// do nothing
		} else {
			return -3
		}

		inSegs++
		data = data[length:]
		if fromMetered {
			atomic.AddUint64(&DefaultSnmp.BytesReceivedFromMeteredRaw, uint64(length))
		} else {
			atomic.AddUint64(&DefaultSnmp.BytesReceivedFromNoMeteredRaw, uint64(length))
		}
	}
	atomic.AddUint64(&DefaultSnmp.InSegs, inSegs)

	// update rtt with the latest ts
	// ignore the FEC packet
	if flag != 0 && regular && !fromMetered {
		current := currentMs()
		if _itimediff(current, latest) >= 0 {
			kcp.update_ack(_itimediff(current, latest))
		}
	}

	// cwnd update when packet arrived
	if kcp.nocwnd == 0 {
		if _itimediff(kcp.snd_una, snd_una) > 0 {
			if kcp.cwnd < kcp.rmt_wnd {
				mss := kcp.mss
				if kcp.cwnd < kcp.ssthresh {
					kcp.cwnd++
					kcp.incr += mss
				} else {
					if kcp.incr < mss {
						kcp.incr = mss
					}
					kcp.incr += (mss*mss)/kcp.incr + (mss / 16)
					if (kcp.cwnd+1)*mss <= kcp.incr {
						if mss > 0 {
							kcp.cwnd = (kcp.incr + mss - 1) / mss
						} else {
							kcp.cwnd = kcp.incr + mss - 1
						}
					}
				}
				if kcp.cwnd > kcp.rmt_wnd {
					kcp.cwnd = kcp.rmt_wnd
					kcp.incr = kcp.rmt_wnd * mss
				}
			}
		}
	}

	if windowSlides { // if window has slided, flush
		kcp.flush(false)
	} else if ackNoDelay && len(kcp.acklist) > 0 { // ack immediately
		kcp.flush(true)
	}
	return 0
}

func (kcp *KCP) wnd_unused() uint16 {
	if len(kcp.rcv_queue) < int(kcp.rcv_wnd) {
		return uint16(int(kcp.rcv_wnd) - len(kcp.rcv_queue))
	}
	return 0
}

const DEFAULT_BUFFER_POOL_LEVEL = 3

type KCPBufferPool struct {

	// 0b-32b level
	// 32b-64b flat
	Used      map[uint64]*[]byte
	UsedFlat  map[uint32]uint32
	UsedIndex map[uint64]uint32
	// 0 - (level-1) is the normal buffer bucket
	// normal buffer bucket used retry times to distinguish level
	// The level bucket holding the ack buffer
	// When init a buffer pool need level + 1 space
	Level    uint32
	Reserved int

	reuse sync.Pool
	mtu   uint32
}

func C32T64(high uint32, low uint32) uint64 {
	high64 := uint64(high) << 32
	result := high64 | uint64(low)
	return result
}

func C64T32(value uint64) (uint32, uint32) {
	high := uint32(value >> 32)
	low := uint32(value)
	return high, low
}

func NewKCPBufferPool(level uint32, reserved int, mtu uint32) *KCPBufferPool {
	if level > 5 || level < 3 {
		panic(errors.New("level should between [3,5]"))
	}

	bp := new(KCPBufferPool)
	bp.Level = level
	bp.Reserved = reserved
	bp.mtu = mtu
	bp.reuse.New = func() interface{} {
		b := make([]byte, mtu)
		return &b
	}

	bp.Used = make(map[uint64]*[]byte)
	bp.UsedFlat = make(map[uint32]uint32)
	bp.UsedIndex = make(map[uint64]uint32)

	return bp
}

func (bp *KCPBufferPool) getBufferSize(level uint32, isAck bool) uint32 {
	if level > bp.Level || (!isAck && level == bp.Level) {
		panic(errors.New(fmt.Sprintf("invalid level %d, bp.Level is %d", level, bp.Level)))
	}

	return bp.UsedFlat[level]
}

func (bp *KCPBufferPool) GetBufferSize(level uint32) uint32 {
	return bp.getBufferSize(level, false)
}

func (bp *KCPBufferPool) GetAckBufferSize() uint32 {
	return bp.getBufferSize(bp.Level, true)
}

func (bp *KCPBufferPool) EncodeAckSegInfo(seg *segment) {
	var ack_buffer = make([]byte, IKCP_OVERHEAD)
	var ack_flat_size = bp.GetAckBufferSize()
	var used_size uint32 = 0
	if ack_flat_size == 0 {
		reuse_buffer := bp.reuse.Get().(*[]byte)
		bp.Used[C32T64(bp.Level, 0)] = reuse_buffer
		bp.UsedIndex[C32T64(bp.Level, 0)] = uint32(bp.Reserved + copy((*reuse_buffer)[bp.Reserved:], ack_buffer))

		ack_flat_size += 1
		bp.UsedFlat[bp.Level] = ack_flat_size
		used_size = uint32(bp.Reserved)
	} else if bp.UsedIndex[C32T64(bp.Level, ack_flat_size-1)]+IKCP_OVERHEAD > bp.mtu {
		reuse_buffer := bp.reuse.Get().(*[]byte)
		bp.Used[C32T64(bp.Level, ack_flat_size)] = reuse_buffer
		bp.UsedIndex[C32T64(bp.Level, ack_flat_size)] =
			uint32(bp.Reserved + copy((*reuse_buffer)[bp.Reserved:], ack_buffer))

		ack_flat_size += 1
		bp.UsedFlat[bp.Level] = ack_flat_size
		used_size = uint32(bp.Reserved)
	} else { // < mtu
		used_size = bp.UsedIndex[C32T64(bp.Level, ack_flat_size-1)]
		used_buffer := bp.Used[C32T64(bp.Level, ack_flat_size-1)]
		bp.UsedIndex[C32T64(bp.Level, ack_flat_size-1)] += uint32(copy((*used_buffer)[used_size:], ack_buffer))
	}

	seg.encode((*bp.Used[C32T64(bp.Level, ack_flat_size-1)])[used_size:])
}

func (bp *KCPBufferPool) EncodeSegInfo(seg *segment, level uint32) {
	var need = len(seg.data) + IKCP_OVERHEAD
	var buffer_flat_size = bp.GetBufferSize(level)
	var used_size uint32 = 0

	if buffer_flat_size == 0 {
		reuse_buffer := bp.reuse.Get().(*[]byte)
		bp.Used[C32T64(level, 0)] = reuse_buffer

		bp.UsedIndex[C32T64(level, 0)] = uint32(bp.Reserved + IKCP_OVERHEAD + copy((*reuse_buffer)[bp.Reserved+IKCP_OVERHEAD:], seg.data))

		buffer_flat_size += 1
		bp.UsedFlat[level] = buffer_flat_size

		used_size = uint32(bp.Reserved)
	} else if bp.UsedIndex[C32T64(level, buffer_flat_size-1)]+uint32(need) > uint32(bp.mtu) {

		reuse_buffer := bp.reuse.Get().(*[]byte)
		bp.Used[C32T64(level, buffer_flat_size)] = reuse_buffer
		bp.UsedIndex[C32T64(level, buffer_flat_size)] =
			uint32(bp.Reserved + IKCP_OVERHEAD + copy((*reuse_buffer)[bp.Reserved+IKCP_OVERHEAD:], seg.data))

		buffer_flat_size += 1
		bp.UsedFlat[level] = buffer_flat_size
		used_size = uint32(bp.Reserved)
	} else {

		used_size = bp.UsedIndex[C32T64(level, buffer_flat_size-1)]
		used_buffer := bp.Used[C32T64(level, buffer_flat_size-1)]
		bp.UsedIndex[C32T64(level, buffer_flat_size-1)] += IKCP_OVERHEAD + uint32(copy((*used_buffer)[used_size+IKCP_OVERHEAD:], seg.data))
	}

	seg.encode((*bp.Used[C32T64(level, buffer_flat_size-1)])[used_size:])
}

func (bp *KCPBufferPool) CombineACKIfAllow() {
	ack_flat_size := bp.UsedFlat[bp.Level]
	level_0_flat_size := bp.UsedFlat[0]
	ack_buffer_len := bp.UsedIndex[C32T64(bp.Level, ack_flat_size-1)]
	level_0_buffer_off := bp.UsedIndex[C32T64(0, level_0_flat_size-1)]

	if ack_flat_size != 0 && level_0_flat_size != 0 &&
		bp.mtu-level_0_buffer_off > ack_buffer_len {

		ack_buffer := bp.Used[C32T64(bp.Level, ack_flat_size-1)]
		level_0_buffer := bp.Used[C32T64(0, level_0_flat_size-1)]

		bp.UsedIndex[C32T64(0, level_0_flat_size-1)] += uint32(copy((*level_0_buffer)[level_0_buffer_off:], (*ack_buffer)[bp.Reserved:ack_buffer_len]))

		bp.reuse.Put(bp.Used[C32T64(bp.Level, ack_flat_size-1)])

		bp.UsedFlat[bp.Level] = ack_flat_size - 1
		delete(bp.Used, C32T64(bp.Level, ack_flat_size-1))
		delete(bp.UsedIndex, C32T64(bp.Level, ack_flat_size-1))
	}
}

// flush pending data
func (kcp *KCP) flush(ackOnly bool) uint32 {
	var seg segment
	seg.conv = kcp.conv
	seg.cmd = IKCP_CMD_ACK
	seg.wnd = kcp.wnd_unused()
	seg.una = kcp.rcv_nxt

	aggressiveness := kcp.metered_ip_aggressiveness

	bp := NewKCPBufferPool(DEFAULT_BUFFER_POOL_LEVEL, kcp.reserved, kcp.mtu)
	bpi := NewKCPBufferPool(DEFAULT_BUFFER_POOL_LEVEL, kcp.reserved, kcp.mtu)

	getBPLevel := func(retryTimes uint32) uint32 {
		if retryTimes >= uint32(bp.Level) {
			return bp.Level - 1
		}
		return retryTimes
	}

	encodeAckSegInfo := func(seg *segment) {
		bpi.EncodeAckSegInfo(seg)
	}

	encodeSegInfo := func(seg *segment, important bool, retryTimes uint32) {
		level := getBPLevel(retryTimes)

		if important {
			bpi.EncodeSegInfo(seg, level)
		} else {
			bp.EncodeSegInfo(seg, level)
		}
	}

	fullACK := func() {
		if len(kcp.acklist) == 0 {
			return
		}

		seg.cmd = IKCP_CMD_ACK
		for i, ack := range kcp.acklist {
			// filter jitters caused by bufferbloat
			if _itimediff(ack.sn, kcp.rcv_nxt) >= 0 || len(kcp.acklist)-1 == i {
				seg.sn, seg.ts = ack.sn, ack.ts
				encodeAckSegInfo(&seg)
			}
		}
		kcp.acklist = kcp.acklist[0:0]
	}

	flushBuffer := func() {
		flush_internal_buffer := func(bp *KCPBufferPool, important bool) {

			if len(bp.UsedFlat) == 0 {
				return
			}

			if important {
				bp.CombineACKIfAllow()
			}

			for level, flat_size := range bp.UsedFlat {
				if flat_size == 0 {
					LogError("used flat: %v", bp.UsedFlat)
					panic(errors.New("invalid flat. logic error"))
				}

				var flat uint32 = 0
				for ; flat < flat_size; flat++ {
					combine_key := C32T64(level, flat)

					if bp.UsedIndex[combine_key] == 0 {
						LogError("used flat: %v \n, used index: %v", bp.UsedFlat, bp.UsedIndex)
						panic(errors.New("logic error"))
					}

					if level == bp.Level {
						if !important {
							panic(errors.New("invalid ack. logic error"))
						}
						kcp.output(*bp.Used[combine_key], int(bp.UsedIndex[combine_key]), true, 0)
					} else {
						kcp.output(*bp.Used[combine_key], int(bp.UsedIndex[combine_key]), important, level)
					}
					bp.reuse.Put(bp.Used[combine_key])
				}
			}

			bp.Used = make(map[uint64]*[]byte)
			bp.UsedFlat = make(map[uint32]uint32)
			bp.UsedIndex = make(map[uint64]uint32)
		}
		flush_internal_buffer(bpi, true)
		flush_internal_buffer(bp, false)

	}

	if ackOnly { // flush remain ack segments
		fullACK()
		flushBuffer()
		return kcp.interval
	}

	// probe window size (if remote window size equals zero)
	if kcp.rmt_wnd == 0 {
		current := currentMs()
		if kcp.probe_wait == 0 {
			kcp.probe_wait = IKCP_PROBE_INIT
			kcp.ts_probe = current + kcp.probe_wait
		} else {
			if _itimediff(current, kcp.ts_probe) >= 0 {
				if kcp.probe_wait < IKCP_PROBE_INIT {
					kcp.probe_wait = IKCP_PROBE_INIT
				}
				kcp.probe_wait += kcp.probe_wait / 2
				if kcp.probe_wait > IKCP_PROBE_LIMIT {
					kcp.probe_wait = IKCP_PROBE_LIMIT
				}
				kcp.ts_probe = current + kcp.probe_wait
				kcp.probe |= IKCP_ASK_SEND
			}
		}
	} else {
		kcp.ts_probe = 0
		kcp.probe_wait = 0
	}

	// flush window probing commands
	if (kcp.probe & IKCP_ASK_SEND) != 0 {
		seg.cmd = IKCP_CMD_WASK
		// Make default IKCP_CMD_WASK retry(fake) 1
		// It will reduces packet loss
		encodeAckSegInfo(&seg)
	}

	// flush window probing commands
	if (kcp.probe & IKCP_ASK_TELL) != 0 {
		seg.cmd = IKCP_CMD_WINS
		// Make default IKCP_CMD_WINS retry(fake) 1
		// It will reduces packet loss
		encodeAckSegInfo(&seg)
	}

	kcp.probe = 0

	// calculate window size
	cwnd := _imin_(kcp.snd_wnd, kcp.rmt_wnd)
	if kcp.nocwnd == 0 {
		cwnd = _imin_(kcp.cwnd, cwnd)
	}

	// sliding window, controlled by snd_nxt && sna_una+cwnd
	newSegsCount := 0
	cwnd_full := false
	for k := range kcp.snd_queue {
		if _itimediff(kcp.snd_nxt, kcp.snd_una+cwnd) >= 0 {
			cwnd_full = true
			break
		}
		newseg := kcp.snd_queue[k]
		newseg.conv = kcp.conv
		newseg.cmd = IKCP_CMD_PUSH
		newseg.sn = kcp.snd_nxt
		kcp.snd_buf = append(kcp.snd_buf, newseg)
		kcp.snd_nxt++
		newSegsCount++
	}

	if newSegsCount > 0 {
		kcp.snd_queue = kcp.remove_front(kcp.snd_queue, newSegsCount)
	} else if cwnd_full {
		// 滑动窗口若无进展（窗口满或无新数据），提高发送优先级，希望能推动窗口进展
		if aggressiveness < 1 {
			fullACK()
			flushBuffer()
		}
	}

	// calculate resent
	resent := uint32(kcp.fastresend)
	if kcp.fastresend <= 0 {
		resent = 0xffffffff
	}

	// check for retransmissions
	current := currentMs()
	var change, lostSegs, fastRetransSegs, earlyRetransSegs uint64
	minrto := int32(kcp.interval)

	sndBufLen := len(kcp.snd_buf)
	initialTXRTO := kcp.rx_rto
	if sndBufLen > 64 {
		// Stream isn't thin
		initialTXRTO = kcp.initial_tx_rto
	}

	ref := kcp.snd_buf[:sndBufLen] // for bounds check elimination

	for k := range ref {
		segment := &ref[k]
		needsend := false
		if segment.acked == 1 {
			continue
		}
		if segment.xmit == 0 { // initial transmit
			needsend = true
			segment.rto = kcp.rx_rto
			segment.resendts = current + initialTXRTO
		} else if segment.fastack >= resent && (seg.xmit <= uint32(kcp.fastacklimit) || kcp.fastacklimit <= 0) { // fast retransmit
			needsend = true
			segment.fastack = 0
			segment.rto = kcp.rx_rto
			segment.resendts = current + initialTXRTO
			change++
			fastRetransSegs++
		} else if segment.fastack > 0 && newSegsCount == 0 { // early retransmit
			needsend = true
			segment.fastack = 0
			segment.rto = kcp.rx_rto
			segment.resendts = current + initialTXRTO
			change++
			earlyRetransSegs++
		} else if _itimediff(current, segment.resendts) >= 0 { // RTO
			needsend = true
			if kcp.nodelay == 0 {
				segment.rto += kcp.rx_rto
			} else {
				segment.rto += kcp.rx_rto / 2
			}
			segment.fastack = 0
			segment.resendts = current + segment.rto
			lostSegs++
		}

		if needsend {
			current = currentMs()
			segment.xmit++
			segment.ts = current
			segment.wnd = seg.wnd
			segment.una = seg.una

			promote := segment.xmit > 3 || (segment.xmit >= 2 && len(kcp.acklist) > 0)

			// If *kcp.SessionType is SessionTypeNormal
			// Then current segment or ack won't be promote in `output`
			if promote && *kcp.SessionType != SessionTypeNormal {
				segment.has_promote = true
			}

			encodeSegInfo(segment, promote, segment.xmit-1)
			if segment.xmit >= kcp.dead_link {
				kcp.state = 0xFFFFFFFF
			}
		}

		// get the nearest rto
		if rto := _itimediff(segment.resendts, current); rto > 0 && rto < minrto {
			minrto = rto
		}
	}

	fullACK()
	flushBuffer()

	// counter updates
	sum := lostSegs
	if lostSegs > 0 {
		atomic.AddUint64(&DefaultSnmp.LostSegs, lostSegs)
	}
	if fastRetransSegs > 0 {
		atomic.AddUint64(&DefaultSnmp.FastRetransSegs, fastRetransSegs)
		sum += fastRetransSegs
	}
	if earlyRetransSegs > 0 {
		atomic.AddUint64(&DefaultSnmp.EarlyRetransSegs, earlyRetransSegs)
		sum += earlyRetransSegs
	}
	if sum > 0 {
		atomic.AddUint64(&DefaultSnmp.RetransSegs, sum)
	}

	// cwnd update
	if kcp.nocwnd == 0 {
		// update ssthresh
		// rate halving, https://tools.ietf.org/html/rfc6937
		if change > 0 {
			inflight := kcp.snd_nxt - kcp.snd_una
			kcp.ssthresh = inflight / 2
			if kcp.ssthresh < IKCP_THRESH_MIN {
				kcp.ssthresh = IKCP_THRESH_MIN
			}
			kcp.cwnd = kcp.ssthresh + resent
			kcp.incr = kcp.cwnd * kcp.mss
		}

		// congestion control, https://tools.ietf.org/html/rfc5681
		if lostSegs > 0 {
			kcp.ssthresh = cwnd / 2
			if kcp.ssthresh < IKCP_THRESH_MIN {
				kcp.ssthresh = IKCP_THRESH_MIN
			}
			kcp.cwnd = 1
			kcp.incr = kcp.mss
		}

		if kcp.cwnd < 1 {
			kcp.cwnd = 1
			kcp.incr = kcp.mss
		}
	}

	return uint32(minrto)
}

// (deprecated)
//
// Update updates state (call it repeatedly, every 10ms-100ms), or you can ask
// ikcp_check when to call it again (without ikcp_input/_send calling).
// 'current' - current timestamp in millisec.
func (kcp *KCP) Update() {
	var slap int32

	current := currentMs()
	if kcp.updated == 0 {
		kcp.updated = 1
		kcp.ts_flush = current
	}

	slap = _itimediff(current, kcp.ts_flush)

	if slap >= 10000 || slap < -10000 {
		kcp.ts_flush = current
		slap = 0
	}

	if slap >= 0 {
		kcp.ts_flush += kcp.interval
		if _itimediff(current, kcp.ts_flush) >= 0 {
			kcp.ts_flush = current + kcp.interval
		}
		kcp.flush(false)
	}
}

// (deprecated)
//
// Check determines when should you invoke ikcp_update:
// returns when you should invoke ikcp_update in millisec, if there
// is no ikcp_input/_send calling. you can call ikcp_update in that
// time, instead of call update repeatly.
// Important to reduce unnacessary ikcp_update invoking. use it to
// schedule ikcp_update (eg. implementing an epoll-like mechanism,
// or optimize ikcp_update when handling massive kcp connections)
func (kcp *KCP) Check() uint32 {
	current := currentMs()
	ts_flush := kcp.ts_flush
	tm_flush := int32(0x7fffffff)
	tm_packet := int32(0x7fffffff)
	minimal := uint32(0)
	if kcp.updated == 0 {
		return current
	}

	if _itimediff(current, ts_flush) >= 10000 ||
		_itimediff(current, ts_flush) < -10000 {
		ts_flush = current
	}

	if _itimediff(current, ts_flush) >= 0 {
		return current
	}

	tm_flush = _itimediff(ts_flush, current)

	for k := range kcp.snd_buf {
		seg := &kcp.snd_buf[k]
		diff := _itimediff(seg.resendts, current)
		if diff <= 0 {
			return current
		}
		if diff < tm_packet {
			tm_packet = diff
		}
	}

	minimal = uint32(tm_packet)
	if tm_packet >= tm_flush {
		minimal = uint32(tm_flush)
	}
	if minimal >= kcp.interval {
		minimal = kcp.interval
	}

	return current + minimal
}

// SetMtu changes MTU size, default is 1400
func (kcp *KCP) SetMtu(mtu int) int {
	if mtu < 50 || mtu < IKCP_OVERHEAD {
		return -1
	}
	if kcp.reserved >= int(kcp.mtu-IKCP_OVERHEAD) || kcp.reserved < 0 {
		return -1
	}

	buffer := make([]byte, mtu)
	if buffer == nil {
		return -2
	}
	kcp.mtu = uint32(mtu)
	kcp.mss = kcp.mtu - IKCP_OVERHEAD - uint32(kcp.reserved)
	kcp.buffer = buffer
	return 0
}

func (kcp *KCP) ConfigMeteredIpUsage(all_acks bool, aggressiveness int) {
	kcp.send_all_acks_through_metered_ip = all_acks
	kcp.metered_ip_aggressiveness = aggressiveness
}

// NoDelay options
// fastest: ikcp_nodelay(kcp, 1, 20, 2, 1)
// nodelay: 0:disable(default), 1:enable
// interval: internal update timer interval in millisec, default is 100ms
// resend: 0:disable fast resend(default), 1:enable fast resend
// nc: 0:normal congestion control(default), 1:disable congestion control
func (kcp *KCP) NoDelay(nodelay, interval, resend, nc int) int {
	if nodelay >= 0 {
		kcp.nodelay = uint32(nodelay)
		if nodelay != 0 {
			kcp.rx_minrto = IKCP_RTO_NDL
		} else {
			kcp.rx_minrto = IKCP_RTO_MIN
		}
	}
	if interval >= 0 {
		if interval > 5000 {
			interval = 5000
		} else if interval < 10 {
			interval = 10
		}
		kcp.interval = uint32(interval)
	}
	if resend >= 0 {
		kcp.fastresend = int32(resend)
	}
	if nc >= 0 {
		kcp.nocwnd = int32(nc)
	}
	return 0
}

// WndSize sets maximum window size: sndwnd=32, rcvwnd=32 by default
func (kcp *KCP) WndSize(sndwnd, rcvwnd int) int {
	if sndwnd > 0 {
		kcp.snd_wnd = uint32(sndwnd)
	}
	if rcvwnd > 0 {
		kcp.rcv_wnd = uint32(rcvwnd)
	}
	return 0
}

// WaitSnd gets how many packet is waiting to be sent
func (kcp *KCP) WaitSnd() int {
	return len(kcp.snd_buf) + len(kcp.snd_queue)
}

// remove front n elements from queue
// if the number of elements to remove is more than half of the size.
// just shift the rear elements to front, otherwise just reslice q to q[n:]
// then the cost of runtime.growslice can always be less than n/2
func (kcp *KCP) remove_front(q []segment, n int) []segment {
	if n > cap(q)/2 {
		newn := copy(q, q[n:])
		return q[:newn]
	}
	return q[n:]
}

// Release all cached outgoing segments
func (kcp *KCP) ReleaseTX() {
	for k := range kcp.snd_queue {
		if kcp.snd_queue[k].data != nil {
			xmitBuf.Put(kcp.snd_queue[k].data)
		}
	}
	for k := range kcp.snd_buf {
		if kcp.snd_buf[k].data != nil {
			xmitBuf.Put(kcp.snd_buf[k].data)
		}
	}
	kcp.snd_queue = nil
	kcp.snd_buf = nil
}

// support drop
func (dkcp *KCP) dropOpen() {
	dkcp.dropOn = true
}

func (dkcp *KCP) dropOff() {
	dkcp.dropOn = false
}

func (dkcp *KCP) setDropRate(dropRate float64) {
	dkcp.dropRate = dropRate
}

package kcp

import (
	"encoding/binary"
	"io"
	"net"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/xtaci/lossyconn"
)

const repeat = 16

func TestLossyConn1(t *testing.T) {
	t.Log("testing loss rate 10%, rtt 200ms")
	t.Log("testing link with nodelay parameters:1 10 2 1")
	client, err := lossyconn.NewLossyConn(0.1, 100)
	if err != nil {
		t.Fatal(err)
	}

	server, err := lossyconn.NewLossyConn(0.1, 100)
	if err != nil {
		t.Fatal(err)
	}
	testlink(t, client, server, 1, 10, 2, 1)
}

func TestLossyConn2(t *testing.T) {
	t.Log("testing loss rate 20%, rtt 200ms")
	t.Log("testing link with nodelay parameters:1 10 2 1")
	client, err := lossyconn.NewLossyConn(0.2, 100)
	if err != nil {
		t.Fatal(err)
	}

	server, err := lossyconn.NewLossyConn(0.2, 100)
	if err != nil {
		t.Fatal(err)
	}
	testlink(t, client, server, 1, 10, 2, 1)
}

func TestLossyConn3(t *testing.T) {
	t.Log("testing loss rate 30%, rtt 200ms")
	t.Log("testing link with nodelay parameters:1 10 2 1")
	client, err := lossyconn.NewLossyConn(0.3, 100)
	if err != nil {
		t.Fatal(err)
	}

	server, err := lossyconn.NewLossyConn(0.3, 100)
	if err != nil {
		t.Fatal(err)
	}
	testlink(t, client, server, 1, 10, 2, 1)
}

func TestLossyConn4(t *testing.T) {
	t.Log("testing loss rate 10%, rtt 200ms")
	t.Log("testing link with nodelay parameters:1 10 2 0")
	client, err := lossyconn.NewLossyConn(0.1, 100)
	if err != nil {
		t.Fatal(err)
	}

	server, err := lossyconn.NewLossyConn(0.1, 100)
	if err != nil {
		t.Fatal(err)
	}
	testlink(t, client, server, 1, 10, 2, 0)
}

func testlink(t *testing.T, client *lossyconn.LossyConn, server *lossyconn.LossyConn, nodelay, interval, resend, nc int) {
	t.Log("testing with nodelay parameters:", nodelay, interval, resend, nc)
	sess, _ := NewConn2(server.LocalAddr(), nil, 0, 0, client)
	listener, _ := ServeConn(nil, 0, 0, server)
	echoServer := func(l *Listener) {
		for {
			conn, err := l.AcceptKCP()
			if err != nil {
				return
			}
			go func() {
				conn.SetNoDelay(nodelay, interval, resend, nc)
				buf := make([]byte, 65536)
				for {
					n, err := conn.Read(buf)
					if err != nil {
						return
					}
					conn.Write(buf[:n])
				}
			}()
		}
	}

	echoTester := func(s *UDPSession, raddr net.Addr) {
		s.SetNoDelay(nodelay, interval, resend, nc)
		buf := make([]byte, 64)
		var rtt time.Duration
		for i := 0; i < repeat; i++ {
			start := time.Now()
			s.Write(buf)
			io.ReadFull(s, buf)
			rtt += time.Since(start)
		}

		t.Log("client:", client)
		t.Log("server:", server)
		t.Log("avg rtt:", rtt/repeat)
		t.Logf("total time: %v for %v round trip:", rtt, repeat)
	}

	go echoServer(listener)
	echoTester(sess, server.LocalAddr())
}

func BenchmarkFlush(b *testing.B) {
	kcp := NewKCP(1, func(buf []byte, size int, important bool, retryTimes uint32) {})
	kcp.snd_buf = make([]segment, 1024)
	for k := range kcp.snd_buf {
		kcp.snd_buf[k].xmit = 1
		kcp.snd_buf[k].resendts = currentMs() + 10000
	}
	b.ResetTimer()
	b.ReportAllocs()
	var mu sync.Mutex
	for i := 0; i < b.N; i++ {
		mu.Lock()
		kcp.flush(false)
		mu.Unlock()
	}
}

func createSegement(sn uint32, data []byte, len int) *segment {
	seg := new(segment)
	seg.conv = 123
	if len == 0 {
		seg.cmd = IKCP_CMD_ACK
	} else {
		seg.cmd = IKCP_WND_SND
	}

	seg.sn = sn
	if len != 0 {
		seg.data = data
	}

	return seg
}

func verifySeg(t *testing.T, data []byte, data_len uint32, expectSN uint32, expectConv uint32, expectByte byte) {

	conv := binary.LittleEndian.Uint32(data)
	sn := binary.LittleEndian.Uint32(data[IKCP_SN_OFFSET:])

	assert.Equal(t, expectConv, conv)
	assert.Equal(t, expectSN, sn)

	if data_len != IKCP_OVERHEAD {
		verifyData := data[IKCP_OVERHEAD+10]
		assert.Equal(t, expectByte, verifyData)
	}

}

func TestBufferPool(t *testing.T) {
	var mtu uint32 = 1500
	var expectConv uint32 = 123
	var expectByte byte = 0x6

	bp := NewKCPBufferPool(3, 0, mtu)
	assert.Equal(t, uint32(0), bp.GetBufferSize(0))
	assert.Equal(t, uint32(0), bp.GetBufferSize(1))
	assert.Equal(t, uint32(0), bp.GetBufferSize(2))
	assert.Equal(t, uint32(0), bp.getBufferSize(3, true)) // ack
	assert.Equal(t, uint32(0), bp.GetAckBufferSize())     // ack

	ack_seg := createSegement(1, []byte{}, 0)
	bp.EncodeAckSegInfo(ack_seg)

	assert.Equal(t, uint32(1), bp.GetAckBufferSize()) // ack
	assert.Equal(t, uint32(IKCP_OVERHEAD), bp.UsedIndex[C32T64(3, 0)])
	verifySeg(t, *bp.Used[C32T64(3, 0)], IKCP_OVERHEAD, 1, expectConv, expectByte)

	ack_seg = createSegement(2, []byte{}, 0)
	bp.EncodeAckSegInfo(ack_seg)
	assert.Equal(t, uint32(1), bp.GetAckBufferSize())
	assert.Equal(t, uint32(IKCP_OVERHEAD*2), bp.UsedIndex[C32T64(3, 0)])
	verifySeg(t, (*bp.Used[C32T64(3, 0)])[IKCP_OVERHEAD:], IKCP_OVERHEAD, 2, expectConv, expectByte)

	buffer := make([]byte, 1000)
	buffer[10] = expectByte
	seg := createSegement(13, buffer, 1000)
	bp.EncodeSegInfo(seg, 0)
	assert.Equal(t, uint32(1), bp.GetBufferSize(0))
	assert.Equal(t, uint32(IKCP_OVERHEAD+1000), bp.UsedIndex[C32T64(0, 0)])
	verifySeg(t, (*bp.Used[C32T64(0, 0)]), bp.UsedIndex[C32T64(0, 0)], 13, expectConv, expectByte)

	seg = createSegement(14, buffer, 1000)
	bp.EncodeSegInfo(seg, 0)
	assert.Equal(t, uint32(2), bp.GetBufferSize(0))
	assert.Equal(t, uint32(IKCP_OVERHEAD+1000), bp.UsedIndex[C32T64(0, 1)])
	verifySeg(t, (*bp.Used[C32T64(0, 1)]), bp.UsedIndex[C32T64(0, 1)], 14, expectConv, expectByte)

	buffer = make([]byte, 500)
	buffer[10] = expectByte
	seg = createSegement(25, buffer, 500)
	bp.EncodeSegInfo(seg, 1)
	assert.Equal(t, uint32(1), bp.GetBufferSize(1))
	assert.Equal(t, uint32(IKCP_OVERHEAD+500), bp.UsedIndex[C32T64(1, 0)])
	verifySeg(t, (*bp.Used[C32T64(1, 0)]), bp.UsedIndex[C32T64(1, 0)], 25, expectConv, expectByte)

	seg = createSegement(26, buffer, 500)
	bp.EncodeSegInfo(seg, 1)
	assert.Equal(t, uint32(1), bp.GetBufferSize(1))
	assert.Equal(t, uint32(IKCP_OVERHEAD*2+500*2), bp.UsedIndex[C32T64(1, 0)])
	verifySeg(t, (*bp.Used[C32T64(1, 0)])[IKCP_OVERHEAD+500:], IKCP_OVERHEAD+500, 26, expectConv, expectByte)
}

func TestBufferPoolMtuBound(t *testing.T) {
	var ack_mtu uint32 = 5 * IKCP_OVERHEAD
	var data_mtu uint32 = 3 * (IKCP_OVERHEAD + 500)
	var expectConv uint32 = 123
	var expectByte byte = 0x6

	bp := NewKCPBufferPool(3, 0, ack_mtu)

	var i uint32 = 1
	for ; i <= 5; i++ {
		ack_seg := createSegement(i, []byte{}, 0)
		bp.EncodeAckSegInfo(ack_seg)
		assert.Equal(t, uint32(1), bp.GetAckBufferSize())
		assert.Equal(t, uint32(IKCP_OVERHEAD*i), bp.UsedIndex[C32T64(3, 0)])
		verifySeg(t, (*bp.Used[C32T64(3, 0)])[IKCP_OVERHEAD*(i-1):], IKCP_OVERHEAD, i, expectConv, expectByte)
	}

	ack_seg := createSegement(6, []byte{}, 0)
	bp.EncodeAckSegInfo(ack_seg)
	assert.Equal(t, uint32(2), bp.GetAckBufferSize())
	assert.Equal(t, uint32(IKCP_OVERHEAD), bp.UsedIndex[C32T64(3, 1)])
	verifySeg(t, (*bp.Used[C32T64(3, 1)]), IKCP_OVERHEAD, 6, expectConv, expectByte)

	bp = NewKCPBufferPool(3, 0, data_mtu)

	i = 1
	for ; i <= 3; i++ {
		buffer := make([]byte, 500)
		buffer[10] = expectByte
		seg := createSegement(100+i, buffer, 500)
		bp.EncodeSegInfo(seg, 1)

		assert.Equal(t, uint32(1), bp.GetBufferSize(1))
		assert.Equal(t, uint32((IKCP_OVERHEAD+500)*i), bp.UsedIndex[C32T64(1, 0)])
		verifySeg(t, (*bp.Used[C32T64(1, 0)])[(IKCP_OVERHEAD+500)*(i-1):], IKCP_OVERHEAD+500, 100+i, expectConv, expectByte)
	}

	buffer := make([]byte, 500)
	buffer[10] = expectByte
	seg := createSegement(1000, buffer, 500)
	bp.EncodeSegInfo(seg, 1)

	assert.Equal(t, uint32(2), bp.GetBufferSize(1))
	assert.Equal(t, uint32(IKCP_OVERHEAD+500), bp.UsedIndex[C32T64(1, 1)])
	verifySeg(t, (*bp.Used[C32T64(1, 1)]), IKCP_OVERHEAD+500, 1000, expectConv, expectByte)
}

func TestBufferPoolWithReserved(t *testing.T) {
	var reserved uint32 = 12
	var ack_mtu uint32 = 5*IKCP_OVERHEAD + reserved
	var data_mtu uint32 = 3*(IKCP_OVERHEAD+500) + reserved
	var expectConv uint32 = 123
	var expectByte byte = 0x6

	bp := NewKCPBufferPool(3, int(reserved), ack_mtu)

	var i uint32 = 1
	for ; i <= 5; i++ {
		ack_seg := createSegement(i, []byte{}, 0)
		bp.EncodeAckSegInfo(ack_seg)
		assert.Equal(t, uint32(1), bp.GetAckBufferSize())
		assert.Equal(t, uint32(IKCP_OVERHEAD*i+reserved), bp.UsedIndex[C32T64(3, 0)])
		verifySeg(t, (*bp.Used[C32T64(3, 0)])[IKCP_OVERHEAD*(i-1)+reserved:], IKCP_OVERHEAD, i, expectConv, expectByte)
	}

	ack_seg := createSegement(6, []byte{}, 0)
	bp.EncodeAckSegInfo(ack_seg)
	assert.Equal(t, uint32(2), bp.GetAckBufferSize())
	assert.Equal(t, uint32(IKCP_OVERHEAD+reserved), bp.UsedIndex[C32T64(3, 1)])
	verifySeg(t, (*bp.Used[C32T64(3, 1)])[reserved:], IKCP_OVERHEAD, 6, expectConv, expectByte)

	bp = NewKCPBufferPool(3, int(reserved), data_mtu)

	i = 1
	for ; i <= 3; i++ {
		buffer := make([]byte, 500)
		buffer[10] = expectByte
		seg := createSegement(100+i, buffer, 500)
		bp.EncodeSegInfo(seg, 1)

		assert.Equal(t, uint32(1), bp.GetBufferSize(1))
		assert.Equal(t, uint32((IKCP_OVERHEAD+500)*i+reserved), bp.UsedIndex[C32T64(1, 0)])
		verifySeg(t, (*bp.Used[C32T64(1, 0)])[(IKCP_OVERHEAD+500)*(i-1)+reserved:], IKCP_OVERHEAD+500, 100+i, expectConv, expectByte)
	}

	buffer := make([]byte, 500)
	buffer[10] = expectByte
	seg := createSegement(1000, buffer, 500)
	bp.EncodeSegInfo(seg, 1)

	assert.Equal(t, uint32(2), bp.GetBufferSize(1))
	assert.Equal(t, uint32((IKCP_OVERHEAD+500)+reserved), bp.UsedIndex[C32T64(1, 1)])
	verifySeg(t, (*bp.Used[C32T64(1, 1)])[reserved:], IKCP_OVERHEAD+1000, 1000, expectConv, expectByte)

}

func TestBufferPoolCombineACK(t *testing.T) {
	var mtu uint32 = 1500
	var expectConv uint32 = 123
	var expectByte byte = 0x6

	bp := NewKCPBufferPool(3, 0, mtu)

	ack_seg := createSegement(777, []byte{}, 0)
	bp.EncodeAckSegInfo(ack_seg)

	assert.Equal(t, uint32(1), bp.GetAckBufferSize())
	assert.Equal(t, uint32(IKCP_OVERHEAD), bp.UsedIndex[C32T64(3, 0)])
	verifySeg(t, (*bp.Used[C32T64(3, 0)]), IKCP_OVERHEAD, 777, expectConv, expectByte)

	// should do nothing, because no data in level 0
	bp.CombineACKIfAllow()

	assert.Equal(t, uint32(1), bp.GetAckBufferSize())
	assert.Equal(t, uint32(IKCP_OVERHEAD), bp.UsedIndex[C32T64(3, 0)])
	verifySeg(t, (*bp.Used[C32T64(3, 0)]), IKCP_OVERHEAD, 777, expectConv, expectByte)

	buffer := make([]byte, 500)
	buffer[10] = expectByte
	seg := createSegement(25, buffer, 500)
	bp.EncodeSegInfo(seg, 1)
	assert.Equal(t, uint32(1), bp.GetBufferSize(1))
	assert.Equal(t, uint32(IKCP_OVERHEAD+500), bp.UsedIndex[C32T64(1, 0)])
	verifySeg(t, (*bp.Used[C32T64(1, 0)]), bp.UsedIndex[C32T64(1, 0)], 25, expectConv, expectByte)

	// should do nothing, because no data in level 0
	bp.CombineACKIfAllow()

	assert.Equal(t, uint32(1), bp.GetAckBufferSize())
	assert.Equal(t, uint32(IKCP_OVERHEAD), bp.UsedIndex[C32T64(3, 0)])
	verifySeg(t, (*bp.Used[C32T64(3, 0)]), IKCP_OVERHEAD, 777, expectConv, expectByte)

	buffer = make([]byte, mtu-IKCP_OVERHEAD)
	buffer[10] = expectByte
	seg = createSegement(26, buffer, int(mtu-IKCP_OVERHEAD))
	bp.EncodeSegInfo(seg, 0)
	assert.Equal(t, uint32(1), bp.GetBufferSize(0))
	assert.Equal(t, uint32(mtu), bp.UsedIndex[C32T64(0, 0)])
	verifySeg(t, (*bp.Used[C32T64(0, 0)]), bp.UsedIndex[C32T64(0, 0)], 26, expectConv, expectByte)

	// still do nothing, because data is full in level 0
	bp.CombineACKIfAllow()

	assert.Equal(t, uint32(1), bp.GetAckBufferSize())
	assert.Equal(t, uint32(IKCP_OVERHEAD), bp.UsedIndex[C32T64(3, 0)])
	verifySeg(t, (*bp.Used[C32T64(3, 0)]), IKCP_OVERHEAD, 777, expectConv, expectByte)

	buffer = make([]byte, 500)
	buffer[10] = expectByte
	seg = createSegement(27, buffer, 500)
	bp.EncodeSegInfo(seg, 0)
	assert.Equal(t, uint32(2), bp.GetBufferSize(0))
	assert.Equal(t, uint32(500+IKCP_OVERHEAD), bp.UsedIndex[C32T64(0, 1)])
	verifySeg(t, (*bp.Used[C32T64(0, 1)]), bp.UsedIndex[C32T64(0, 1)], 27, expectConv, expectByte)

	// should be copy to level 0 flat 1
	bp.CombineACKIfAllow()

	assert.Equal(t, uint32(0), bp.GetAckBufferSize())
	assert.Equal(t, uint32(0), bp.UsedIndex[C32T64(3, 0)])
	assert.Equal(t, uint32(2), bp.GetBufferSize(0))
	assert.Equal(t, uint32(500+IKCP_OVERHEAD+IKCP_OVERHEAD), bp.UsedIndex[C32T64(0, 1)])

	verifySeg(t, (*bp.Used[C32T64(0, 1)])[500+IKCP_OVERHEAD:], IKCP_OVERHEAD, 777, expectConv, expectByte)
}

func TestBufferPoolCombineACKWithReserved(t *testing.T) {
	var mtu uint32 = 1500
	var expectConv uint32 = 123
	var expectByte byte = 0x6
	var reserved int = 12

	bp := NewKCPBufferPool(3, reserved, mtu)

	ack_seg := createSegement(777, []byte{}, 0)
	bp.EncodeAckSegInfo(ack_seg)

	assert.Equal(t, uint32(1), bp.GetAckBufferSize())
	assert.Equal(t, uint32(IKCP_OVERHEAD+reserved), bp.UsedIndex[C32T64(3, 0)])
	verifySeg(t, (*bp.Used[C32T64(3, 0)])[reserved:], IKCP_OVERHEAD, 777, expectConv, expectByte)

	buffer := make([]byte, 500)
	buffer[10] = expectByte
	seg := createSegement(27, buffer, 500)
	bp.EncodeSegInfo(seg, 0)
	assert.Equal(t, uint32(1), bp.GetBufferSize(0))
	assert.Equal(t, uint32(500+IKCP_OVERHEAD+reserved), bp.UsedIndex[C32T64(0, 0)])
	verifySeg(t, (*bp.Used[C32T64(0, 0)])[reserved:], bp.UsedIndex[C32T64(0, 0)], 27, expectConv, expectByte)

	// should be copy to level 0 flat 0
	bp.CombineACKIfAllow()

	assert.Equal(t, uint32(0), bp.GetAckBufferSize())
	assert.Equal(t, uint32(0), bp.UsedIndex[C32T64(3, 0)])
	assert.Equal(t, uint32(1), bp.GetBufferSize(0))
	assert.Equal(t, uint32(500+IKCP_OVERHEAD+IKCP_OVERHEAD+reserved), bp.UsedIndex[C32T64(0, 0)])

	verifySeg(t, (*bp.Used[C32T64(0, 0)])[500+IKCP_OVERHEAD+reserved:], IKCP_OVERHEAD, 777, expectConv, expectByte)
}

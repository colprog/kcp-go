package kcp

import (
	"net"
	"sync/atomic"

	"github.com/pkg/errors"
)

func (s *UDPSession) defaultReadLoop() {
	buf := make([]byte, mtuLimit)
	var src string
	for {
		if n, addr, err := s.conn.ReadFrom(buf); err == nil {
			// make sure the packet is from the same source
			isFromMeteredIP := s.isFromMeteredIP(addr)
			if isFromMeteredIP {
			} else if src == "" { // set source address
				src = addr.String()
			} else if addr.String() != src {
				atomic.AddUint64(&DefaultSnmp.InErrs, 1)
				continue
			}
			s.packetInput(buf[:n], isFromMeteredIP)
		} else {
			s.notifyReadError(errors.WithStack(err))
			return
		}
	}
}

func (l *Listener) defaultMonitor() {
	buf := make([]byte, mtuLimit)
	for {
		if n, from, err := l.conn.ReadFrom(buf); err == nil {
			l.packetInput(buf[:n], from)
		} else {
			l.notifyReadError(errors.WithStack(err))
			return
		}
	}
}

func (s *UDPSession) isFromMeteredIP(addr net.Addr) bool {
	if s.meteredRemote == nil {
		return false
	}

	switch addr := addr.(type) {
	case *net.UDPAddr:
		return addr.IP.Equal(s.meteredRemote.IP)
	case *net.TCPAddr:
		return addr.IP.Equal(s.meteredRemote.IP)
	}

	return false
}

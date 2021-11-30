// +build linux

package kcp

import (
	"net"
	"os"
	"sync/atomic"

	"github.com/pkg/errors"
)

func (s *UDPSession) tx() {
	// default version
	if s.xconn == nil || s.xconnWriteError != nil {
		s.defaultTx()
		return
	}

	// x/net version
	nbytes := 0
	npkts := 0
	for len(s.txqueue) > 0 {
		if n, err := s.xconn.WriteBatch(s.txqueue, 0); err == nil {
			for k := range s.txqueue[:n] {
				nbytes += len(s.txqueue[k].Buffers[0])
			}
			npkts += n
			s.txqueue = s.txqueue[n:]
		} else {
			// compatibility issue:
			// for linux kernel<=2.6.32, support for sendmmsg is not available
			// an error of type os.SyscallError will be returned
			if operr, ok := err.(*net.OpError); ok {
				if se, ok := operr.Err.(*os.SyscallError); ok {
					if se.Syscall == "sendmmsg" {
						s.xconnWriteError = se
						s.defaultTx()
						return
					}
				}
			}
			s.notifyWriteError(errors.WithStack(err))
			break
		}
	}

	for len(s.meteredTxqueue) > 0 {
		if n, err := s.xconn.WriteBatch(s.meteredTxqueue, 0); err == nil {
			for k := range s.meteredTxqueue[:n] {
				nbytes += len(s.meteredTxqueue[k].Buffers[0])
			}
			npkts += n
			s.meteredTxqueue = s.meteredTxqueue[n:]
		} else {
			// compatibility issue:
			// for linux kernel<=2.6.32, support for sendmmsg is not available
			// an error of type os.SyscallError will be returned
			if operr, ok := err.(*net.OpError); ok {
				if se, ok := operr.Err.(*os.SyscallError); ok {
					if se.Syscall == "sendmmsg" {
						s.defaultTx()
						return
					}
				}
			}
			s.notifyWriteError(errors.WithStack(err))
			break
		}
	}

	atomic.AddUint64(&DefaultSnmp.OutPkts, uint64(npkts))
	atomic.AddUint64(&DefaultSnmp.OutBytes, uint64(nbytes))
}

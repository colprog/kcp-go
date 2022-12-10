package kcp

import (
	"sync/atomic"

	"github.com/pkg/errors"
)

func (s *UDPSession) defaultTx() {
	nbytes := 0
	npkts := 0
	for k := range s.txqueue {

		if n, err := s.conn.WriteTo(s.txqueue[k].Buffers[0], s.txqueue[k].Addr); err == nil {
			nbytes += n
			npkts++
		} else {
			s.notifyWriteError(errors.WithStack(err))
			break
		}
	}
	if s.meteredRemote != nil {
		for k := range s.meteredTxqueue {

			if n, err := s.conn.WriteTo(s.meteredTxqueue[k].Buffers[0], s.meteredTxqueue[k].Addr); err == nil {
				nbytes += n
				npkts++
			} else {
				s.notifyWriteError(errors.WithStack(err))
				break
			}
		}
	}
	atomic.AddUint64(&DefaultSnmp.OutPkts, uint64(npkts))
	atomic.AddUint64(&DefaultSnmp.OutBytes, uint64(nbytes))
}

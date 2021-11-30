// The MIT License (MIT)
//
// Copyright (c) 2015 xtaci
//
// Permission is hereby granted, free of charge, to any person obtaining a copy
// of this software and associated documentation files (the "Software"), to deal
// in the Software without restriction, including without limitation the rights
// to use, copy, modify, merge, publish, distribute, sublicense, and/or sell
// copies of the Software, and to permit persons to whom the Software is
// furnished to do so, subject to the following conditions:
//
// The above copyright notice and this permission notice shall be included in all
// copies or substantial portions of the Software.
//
// THE SOFTWARE IS PROVIDED "AS IS", WITHOUT WARRANTY OF ANY KIND, EXPRESS OR
// IMPLIED, INCLUDING BUT NOT LIMITED TO THE WARRANTIES OF MERCHANTABILITY,
// FITNESS FOR A PARTICULAR PURPOSE AND NONINFRINGEMENT. IN NO EVENT SHALL THE
// AUTHORS OR COPYRIGHT HOLDERS BE LIABLE FOR ANY CLAIM, DAMAGES OR OTHER
// LIABILITY, WHETHER IN AN ACTION OF CONTRACT, TORT OR OTHERWISE, ARISING FROM,
// OUT OF OR IN CONNECTION WITH THE SOFTWARE OR THE USE OR OTHER DEALINGS IN THE
// SOFTWARE.

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

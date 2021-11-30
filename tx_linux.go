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

//go:build linux
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

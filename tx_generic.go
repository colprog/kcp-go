// +build !linux

package kcp

func (s *UDPSession) tx() {
	s.defaultTx()
}

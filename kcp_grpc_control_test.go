package kcp

import (
	"fmt"
	"testing"
	"time"
)

// User should make sure meterIp + meteredPort is a loopback interface
var meteredIp = "192.168.0.107"
var meteredPort = 10011

const (
	localIp   = "0.0.0.0"
	localPort = 10001

	controlIp   = "0.0.0.0"
	controlPort = 10720

	controlDetectIp   = "0.0.0.0"
	controlDetectPort = 10720

	clientRegisteBackupIp   = "127.0.0.1"
	clientRegisteBackupPort = 10021

	buffSize = 1024
)

const (
	ServerCloseSignal     = 1
	ServerBeginDropSignal = 2
	ServerStopDropSignal  = 3
	// todo
)

var (
	chanServer = make(chan int)
	signal     int
)

func init() {
	LoggerDefault()
}

func serverSignal(l *Listener) {
	for {
		signal = <-chanServer
		fmt.Printf("signal: %d\n", signal)
		switch signal {
		case ServerCloseSignal:
			l.Close()
			return
		case ServerBeginDropSignal:
			l.dropOpen()
		case ServerStopDropSignal:
			l.dropOff()
		}
	}

}

func startServer(t *testing.T) error {
	LogTest("Test server side started.")
	listenSlowAddrStr := fmt.Sprintf("%s:%d", localIp, localPort)

	LogTest("Server slow path listen to: %s", listenSlowAddrStr)
	l, err := ListenWithDrop(listenSlowAddrStr, 1)
	if err != nil {
		t.Fatal(err)
	}

	l.NewControllerConfig(nil)
	go serverSignal(l)

	for {
		s, err := l.AcceptKCP()
		s.SetMeteredAddr(meteredIp, uint16(meteredPort), true)
		LogTest("Server slow path got session on")
		if err != nil {
			t.Fatal(err)
		}
		go handleMessage(s, t)
	}

}

func handleMessage(conn *UDPSession, t *testing.T) {
	buf := make([]byte, buffSize)
	for {
		_, err := conn.Read(buf)
		if err != nil {
			t.Fatal(err)
		}
	}
}

func TestStartServer(t *testing.T) {
	go startServer(t)
	time.Sleep(time.Second * 10)
	chanServer <- ServerCloseSignal
}

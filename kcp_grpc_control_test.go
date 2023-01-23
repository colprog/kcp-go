package kcp

import (
	"context"
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/docker/docker/pkg/reexec"
	"github.com/stretchr/testify/assert"
	"github.com/xtaci/kcp-go/v5/grpc_control"
	"google.golang.org/grpc"
)

// User should make sure meterIp + meteredPort is a loopback interface
var meteredIp = "192.168.0.108"
var meteredPort = 10011

const (
	localIp   = "0.0.0.0"
	localPort = 10001

	controlIp         = "0.0.0.0"
	controlPort       = 10720
	clientControlPort = 10722

	controlDetectIp   = "0.0.0.0"
	controlDetectPort = 10721

	clientRegisteBackupIp   = "127.0.0.1"
	clientRegisteBackupPort = 10021

	buffSize = 1024

	clientDetectInterval = 10
	clientDetectRate     = 0.9
)

const (
	ServerCloseSignal     = 1
	ServerBeginDropSignal = 2
	ServerStopDropSignal  = 3
)

const (
	ClientCloseSignal = 11
)

var (
	chanServer   = make(chan int)
	chanClient   = make(chan int)
	serverSignal int
	clientSignal int

	listener   *Listener
	cliSession *UDPSession
)

func init() {
	LoggerDefault()

	reexec.Register("serverProcess", serverProcess)
	if reexec.Init() {
		os.Exit(0)
	}
}

func serverProcess() {
	LogTest("serverProcess started")
	startServer(nil)
}

func processServerSignal(l *Listener) {
	for {
		serverSignal = <-chanServer
		fmt.Printf("serverSignal: %d\n", serverSignal)
		switch serverSignal {
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

func startServer(t *testing.T) {
	LogTest("Test server side started.")
	var err error

	listenAddrStr := fmt.Sprintf("%s:%d", localIp, localPort)

	LogTest("Server listen to: %s", listenAddrStr)
	listener, err = ListenWithDrop(listenAddrStr, 1)
	if t != nil {
		assert.NoError(t, err)
	} else if err != nil {
		LogTest("Error: %v", err)
		return
	}

	listener.dropOff()

	listener.NewControllerConfig(&ControllerServerConfig{
		controllerIP:   controlIp,
		controllerPort: controlPort,
		allowDetect:    true,

		dectedIP:   controlDetectIp,
		dectedPort: controlDetectPort,

		// no need set in server side
		enableOriginRouteDetect:       false,
		satisfyingDetectionRate:       0,
		routeDetectTimes:              0,
		detectPackageNumbersEachTimes: 0,
	})
	go processServerSignal(listener)

	for {
		s, err := listener.AcceptKCP()
		s.SetMeteredAddr(meteredIp, uint16(meteredPort), true)
		LogTest("Server slow path got session on")
		if t != nil {
			assert.NoError(t, err)
		} else if err != nil {
			LogTest("Error: %v", err)
			return
		}
		go handleMessage(s, t)
	}
}

func handleMessage(conn *UDPSession, t *testing.T) {
	buf := make([]byte, buffSize)
	for {
		n, err := conn.Read(buf)
		LogTest("recv: %d", n)
		if t != nil {
			assert.NoError(t, err)
		} else if err != nil {
			LogTest("Error: %v", err)
			return
		}
	}
}

func processClientSignal(sess *UDPSession) {
	for {
		clientSignal = <-chanClient
		fmt.Printf("clientSignal: %d\n", clientSignal)
		switch clientSignal {
		case ClientCloseSignal:
			sess.Close()
			return
		}
	}
}

func startClient(t *testing.T) {
	serverAddrStr := fmt.Sprintf("%s:%d", localIp, localPort)
	LogTest("Connect to: %s", serverAddrStr)
	var err error

	cliSession, err = Dial(serverAddrStr)
	assert.NoError(t, err)
	assert.NotEqual(t, nil, cliSession)

	cliSession.SetMeteredAddr(meteredIp, uint16(meteredPort), true)

	cliConfig := &ControllerServerConfig{
		controllerIP:   controlIp,
		controllerPort: clientControlPort,

		// no effect in client side
		allowDetect: false,

		dectedIP:   controlDetectIp,
		dectedPort: controlDetectPort,

		enableOriginRouteDetect:       true,
		satisfyingDetectionRate:       0.7,
		routeDetectTimes:              10,
		detectPackageNumbersEachTimes: 10,
	}
	cliSession.SetControllerServer(NewSessionControllerServer(cliConfig, false))

	go processClientSignal(cliSession)

	err = cliSession.EnableMonitor(uint64(clientDetectInterval), clientDetectRate)
	assert.NoError(t, err)

	for {
		data := make([]byte, buffSize)

		n, err := cliSession.Write([]byte(data))
		LogTest("sent: %d", n)
		assert.NoError(t, err)
		time.Sleep(time.Millisecond * 500)
	}
}

func TestStartServer(t *testing.T) {
	go startServer(t)
	time.Sleep(time.Second * 10)
	assert.NotEqual(t, nil, listener)
	assert.NotEqual(t, nil, listener.conn)

	chanServer <- ServerCloseSignal
}

func TestStartClient(t *testing.T) {
	go startClient(t)
	time.Sleep(time.Second * 10)
	assert.NotEqual(t, nil, cliSession)
	assert.NotEqual(t, nil, cliSession.conn)

	chanClient <- ClientCloseSignal
}

func TestServerSignal(t *testing.T) {
	go startServer(t)
	time.Sleep(time.Second * 10)
	assert.NotEqual(t, nil, listener)
	assert.NotEqual(t, nil, listener.conn)
	assert.Equal(t, false, listener.isDropOpen())

	chanServer <- ServerBeginDropSignal
	time.Sleep(time.Second * 1)
	assert.Equal(t, true, listener.isDropOpen())

	chanServer <- ServerStopDropSignal
	time.Sleep(time.Second * 1)
	assert.Equal(t, false, listener.isDropOpen())

	chanServer <- ServerCloseSignal
}

func TestServerControllerRPC(t *testing.T) {
	go startServer(t)
	time.Sleep(time.Second * 10)
	assert.NotEqual(t, nil, listener)
	assert.NotEqual(t, nil, listener.conn)
	assert.Equal(t, false, listener.isDropOpen())

	// 1. conntect to grpc
	conn, err := grpc.Dial(fmt.Sprintf("%s:%d", controlIp, controlPort), grpc.WithInsecure())
	assert.NoError(t, err)
	defer conn.Close()

	KCPSessionCtlCli := grpc_control.NewKCPSessionCtlClient(conn)
	assert.NotEqual(t, nil, KCPSessionCtlCli)

	// 2. test GetSessions
	getSessionsReply, err := KCPSessionCtlCli.GetSessions(context.Background(), &grpc_control.GetSessionsRequest{})

	assert.NoError(t, err)
	assert.Equal(t, 1, len(getSessionsReply.Connections))

	// 3. test RegsiterNewSession
	registNewSessionReply, err := KCPSessionCtlCli.RegsiterNewSession(context.Background(), &grpc_control.RegsiterNewSessionRequest{
		IpAddress: clientRegisteBackupIp,
		Port:      clientRegisteBackupPort,
	})

	assert.NoError(t, err)
	assert.NotEqual(t, nil, registNewSessionReply)

	assert.Equal(t, true, listener.contollerServer.newRegistered)
	assert.Equal(t, clientRegisteBackupIp, listener.contollerServer.registerIP)
	assert.Equal(t, clientRegisteBackupPort, listener.contollerServer.registerPort)

	// 4. test RegsiterNewSession again
	registNewSessionReply, err = KCPSessionCtlCli.RegsiterNewSession(context.Background(), &grpc_control.RegsiterNewSessionRequest{
		IpAddress: clientRegisteBackupIp,
		Port:      clientRegisteBackupPort + 1,
	})

	assert.NoError(t, err)
	assert.NotEqual(t, nil, registNewSessionReply)

	assert.Equal(t, true, listener.contollerServer.newRegistered)
	assert.Equal(t, clientRegisteBackupPort+1, listener.contollerServer.registerPort)

	chanServer <- ServerCloseSignal
}

func TestServerForkServer(t *testing.T) {
	cmd := reexec.Command("serverProcess")
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	err := cmd.Start()
	assert.NoError(t, err)
	time.Sleep(time.Second * 10)

	err = cmd.Process.Signal(os.Interrupt)
	assert.NoError(t, err)
}

func TestServerForkServerAndRoutineClient(t *testing.T) {
	cmd := reexec.Command("serverProcess")
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	err := cmd.Start()
	assert.NoError(t, err)
	time.Sleep(time.Second * 5)

	go startClient(t)

	time.Sleep(time.Second * 5)
	assert.NotEqual(t, nil, cliSession)
	assert.NotEqual(t, nil, cliSession.conn)

	time.Sleep(time.Second * 10)

	err = cmd.Process.Signal(os.Interrupt)
	assert.NoError(t, err)

	chanClient <- ClientCloseSignal
}

// TBD

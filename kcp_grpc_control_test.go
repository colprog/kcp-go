package kcp

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"syscall"
	"testing"
	"time"

	"github.com/docker/docker/pkg/reexec"
	"github.com/stretchr/testify/assert"
	"github.com/xtaci/kcp-go/v5/grpc_control"
	"google.golang.org/grpc"
)

const (
	localIp   = "0.0.0.0"
	localPort = 10001

	controlIp         = "0.0.0.0"
	controlPort       = 10720
	clientControlPort = 10722

	controlDetectIp   = "0.0.0.0"
	controlDetectPort = 10721

	clientRegisteBackupIp   = "127.0.0.1"
	clientRegisteBackupPort = localPort

	buffSize = 1024

	clientDetectInterval = 10
	clientDetectRate     = 0.9
)

// User should make sure meterIp is a loopback interface
const meteredIp = "192.168.0.102"
const meteredPort = localPort

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

	chanProcessServer = make(chan os.Signal, 1)

	listener   *Listener
	cliSession *UDPSession
)

func init() {
	LoggerDefault()

	reexec.Register("serverProcess", serverProcess)
	reexec.Register("serverProcess2", serverProcess2)
	if reexec.Init() {
		os.Exit(0)
	}
}

func serverProcess() {
	LogTest("serverProcess started")
	startServer(nil)
}

func serverProcess2() {
	LogTest("serverProcess2 started")
	startServerWithoutDetect(nil)
}

func processRoutineServerSignal(l *Listener) {
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

func processServerSignal(l *Listener) {
	for {
		serverProcessSignal := <-chanProcessServer
		fmt.Printf("serverProcessSignal: %d\n", serverProcessSignal)
		switch serverProcessSignal {
		case syscall.SIGUSR1:
			l.dropOpen()
		case syscall.SIGUSR2:
			l.dropOff()
		}
	}
}

func startServer(t *testing.T) {
	startServerWithOptions(t, &SessionControllerConfig{
		ControllerIP:   controlIp,
		ControllerPort: controlPort,
		StartGRPC:      true,

		AllowDetect:  true,
		DetectedIP:   controlDetectIp,
		DetectedPort: controlDetectPort,

		// no need set in server side
		EnableOriginRouteDetect:       false,
		SatisfyingDetectionRate:       0,
		RouteDetectTimes:              0,
		DetectPackageNumbersEachTimes: 0,
	})
}

func startServerWithoutDetect(t *testing.T) {
	startServerWithOptions(t, &SessionControllerConfig{
		ControllerIP:   controlIp,
		ControllerPort: controlPort,
		StartGRPC:      true,

		AllowDetect:  false,
		DetectedIP:   controlDetectIp,
		DetectedPort: controlDetectPort,

		// no need set in server side
		EnableOriginRouteDetect:       false,
		SatisfyingDetectionRate:       0,
		RouteDetectTimes:              0,
		DetectPackageNumbersEachTimes: 0,
	})
}

func startServerWithOptions(t *testing.T, sessionControllerConfig *SessionControllerConfig) {
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

	listener.NewControllerServer(sessionControllerConfig)
	if t != nil {
		go processRoutineServerSignal(listener)
	} else {
		signal.Notify(chanProcessServer, syscall.SIGUSR1, syscall.SIGUSR2)
		go processServerSignal(listener)
	}

	for {
		s, err := listener.AcceptKCP()
		s.SetMeteredAddr(meteredIp, uint16(meteredPort), true)
		LogTest("Server got session on")
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

	cliConfig := &SessionControllerConfig{
		ControllerIP:   controlIp,
		ControllerPort: clientControlPort,
		StartGRPC:      true,

		// no effect in client side
		AllowDetect: false,

		DetectedIP:   controlDetectIp,
		DetectedPort: controlDetectPort,

		EnableOriginRouteDetect:       true,
		SatisfyingDetectionRate:       0.7,
		RouteDetectTimes:              10,
		DetectPackageNumbersEachTimes: 10,
	}
	cliSession.SetSessionController(NewSessionController(cliConfig, false))

	go processClientSignal(cliSession)

	cliSession.EnableMonitor(uint64(clientDetectInterval), clientDetectRate)

	for {
		data := make([]byte, buffSize)

		n, err := cliSession.Write([]byte(data))
		LogTest("sent: %d", n)
		assert.NoError(t, err)
		time.Sleep(time.Millisecond * 500)
	}
}

func TestStartServerWithoutGRPC(t *testing.T) {
	go startServerWithOptions(t, &SessionControllerConfig{
		ControllerIP:   controlIp,
		ControllerPort: controlPort,
		StartGRPC:      false,

		AllowDetect:  true,
		DetectedIP:   controlDetectIp,
		DetectedPort: controlDetectPort,

		// no need set in server side
		EnableOriginRouteDetect:       false,
		SatisfyingDetectionRate:       0,
		RouteDetectTimes:              0,
		DetectPackageNumbersEachTimes: 0,
	})
	time.Sleep(time.Second * 10)
	assert.NotEqual(t, nil, listener)
	assert.NotEqual(t, nil, listener.conn)

	// still can create client
	conn, err := grpc.Dial(fmt.Sprintf("%s:%d", controlIp, controlPort), grpc.WithInsecure())
	assert.NoError(t, err)
	assert.NotEqual(t, conn, nil)
	KCPSessionCtlCli := grpc_control.NewKCPSessionCtlClient(conn)
	assert.NotEqual(t, nil, KCPSessionCtlCli)

	// but will fail to call grpc function
	_, err = KCPSessionCtlCli.GetSessions(context.Background(), &grpc_control.GetSessionsRequest{})
	assert.Error(t, err)

	chanServer <- ServerCloseSignal
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

	assert.Equal(t, true, listener.ContollerServer.newRegistered)
	assert.Equal(t, clientRegisteBackupIp, listener.ContollerServer.registerIP)
	assert.Equal(t, clientRegisteBackupPort, listener.ContollerServer.registerPort)

	// 4. test RegsiterNewSession again
	registNewSessionReply, err = KCPSessionCtlCli.RegsiterNewSession(context.Background(), &grpc_control.RegsiterNewSessionRequest{
		IpAddress: clientRegisteBackupIp,
		Port:      clientRegisteBackupPort + 1,
	})

	assert.NoError(t, err)
	assert.NotEqual(t, nil, registNewSessionReply)

	assert.Equal(t, true, listener.ContollerServer.newRegistered)
	assert.Equal(t, clientRegisteBackupPort+1, listener.ContollerServer.registerPort)

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

	err = cmd.Process.Signal(syscall.SIGTERM)
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

	err = cmd.Process.Signal(syscall.SIGTERM)
	assert.NoError(t, err)

	chanClient <- ClientCloseSignal
}

func TestServerRoutineNoSwitch(t *testing.T) {
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
	assert.Equal(t, SessionTypeExistMetered, globalSessionType)

	for i := 0; i < 10; i++ {
		time.Sleep(time.Second * 10)
		// won't change session type if routine is fine
		assert.Equal(t, SessionTypeExistMetered, globalSessionType)
	}

	err = cmd.Process.Signal(syscall.SIGTERM)
	assert.NoError(t, err)

	chanClient <- ClientCloseSignal
}

func TestServerRoutineAutoSwitch(t *testing.T) {
	// 1. start server in fork process(without testing context)
	cmd := reexec.Command("serverProcess")
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	err := cmd.Start()
	assert.NoError(t, err)
	time.Sleep(time.Second * 5)

	// 2. begin to drop all package which not from alter route
	err = cmd.Process.Signal(syscall.SIGUSR1)
	assert.NoError(t, err)

	// 3. start client
	go startClient(t)

	time.Sleep(time.Second * 5)
	assert.NotEqual(t, nil, cliSession)
	assert.NotEqual(t, nil, cliSession.conn)

	cliSession.controller.DisAllowDectecting()
	cliSession.controller.DisAllowSwitchBakcup()

	// 4. monitor do round and check global status
	time.Sleep(time.Second * 20)
	assert.Equal(t, SessionTypeOnlyMetered, globalSessionType)

	// 5. allow dectecting
	cliSession.controller.AllowDectecting()
	time.Sleep(time.Second * 10)
	assert.Equal(t, SessionTypeExistMetered, globalSessionType)
	cliSession.controller.DisAllowDectecting()

	// will roll back to SessionTypeOnlyMetered, because set the route all dropt
	time.Sleep(time.Second * 10)
	assert.Equal(t, SessionTypeOnlyMetered, globalSessionType)

	// 6. begin to register a backup route
	conn, err := grpc.Dial(fmt.Sprintf("%s:%d", controlIp, clientControlPort), grpc.WithInsecure())
	assert.NoError(t, err)
	defer conn.Close()

	KCPSessionCtlCli := grpc_control.NewKCPSessionCtlClient(conn)
	assert.NotEqual(t, nil, KCPSessionCtlCli)

	registNewSessionReply, err := KCPSessionCtlCli.RegsiterNewSession(context.Background(), &grpc_control.RegsiterNewSessionRequest{
		IpAddress: clientRegisteBackupIp,
		Port:      clientRegisteBackupPort,
	})

	assert.NoError(t, err)
	assert.NotEqual(t, nil, registNewSessionReply)
	assert.Equal(t, true, cliSession.controller.newRegistered)

	// 7. test backup route work
	err = cmd.Process.Signal(syscall.SIGUSR2)
	assert.NoError(t, err)

	cliSession.controller.AllowSwitchBakcup()
	time.Sleep(time.Second * 12)
	assert.Equal(t, SessionTypeExistMetered, globalSessionType)
	assert.Equal(t, false, cliSession.controller.newRegistered)

	time.Sleep(time.Second * 12)
	assert.Equal(t, SessionTypeExistMetered, globalSessionType)

	// close server and client
	err = cmd.Process.Signal(syscall.SIGTERM)
	assert.NoError(t, err)

	chanClient <- ClientCloseSignal
}

func TestServerRoutineAutoSwitch2(t *testing.T) {
	cmd := reexec.Command("serverProcess2")
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	err := cmd.Start()
	assert.NoError(t, err)
	time.Sleep(time.Second * 5)

	// 2. begin to drop all package which not from alter route
	err = cmd.Process.Signal(syscall.SIGUSR1)
	assert.NoError(t, err)

	// 3. start client
	go startClient(t)

	time.Sleep(time.Second * 20)
	// 4. check status should not changed
	assert.Equal(t, SessionTypeOnlyMetered, globalSessionType)
	assert.NotEqual(t, nil, cliSession)
	assert.NotEqual(t, nil, cliSession.conn)

	// 5. after all done, should not changed
	time.Sleep(time.Second * 100)
	assert.Equal(t, SessionTypeOnlyMetered, globalSessionType)
	assert.Equal(t, SessionTypeOnlyMetered, globalSessionType)
	assert.NotEqual(t, nil, cliSession.conn)

	// close server and client
	err = cmd.Process.Signal(syscall.SIGTERM)
	assert.NoError(t, err)

	chanClient <- ClientCloseSignal
}

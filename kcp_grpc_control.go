package kcp

import (
	"context"
	"crypto/rand"
	"fmt"
	"net"
	"sync/atomic"
	"time"

	"github.com/pkg/errors"
	pb "github.com/xtaci/kcp-go/v5/grpc_control"
	"google.golang.org/grpc"
)

const (
	DETECTION_MAGIC = 0x13213d
)

type SessionControllerConfig struct {
	// server side control config
	StartGRPC      bool
	ControllerIP   string
	ControllerPort int

	AllowDetect bool

	// both side
	// dectedIP and dectedPort:
	// - for server side need allowDetect = true
	// - for client side need enableOriginRouteDetect = true
	DetectedIP   string
	DetectedPort int

	// client side control config
	EnableOriginRouteDetect       bool
	SatisfyingDetectionRate       float32
	RouteDetectTimes              uint16
	DetectPackageNumbersEachTimes uint16
}

func (c *SessionControllerConfig) SetControllerIP(ip string) {
	c.ControllerIP = ip
}

func (c *SessionControllerConfig) SetControllerPort(port int) {
	c.ControllerPort = port
}

func NewDefaultConfig() *SessionControllerConfig {
	c := new(SessionControllerConfig)
	c.StartGRPC = true
	c.ControllerIP = "0.0.0.0"
	c.ControllerPort = 10720
	c.EnableOriginRouteDetect = true
	c.RouteDetectTimes = 10
	c.SatisfyingDetectionRate = 0.7
	c.DetectPackageNumbersEachTimes = 10
	c.AllowDetect = true
	c.DetectedIP = "0.0.0.0"
	c.DetectedPort = 10721
	return c
}

type SessionController struct {
	// 0 means no dectecting
	// 1 means dectecting
	// -1 means dectection fail
	isDectecting        int32
	dectectresultVeried float32

	newRegistered bool
	registerIP    string
	registerPort  int

	config *SessionControllerConfig

	// only for test
	allowDectecting   bool
	allowSwitchBakcup bool
}

func (sessionController *SessionController) RegsiterNewRoute(IpAddress string, Port int32) {
	sessionController.newRegistered = true
	sessionController.registerIP = IpAddress
	sessionController.registerPort = int(Port)
}

func (sessionController *SessionController) DisAllowDectecting() {
	sessionController.allowDectecting = false
}

func (sessionController *SessionController) AllowDectecting() {
	sessionController.allowDectecting = true
}

func (sessionController *SessionController) DisAllowSwitchBakcup() {
	sessionController.allowSwitchBakcup = false
}

func (sessionController *SessionController) AllowSwitchBakcup() {
	sessionController.allowSwitchBakcup = true
}

func (sessionController *SessionController) SwitchGlobalSessionType(sessionType int32, flushDetecting bool) error {
	if flushDetecting && atomic.LoadInt32(&sessionController.isDectecting) == -1 {
		atomic.StoreInt32(&sessionController.isDectecting, 0)
	}

	switch sessionType {
	case SessionTypeNormal:
		RunningAsNormal()
	case SessionTypeExistMetered:
		RunningAsExistMetered()
	case SessionTypeOnlyMetered:
		RunningAsOnlyMetered()
	default:
		return errors.New(fmt.Sprintf("Inavlid sessionType, sessionType should between [%d,%d]",
			SessionTypeOnlyMeteredMin, SessionTypeOnlyMeteredMax))
	}
	return nil
}

func (sessionController *SessionController) GetGlobalSessionTypeValue() int32 {
	return atomic.LoadInt32(&globalSessionType)
}

func NewSessionController(config *SessionControllerConfig, serverSide bool) *SessionController {
	s := &SessionController{}

	if config == nil {
		config = NewDefaultConfig()
	}
	s.config = config
	s.allowDectecting = true
	s.allowSwitchBakcup = true

	if config.StartGRPC {
		sessionControllerServer := SessionControllerServer{}
		sessionControllerServer.sessionController = s

		rpcAddr := fmt.Sprintf("%s:%d", config.ControllerIP, config.ControllerPort)
		li, err := net.Listen("tcp", rpcAddr)
		if err != nil {
			LogFatalf("failed to listen: %v", err)
		}

		LogInfo("Controller listening on %s\n", rpcAddr)
		go func() {
			grpcServer := grpc.NewServer()
			pb.RegisterKCPSessionCtlServer(grpcServer, &sessionControllerServer)
			grpcServer.Serve(li)
		}()
	} else {
		LogInfo("Controller GRPC is disabled")
	}

	if serverSide && config.AllowDetect {
		go func() {
			detectAddrStr := fmt.Sprintf("%s:%d", config.DetectedIP, config.DetectedPort)
			detectAddr, err := net.ResolveUDPAddr("udp", detectAddrStr)
			if err != nil {
				LogFatalf("detect udp service fail to reslove addr: %s", detectAddrStr)
			}
			LogInfo("Controller detect port listening on %s\n", detectAddr)

			err = DetectPackageService(detectAddr)
			if err != nil {
				LogFatalf("detect udp service got err: %v", err)
			}
		}()
	}

	if !serverSide && config.AllowDetect {
		LogWarn("no effect after client side allow detect")
	}

	return s
}

func (server *SessionController) resetRegisterServer() {
	server.newRegistered = false
}

// structure used to register grpc
// The structure is decoupled from SessionController
// After kcp-vpn registered grpc, the grpc service must not be used
type SessionControllerServer struct {
	pb.UnimplementedKCPSessionCtlServer

	sessionController *SessionController
}

func (server *SessionControllerServer) GetSessions(context.Context, *pb.GetSessionsRequest) (*pb.GetSessionsReply, error) {
	reply := pb.GetSessionsReply{}

	if server.sessionController == nil {
		return &reply, errors.New("SessionController have not been inited.")
	}

	if DefaultSnmp == nil {
		DefaultSnmp = newSnmp()
	}

	s := DefaultSnmp.Copy()
	reply.Connections = make([]*pb.ConnectionInfo, 1)
	d := new(pb.ConnectionInfo)

	// No need atomic load here
	// Cause s = s.Copy()
	d.SentBytes = s.BytesSent
	d.RecvBytes = s.BytesReceived
	d.DroptBytes = s.BytesDropt
	d.MaxConn = s.MaxConn
	d.ActiveOpens = s.ActiveOpens
	d.PassiveOpens = s.PassiveOpens
	d.CurrEstab = s.CurrEstab
	d.InErrs = s.InErrs
	d.InCsumErrs = s.InCsumErrors
	d.KcpInErrs = s.KCPInErrors
	d.InPkts = s.InPkts
	d.OutPkts = s.OutPkts
	d.InSegs = s.InSegs
	d.OutSegs = s.OutSegs
	d.InBytes = s.InBytes
	d.OutBytes = s.OutBytes
	d.RetransSegs = s.RetransSegs
	d.FastRetransSegs = s.FastRetransSegs
	d.EarlyRetransSegs = s.EarlyRetransSegs
	d.LostSegs = s.LostSegs
	d.RepeatSegs = s.RepeatSegs
	d.FecParityShards = s.FECParityShards
	d.FecErrs = s.FECErrs
	d.FecRecovered = s.FECRecovered
	d.FecShortShards = s.FECShortShards

	d.BytesSentFromNoMetered = s.BytesSentFromNoMeteredRaw
	d.BytesSentFromMetered = s.BytesSentFromMeteredRaw
	d.BytesRecvFromNoMetered = s.BytesReceivedFromNoMeteredRaw
	d.BytesRecvFromMetered = s.BytesReceivedFromMeteredRaw
	d.SegsAcked = s.SegmentNumbersACKed
	d.SegsPromoteAcked = s.SegmentNumbersPromotedACKed

	d.Status = pb.SessionStatus(atomic.LoadInt32(&globalSessionType))

	reply.Connections[0] = d

	return &reply, nil
}

func (server *SessionControllerServer) RegsiterNewSession(_ context.Context, request *pb.RegsiterNewSessionRequest) (*pb.RegsiterNewSessionReply, error) {
	reply := pb.RegsiterNewSessionReply{}
	if server.sessionController == nil {
		return &reply, errors.New("SessionController have not been inited.")
	}

	server.sessionController.RegsiterNewRoute(request.IpAddress, request.Port)

	return &reply, nil
}

func LoopExistMetered(sessMonitor *UDPSessionMonitor, detectRate float64) (typeChanged bool) {

	cSegmentACKed := atomic.LoadUint64(&DefaultSnmp.SegmentNumbersACKed)
	cSegmentPromotedACKed := atomic.LoadUint64(&DefaultSnmp.SegmentNumbersPromotedACKed)
	typeChanged = false

	if sessMonitor.lastSegmentAcked > cSegmentACKed || sessMonitor.lastSegmentPromotedAcked > cSegmentPromotedACKed {
		// no package, skipped
		sessMonitor.lastSegmentAcked = cSegmentACKed
		sessMonitor.lastSegmentPromotedAcked = cSegmentPromotedACKed
		return
	}

	dSegmentACKed := cSegmentACKed - sessMonitor.lastSegmentAcked
	dSegmentPromotedACKed := cSegmentPromotedACKed - sessMonitor.lastSegmentPromotedAcked

	LogInfo("Loop detect route status. dSegmentACKed: %d, dSegmentPromotedACKed: %d", dSegmentACKed, dSegmentPromotedACKed)

	if dSegmentACKed != 0 && (float64(dSegmentPromotedACKed)/float64(dSegmentACKed) > detectRate) {
		// change to only meter route
		RunningAsOnlyMetered()
		typeChanged = true
	}

	sessMonitor.lastSegmentAcked = cSegmentACKed
	sessMonitor.lastSegmentPromotedAcked = cSegmentPromotedACKed
	return
}

func DetectPackageService(detectAddr *net.UDPAddr) error {
	listener, err := net.ListenUDP("udp", detectAddr)
	if err != nil {
		return err
	}

	data := make([]byte, 1024)
	for {
		n, remoteAddr, err := listener.ReadFromUDP(data)
		if err != nil {
			return err
		}

		LogDebug("detect ReadFromUDP get %d data", n)

		DetectPackageProcess(data, n, listener, remoteAddr)
	}

}

func DetectPackageProcess(data []byte, rev_size int, listener *net.UDPConn, remoteAddr *net.UDPAddr) error {

	match := DetectPackageVerify(data, rev_size)
	if match {
		ack_data := make([]byte, IKCP_ALIVE_DETECT_HEAD)

		ikcp_encode32u(ack_data, DETECTION_MAGIC)
		_, err := listener.WriteToUDP(ack_data, remoteAddr)
		if err != nil {
			return err
		}
	}

	return nil
}

func DetectPackageVerify(data []byte, rev_size int) (verifed bool) {
	verifed = false
	if rev_size != IKCP_ALIVE_DETECTION {
		LogWarn("detect package parse failed. package length not match. rev size: %d, need %d", rev_size, IKCP_ALIVE_DETECTION)
		return
	}

	var magic uint32

	data = ikcp_decode32u(data, &magic)
	if magic != DETECTION_MAGIC {
		LogWarn("detect package parse failed. magic not match")
		return
	}

	r := data[0]

	verifed = true
	for j := 1; j < IKCP_ALIVE_DETECTION-IKCP_ALIVE_DETECT_HEAD; j++ {
		if data[j] != r {
			LogWarn("detect package parse failed. data not same.")
			verifed = false
			break
		}
	}
	return
}

func DetectOriginRouteProcess(interval uint64, controller *SessionController) (changed bool) {
	changed = false
	buf := make([]byte, IKCP_ALIVE_DETECTION)
	ikcp_encode32u(buf, DETECTION_MAGIC)
	r := make([]byte, 1)

	detectAddrStr := fmt.Sprintf("%s:%d", controller.config.DetectedIP, controller.config.DetectedPort)
	LogInfo("DetectOriginRoute prepare loop, detect dest: %s \n", detectAddrStr)

	for i := uint16(0); i < controller.config.RouteDetectTimes; i++ {
		if atomic.LoadInt32(&controller.isDectecting) != 1 {
			LogInfo("DetectOriginRoute loop interrupt. \n")
			return
		}

		LogInfo("DetectOriginRoute start loop: %d/%d\n", i, controller.config.RouteDetectTimes)
		// reset as init
		controller.dectectresultVeried = 0

		srcAddr := &net.UDPAddr{IP: net.IPv4zero, Port: 0}

		detectAddr, err := net.ResolveUDPAddr("udp", detectAddrStr)
		if err != nil {
			LogError("Detected addr err: %s\n", err)
			return
		}

		conn, err := net.DialUDP("udp", srcAddr, detectAddr)
		if err != nil {
			LogError("Detected err: %s\n", err)
			time.Sleep(time.Duration(interval) * time.Second)
			continue
		}
		now := time.Now()
		conn.SetDeadline(now.Add(time.Duration(interval) * time.Second))

		defer conn.Close()

		rand.Read(r)

		for j := IKCP_ALIVE_DETECT_HEAD; j < IKCP_ALIVE_DETECTION; j++ {
			buf[j] = r[0]
		}

		data := make([]byte, 1024)

		for j := uint16(0); j < controller.config.DetectPackageNumbersEachTimes; j++ {
			_, err := conn.Write(buf)
			if err != nil {
				continue
			}
			rev_size, err := conn.Read(data)

			if rev_size == IKCP_ALIVE_DETECT_HEAD {
				var rev_ack uint32
				ikcp_decode32u(data, &rev_ack)
				if rev_ack == DETECTION_MAGIC {
					controller.dectectresultVeried++
					// reset data
					for k := 0; k < IKCP_ALIVE_DETECT_HEAD; k++ {
						data[k] = 0
					}
				}
			}

			if err != nil {
				continue
			}
		}

		detectedRate := controller.dectectresultVeried / float32(controller.config.DetectPackageNumbersEachTimes)

		if detectedRate > controller.config.SatisfyingDetectionRate {
			changed = true
			return
		}
		LogInfo("Detection done, pass rate: %f, satisfying rate: %f\n", detectedRate, controller.config.SatisfyingDetectionRate)
		time.Sleep(time.Duration(interval) * time.Second)
	}

	return
}

func DetectOriginRoute(interval uint64, controller *SessionController) {
	atomic.StoreInt32(&controller.isDectecting, 1)
	if DetectOriginRouteProcess(interval, controller) {
		RunningAsExistMetered()
		atomic.StoreInt32(&controller.isDectecting, 0)
	} else {
		atomic.StoreInt32(&controller.isDectecting, -1)
	}

}

func backupRouteWakeUp(sess *UDPSession, controller *SessionController) (string, error) {
	if !sess.ownConn || sess.conn == nil {
		return "", errors.New("invalid session.")
	}

	raddr := fmt.Sprintf("%s:%d", controller.registerIP, controller.registerPort)
	udpaddr, err := net.ResolveUDPAddr("udp", raddr)
	if err != nil {
		return "", err
	}
	sess.remote = udpaddr
	return raddr, nil
}

func MonitorStart(sess *UDPSession, interval uint64, detectRate float64, controller *SessionController) {
	// Allow SessionTypeNormal in dectection

	sessMonitor := new(UDPSessionMonitor)

	for {

		// After MonitorStart, user may switch the session type by GRPC
		// if globalSessionType == SessionTypeNormal
		// Then just do nothing

		changed := false
		if globalSessionType == SessionTypeExistMetered {
			changed = LoopExistMetered(sessMonitor, detectRate)
		}

		if globalSessionType == SessionTypeOnlyMetered {
			if controller != nil {
				LogInfo("allow detect: %t, newRegistered route: %t, allowSwitchBakcup: %t, detecting status: %d", controller.config.EnableOriginRouteDetect,
					controller.newRegistered, controller.allowSwitchBakcup, atomic.LoadInt32(&controller.isDectecting))
				if controller.newRegistered && controller.allowSwitchBakcup {
					backupAddr, err := backupRouteWakeUp(sess, controller)
					if err != nil {
						LogError("backup route is invalid, error: %s", err)
					} else {
						LogInfo("backup route promoted, new route is: %s. The origin one will be ignored", backupAddr)
						if atomic.LoadInt32(&controller.isDectecting) == 1 {
							atomic.StoreInt32(&controller.isDectecting, -1)
							LogInfo("origin route still detecting. already disabled.")
						}
						RunningAsExistMetered()
					}
					controller.resetRegisterServer()
				} else if controller.config.EnableOriginRouteDetect && atomic.LoadInt32(&controller.isDectecting) == 0 && controller.allowDectecting {
					go DetectOriginRoute(interval, controller)
				} else if changed {
					LogWarn("Controller Server stared, but not enable the route detece and not config the backup server.")
				}
			} else if changed {
				LogWarn("controller Server not started.\n")
			}
		}

		// TODO: If need support diable monitor, then change the sleep to timeout latch
		time.Sleep(time.Duration(interval) * time.Second)
	}
}

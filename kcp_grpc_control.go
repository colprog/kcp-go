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

type ControllerServerConfig struct {
	// server side control config
	controllerIP   string
	controllerPort int
	allowDetect    bool

	// both side
	// dectedIP and dectedPort:
	// - for server side need allowDetect = true
	// - for client side need enableOriginRouteDetect = true
	dectedIP   string
	dectedPort int

	// client side control config
	enableOriginRouteDetect       bool
	satisfyingDetectionRate       float32
	routeDetectTimes              uint16
	detectPackageNumbersEachTimes uint16
}

func (c *ControllerServerConfig) SetControllerIP(ip string) {
	c.controllerIP = ip
}

func (c *ControllerServerConfig) SetControllerPort(port int) {
	c.controllerPort = port
}

func NewDefaultConfig() *ControllerServerConfig {
	c := new(ControllerServerConfig)
	c.controllerIP = "0.0.0.0"
	c.controllerPort = 10720
	c.enableOriginRouteDetect = true
	c.routeDetectTimes = 10
	c.satisfyingDetectionRate = 0.7
	c.detectPackageNumbersEachTimes = 10
	c.allowDetect = true
	c.dectedIP = "0.0.0.0"
	c.dectedPort = 10721
	return c
}

type ControllerServer struct {
	pb.UnimplementedKCPSessionCtlServer

	// 0 means no dectecting
	// 1 means dectecting
	// -1 means dectection fail
	isDectecting        int32
	dectectresultVeried float32

	newRegistered bool
	registerIP    string
	registerPort  int

	config *ControllerServerConfig
}

func (server *ControllerServer) GetSessions(context.Context, *pb.GetSessionsRequest) (*pb.GetSessionsReply, error) {
	reply := pb.GetSessionsReply{}

	if DefaultSnmp == nil {
		DefaultSnmp = newSnmp()
	}

	s := DefaultSnmp.Copy()
	reply.Connections = make([]*pb.ConnectionInfo, 1)
	d := new(pb.ConnectionInfo)

	d.SentBytes = atomic.LoadUint64(&s.BytesSent)
	d.RecvBytes = atomic.LoadUint64(&s.BytesReceived)
	d.DroptBytes = atomic.LoadUint64(&s.BytesDropt)
	d.MaxConn = atomic.LoadUint64(&s.MaxConn)
	d.ActiveOpens = atomic.LoadUint64(&s.ActiveOpens)
	d.PassiveOpens = atomic.LoadUint64(&s.PassiveOpens)
	d.CurrEstab = atomic.LoadUint64(&s.CurrEstab)
	d.InErrs = atomic.LoadUint64(&s.InErrs)
	d.InCsumErrs = atomic.LoadUint64(&s.InCsumErrors)
	d.KcpInErrs = atomic.LoadUint64(&s.KCPInErrors)
	d.InPkts = atomic.LoadUint64(&s.InPkts)
	d.OutPkts = atomic.LoadUint64(&s.OutPkts)
	d.InSegs = atomic.LoadUint64(&s.InSegs)
	d.OutSegs = atomic.LoadUint64(&s.OutSegs)
	d.InBytes = atomic.LoadUint64(&s.InBytes)
	d.OutBytes = atomic.LoadUint64(&s.OutBytes)
	d.RetransSegs = atomic.LoadUint64(&s.RetransSegs)
	d.FastRetransSegs = atomic.LoadUint64(&s.FastRetransSegs)
	d.EarlyRetransSegs = atomic.LoadUint64(&s.EarlyRetransSegs)
	d.LostSegs = atomic.LoadUint64(&s.LostSegs)
	d.RepeatSegs = atomic.LoadUint64(&s.RepeatSegs)
	d.FecParityShards = atomic.LoadUint64(&s.FECParityShards)
	d.FecErrs = atomic.LoadUint64(&s.FECErrs)
	d.FecRecovered = atomic.LoadUint64(&s.FECRecovered)
	d.FecShortShards = atomic.LoadUint64(&s.FECShortShards)

	d.BytesSentFromNoMetered = atomic.LoadUint64(&s.BytesSentFromNoMeteredRaw)
	d.BytesSentFromMetered = atomic.LoadUint64(&s.BytesSentFromMeteredRaw)
	d.BytesRecvFromNoMetered = atomic.LoadUint64(&s.BytesReceivedFromNoMeteredRaw)
	d.BytesRecvFromMetered = atomic.LoadUint64(&s.BytesReceivedFromMeteredRaw)
	d.SegsAcked = atomic.LoadUint64(&s.SegmentNumbersACKed)
	d.SegsPromoteAcked = atomic.LoadUint64(&s.SegmentNumbersPromotedACKed)

	d.Status = pb.SessionStatus(globalSessionType)

	reply.Connections[0] = d

	return &reply, nil
}

func (server *ControllerServer) RegsiterNewSession(_ context.Context, request *pb.RegsiterNewSessionRequest) (*pb.RegsiterNewSessionReply, error) {
	reply := pb.RegsiterNewSessionReply{}
	server.newRegistered = true
	server.registerIP = request.IpAddress
	server.registerPort = int(request.Port)

	return &reply, nil
}

func NewSessionControllerServer(config *ControllerServerConfig, serverSide bool) *ControllerServer {
	s := &ControllerServer{}

	if config == nil {
		config = NewDefaultConfig()
	}
	s.config = config

	rpcAddr := fmt.Sprintf("%s:%d", config.controllerIP, config.controllerPort)
	li, err := net.Listen("tcp", rpcAddr)
	if err != nil {
		LogFatalf("failed to listen: %v", err)
	}

	LogInfo("Controller listening on %s\n", rpcAddr)
	go func() {
		grpcServer := grpc.NewServer()
		pb.RegisterKCPSessionCtlServer(grpcServer, s)
		grpcServer.Serve(li)
	}()

	if serverSide && config.allowDetect {
		go func() {
			detectAddrStr := fmt.Sprintf("%s:%d", config.dectedIP, config.dectedPort)
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

	if !serverSide && config.allowDetect {
		LogWarn("no effect after client side allow detect")
	}

	return s
}

func (server *ControllerServer) resetRegisterServer() {
	server.newRegistered = false
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

func DetectOriginRouteProcess(interval uint64, controller *ControllerServer) (changed bool) {
	changed = false
	buf := make([]byte, IKCP_ALIVE_DETECTION)
	ikcp_encode32u(buf, DETECTION_MAGIC)
	r := make([]byte, 1)

	for i := uint16(0); i < controller.config.routeDetectTimes; i++ {
		LogInfo("DetectOriginRoute start loop: %d/%d\n", i, controller.config.routeDetectTimes)
		// reset as init
		controller.dectectresultVeried = 0

		srcAddr := &net.UDPAddr{IP: net.IPv4zero, Port: 0}

		detectAddrStr := fmt.Sprintf("%s:%d", controller.config.dectedIP, controller.config.dectedPort)
		detectAddr, err := net.ResolveUDPAddr("udp", detectAddrStr)
		if err != nil {
			LogError("dected addr err: %s\n", err)
			return
		}
		conn, err := net.DialUDP("udp", srcAddr, detectAddr)
		if err != nil {
			LogError("dected err: %s\n", err)
			time.Sleep(time.Duration(interval) * time.Second)
			continue
		}

		defer conn.Close()

		rand.Read(r)

		for j := IKCP_ALIVE_DETECT_HEAD; j < IKCP_ALIVE_DETECTION; j++ {
			buf[j] = r[0]
		}

		data := make([]byte, 1024)

		for j := uint16(0); j < controller.config.detectPackageNumbersEachTimes; j++ {
			conn.Write(buf)
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

		if controller.dectectresultVeried/float32(controller.config.detectPackageNumbersEachTimes) > controller.config.satisfyingDetectionRate {
			changed = true
			return
		}

		time.Sleep(time.Duration(interval) * time.Second)
	}

	return
}

func DetectOriginRoute(interval uint64, controller *ControllerServer) {
	atomic.StoreInt32(&controller.isDectecting, 1)
	if DetectOriginRouteProcess(interval, controller) {
		RunningAsExistMetered()
		atomic.StoreInt32(&controller.isDectecting, 0)
	} else {
		atomic.StoreInt32(&controller.isDectecting, -1)
	}

}

func backUpRouteWakeUp(sess *UDPSession, controller *ControllerServer) error {
	if sess.ownConn || sess.conn != nil {
		return errors.New("invalid session.")
	}

	raddr := fmt.Sprintf("%s:%d", controller.registerIP, controller.registerPort)
	udpaddr, err := net.ResolveUDPAddr("udp", raddr)
	if err != nil {
		return err
	}
	sess.remote = udpaddr
	return nil
}

func MonitorStart(sess *UDPSession, interval uint64, detectRate float64, controller *ControllerServer) {
	// Monitor disabled
	if globalSessionType == SessionTypeNormal {
		return
	}

	sessMonitor := new(UDPSessionMonitor)

	for {
		changed := false
		if globalSessionType == SessionTypeExistMetered {
			changed = LoopExistMetered(sessMonitor, detectRate)
		}

		if globalSessionType == SessionTypeOnlyMetered {
			if controller != nil {
				if controller.config.enableOriginRouteDetect && atomic.LoadInt32(&controller.isDectecting) == 0 {
					go DetectOriginRoute(interval, controller)
				} else if controller.newRegistered {
					err := backUpRouteWakeUp(sess, controller)
					if err != nil {
						LogError("backup route is invalid, error: %s", err)
					} else {
						RunningAsExistMetered()
					}
					controller.resetRegisterServer()
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

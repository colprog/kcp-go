package kcp

import (
	"context"
	"fmt"
	"log"
	"net"
	"sync/atomic"

	pb "github.com/xtaci/kcp-go/v5/grpc_control"
	"google.golang.org/grpc"
)

type ControllerServerConfig struct {
	enableOriginRouteDetect bool
	controllerIP            string
	controllerPort          int
}

func (config *ControllerServerConfig) NewDefaultConfig() *ControllerServerConfig {
	c := new(ControllerServerConfig)
	c.controllerIP = "0.0.0.0"
	c.controllerPort = 10720
	c.enableOriginRouteDetect = true
	return c
}

type ControllerServer struct {
	pb.UnimplementedKCPSessionCtlServer

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

func NewSessionControllerServer(config *ControllerServerConfig) *ControllerServer {
	s := &ControllerServer{}

	if config == nil {
		config = config.NewDefaultConfig()
	}

	rpcAddr := fmt.Sprintf("%s:%d", config.controllerIP, config.controllerPort)
	li, err := net.Listen("tcp", rpcAddr)
	if err != nil {
		log.Fatalf("failed to listen: %v", err)
	}
	fmt.Printf("Controller listening on %s\n", rpcAddr)
	log.Printf("Controller listening on %s\n", rpcAddr)
	go func() {
		grpcServer := grpc.NewServer()
		pb.RegisterKCPSessionCtlServer(grpcServer, s)
		grpcServer.Serve(li)
	}()

	return s
}

func (server *ControllerServer) resetRegisterServer() {
	server.newRegistered = false
}

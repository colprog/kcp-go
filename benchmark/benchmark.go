package main

import (
	"errors"
	"fmt"
	"log"
	"math/rand"
	"os"
	"time"

	"github.com/urfave/cli/v2"
	"github.com/xtaci/kcp-go/v5"
)

type (
	BenchCliOps struct {
		MinBufferSize       int     `json:"MinBufferSize"`
		MaxBufferSize       int     `json:"MaxBufferSize"`
		RemoteAddress       string  `json:"RemoteAddress"`
		DataRandomMode      int     `json:"DataRandomMode"`
		RemoteSlowAddr      int     `json:"RemoteSlowAddr"`
		RemoteMeterAddr     string  `json:"RemoteMeterAddr"`
		StatisticalInterval int     `json:"StatisticalInterval"`
		MonitorInterval     int     `json:"MonitorInterval"`
		DetectRate          float64 `json:"DetectRate"`
		EnableVerifyMode    bool    `json:"EnableVerifyMode"`
	}

	BenchSerOps struct {
		ListenSlowPort      int     `json:"ListenSlowPort"`
		DropRate            float64 `json:"DropRate"`
		MaxBufferSize       int     `json:"MaxBufferSize"`
		MeteredAddress      string  `json:"MeteredAddress"`
		StatisticalInterval int     `json:"StatisticalInterval"`
		EnableVerifyMode    bool    `json:"EnableVerifyMode"`
	}

	BenchOps struct {
		MeteredAddress string
		serverOps      BenchSerOps
		cliOps         BenchCliOps
	}
)

type RunMode int32

const (
	InvalidMode RunMode = 0
	ServerMode  RunMode = 1
	ClientMode  RunMode = 2
	DirectRun   RunMode = 3
)

const (
	DefaultControlPort      int = 10721
	DefaultVerifyDataLength int = 1000
)

func main() {

	err := kcp.LoggerDefault()
	if err != nil {
		log.Fatal(err)
	}

	runMode := InvalidMode

	benchOps := new(BenchOps)
	benchCliOps := new(BenchCliOps)
	benchSerOps := new(BenchSerOps)

	app := &cli.App{
		Name:  "KCP-GO benchmark",
		Usage: "Benchmark of KCP-GO",
		Commands: []*cli.Command{
			{
				Name:  "bench_server",
				Usage: "Running as bench server",
				Flags: []cli.Flag{
					&cli.IntFlag{
						Name:  "listen_slow_port",
						Usage: "Server size listen to slow port.",
						Value: 10086,
					},
					&cli.Float64Flag{
						Name:  "drop-rate",
						Usage: "Rate of drop ack package in slow port.",
						Value: 0.3,
						Action: func(ctx *cli.Context, v float64) error {
							if v < 0 || v > 1 {
								return fmt.Errorf("flag DropRate value %f out of range[0-1], 1 means drop all datas", v)
							}
							return nil
						},
					},
					&cli.IntFlag{
						Name:  "max-buffer-size",
						Usage: "Maximum Buffer size",
						Value: 128 * 1024,
						Action: func(ctx *cli.Context, v int) error {
							if v > 128*1024 || v <= 0 {
								return fmt.Errorf("flag max-buffer-size value %d out of range[1-128k]", v)
							}

							return nil
						},
					},
					&cli.StringFlag{
						Name:  "metered-address",
						Usage: "Meter ip address.",
						Action: func(ctx *cli.Context, v string) error {
							return nil
						},
					},
					&cli.IntFlag{
						Name:  "statistical-interval",
						Usage: "The interval to print statistical",
						Value: 30,
					},
					&cli.BoolFlag{
						Name:  "enable-verify-mode",
						Usage: "after enabled verify, some of args will been ignored",
						Value: false,
					},
				},
				Action: func(c *cli.Context) error {
					runMode = ServerMode
					benchSerOps.ListenSlowPort = c.Int("listen_slow_port")
					benchSerOps.DropRate = c.Float64("drop-rate")
					benchSerOps.MaxBufferSize = c.Int("max-buffer-size")
					benchSerOps.MeteredAddress = c.String("metered-address")
					benchSerOps.StatisticalInterval = c.Int("statistical-interval")
					benchSerOps.EnableVerifyMode = c.Bool("enable-verify-mode")
					if len(benchSerOps.MeteredAddress) == 0 {
						return errors.New("invalid MeteredAddress")
					}
					return nil
				},
			},
			{
				Name:  "bench_client",
				Usage: "Running as bench server",
				Flags: []cli.Flag{
					&cli.IntFlag{
						Name:  "min-buffer-size",
						Usage: "Minimum Buffer size",
						Value: 1,
						Action: func(ctx *cli.Context, v int) error {
							if v > 4*1024 || v <= 0 {
								return fmt.Errorf("flag min-buffer-size value %d out of range[1-4k]", v)
							}
							return nil
						},
					},
					&cli.IntFlag{
						Name:  "max-buffer-size",
						Usage: "Maximum Buffer size",
						Value: 128 * 1024,
						Action: func(ctx *cli.Context, v int) error {
							if v > 128*1024 || v <= 0 {
								return fmt.Errorf("flag max-buffer-size value %d out of range[1-128k]", v)
							}

							return nil
						},
					},
					&cli.StringFlag{
						Name:  "remote-address",
						Usage: "Remote ip address.",
						Value: "0.0.0.0",
						Action: func(ctx *cli.Context, v string) error {
							// todo: check
							return nil
						},
					},
					&cli.StringFlag{
						Name:  "remote-metered-address",
						Usage: "Remote metered ip address.",
						Action: func(ctx *cli.Context, v string) error {
							// todo: check
							return nil
						},
					},
					&cli.IntFlag{
						Name:  "data-random-mode",
						Usage: "0 - no random, 1 - simple random(default), 2 - full random",
						Value: 1,
						Action: func(ctx *cli.Context, v int) error {
							if v < 0 || v > 2 {
								return fmt.Errorf("flag data-random-mode value %d out of range[0, 1, 2]", v)
							}
							return nil
						},
					},
					&cli.IntFlag{
						Name:  "remote-slow-port",
						Usage: "Remote slow port address.",
						Value: 10086,
					},
					&cli.IntFlag{
						Name:  "statistical-interval",
						Usage: "The interval to print statistical",
						Value: 30,
					},
					&cli.IntFlag{
						Name:  "monitor-interval",
						Usage: "Enable the monitor in client side and set the interval. 0 means not enable monitor.",
						Value: 0,
						Action: func(ctx *cli.Context, v int) error {
							if v < 0 || v > 60 {
								return fmt.Errorf("flag monitor-interval value %d out of range[0, 60]", v)
							}
							return nil
						},
					},
					&cli.Float64Flag{
						Name:  "detect-rate",
						Usage: "After enabled monitor, the rate will effect mode change",
						Value: 0.9,
					},
					&cli.BoolFlag{
						Name:  "enable-verify-mode",
						Usage: "after enabled verify, some of args will been ignored",
						Value: false,
					},
				},
				Action: func(c *cli.Context) error {
					runMode = ClientMode
					benchCliOps.MaxBufferSize = c.Int("max-buffer-size")
					benchCliOps.MinBufferSize = c.Int("min-buffer-size")
					benchCliOps.RemoteAddress = c.String("remote-address")
					benchCliOps.DataRandomMode = c.Int("data-random-mode")
					benchCliOps.RemoteSlowAddr = c.Int("remote-slow-port")
					benchCliOps.RemoteMeterAddr = c.String("remote-metered-address")
					benchCliOps.StatisticalInterval = c.Int("statistical-interval")
					benchCliOps.MonitorInterval = c.Int("monitor-interval")
					benchCliOps.DetectRate = c.Float64("detect-rate")
					benchCliOps.EnableVerifyMode = c.Bool("enable-verify-mode")
					if len(benchCliOps.RemoteMeterAddr) == 0 {
						return errors.New("invalid RemoteMeterAddr")
					}
					return nil
				},
			},
		},
		Flags: []cli.Flag{
			&cli.StringFlag{
				Name:  "metered-address",
				Usage: "Meter ip address.",
				Action: func(ctx *cli.Context, v string) error {
					return nil
				},
			},
		},
		Action: func(c *cli.Context) error {
			runMode = DirectRun
			benchOps.MeteredAddress = c.String("metered-address")
			benchOps.serverOps.DropRate = 0.3
			benchOps.serverOps.ListenSlowPort = 10086
			benchOps.serverOps.MaxBufferSize = 128 * 1024
			benchOps.serverOps.MeteredAddress = benchOps.MeteredAddress
			benchOps.serverOps.StatisticalInterval = 30

			benchOps.cliOps.DataRandomMode = 1
			benchOps.cliOps.MaxBufferSize = 64 * 1024
			benchOps.cliOps.MinBufferSize = 1 * 1024
			benchOps.cliOps.RemoteAddress = "0.0.0.0"
			benchOps.cliOps.RemoteMeterAddr = benchOps.MeteredAddress
			benchOps.cliOps.RemoteSlowAddr = benchOps.serverOps.ListenSlowPort
			return nil
		},
	}

	if err := app.Run(os.Args); err != nil {
		kcp.LogTestFatalf("Fail to start application, error: %s", err)
	}

	switch runMode {
	case ServerMode:
		err := startServer(benchSerOps)
		if err != nil {
			kcp.LogTestFatalf("server side got error: %s", err)
		}
		return
	case ClientMode:
		err := start(benchCliOps)
		if err != nil {
			kcp.LogTestFatalf("client side got error: %s", err)
		}
	case DirectRun:
		// TODO
		kcp.LogTestFatalf("No support yet!")
	default:
		return
	}
}

var staticByteData []byte

func genRandomFromRange(min int, max int) int {
	rand.Seed(time.Now().UnixNano())
	return rand.Intn(max-min) + min
}

func genFixData(length int) []byte {
	if staticByteData == nil {
		staticByteData = make([]byte, length)
		for i := 0; i < length; i++ {
			staticByteData[i] = byte(i)
		}
	}
	return staticByteData
}

func verifyFixData(buffer []byte, length int, expectLen int) bool {
	if length != expectLen {
		return false
	}

	for i := 0; i < length; i++ {
		if buffer[i] != byte(i) {
			return false
		}
	}
	return true
}

func getRandomData(verifyMode bool, randMode int, minSize int, maxSize int) ([]byte, int, error) {
	dataLen := maxSize

	if verifyMode {
		return genFixData(DefaultVerifyDataLength), DefaultVerifyDataLength, nil
	}

	if randMode != 0 {
		dataLen = genRandomFromRange(minSize, maxSize)
	}

	if staticByteData == nil {
		staticByteData = make([]byte, maxSize)
		for i := 0; i < maxSize; i++ {
			staticByteData[i] = byte(i)
		}
	}

	switch randMode {
	case 0:
		break
	case 1:
		l := genRandomFromRange(0, dataLen)
		r := genRandomFromRange(0, dataLen)
		if l != r {
			lb := staticByteData[l]
			staticByteData[l] = staticByteData[r]
			staticByteData[r] = lb
		}
	case 2:
		rand.Shuffle(maxSize, func(i, j int) { staticByteData[i], staticByteData[j] = staticByteData[j], staticByteData[i] })
	default:
		return nil, 0, errors.New("invalid data-random-mode")
	}
	return staticByteData[:dataLen], dataLen, nil
}

func startServerSnmpTricker(args *BenchSerOps) *time.Ticker {
	snmpTicker := time.NewTicker(time.Duration(args.StatisticalInterval) * time.Second)

	go func(t *time.Ticker) {
		for {
			<-t.C
			kcp.LogTest(kcp.DefaultSnmp.ToString())
		}
	}(snmpTicker)

	return snmpTicker
}

func startClientSnmpTricker(args *BenchCliOps) *time.Ticker {
	snmpTicker := time.NewTicker(time.Duration(args.StatisticalInterval) * time.Second)

	go func(t *time.Ticker) {
		for {
			<-t.C
			kcp.LogTest(kcp.DefaultSnmp.ToString())
		}
	}(snmpTicker)

	return snmpTicker
}

func startServer(args *BenchSerOps) error {

	kcp.LogTest("Benchmark server side started. options: %+v \n", args)
	listenSlowAddrStr := fmt.Sprintf("%s:%d", "0.0.0.0", args.ListenSlowPort)

	kcp.LogTest("Server slow path listen to: %s", listenSlowAddrStr)
	slowListener, err := kcp.ListenWithDrop(listenSlowAddrStr, args.DropRate)
	if err != nil {
		return err
	}

	slowListener.NewControllerServer(nil)

	snmpTicker := startServerSnmpTricker(args)
	defer snmpTicker.Stop()

	for {
		s, err := slowListener.AcceptKCP()
		s.SetMeteredAddr(args.MeteredAddress, uint16(args.ListenSlowPort), true)
		kcp.LogTest("Server slow path got session on")
		if err != nil {
			return err
		}
		go handleMessage(s, args.MaxBufferSize, args.EnableVerifyMode)
	}

	return nil
}

func handleMessage(conn *kcp.UDPSession, maxSize int, verifyMode bool) {
	buf := make([]byte, maxSize)
	for {
		verified := -1
		n, err := conn.Read(buf)

		if verifyMode {
			if verifyFixData(buf, n, DefaultVerifyDataLength) {
				verified = 1
			} else {
				verified = 0
			}
		}

		switch verified {
		case -1:
			{
				kcp.LogTest("Server side revc: %d", n)
				break
			}
		case 0:
			{
				kcp.LogWarn("Server side revc: %d, verified failed")
				break
			}
		case 1:
			{
				kcp.LogTest("Server side revc: %d, verified.", n)
				break
			}
		default:
			{
				log.Panicln("logic error")
			}
		}

		if err != nil {
			kcp.LogTest("Server side fail to revc: %s", err)
			return
		}
	}
}

func start(args *BenchCliOps) error {
	kcp.LogTest("Benchmark client side started. options: %+v \n", args)

	snmpTicker := startClientSnmpTricker(args)
	defer snmpTicker.Stop()

	remoteSlowAddrStr := fmt.Sprintf("%s:%d", args.RemoteAddress, args.RemoteSlowAddr)
	kcp.LogTest("Connect to: %s", remoteSlowAddrStr)
	if sess, err := kcp.Dial(remoteSlowAddrStr); err == nil {
		sess.SetMeteredAddr(args.RemoteMeterAddr, uint16(args.RemoteSlowAddr), true)

		cliConfig := kcp.NewDefaultConfig()
		cliConfig.SetControllerPort(DefaultControlPort)
		sess.SetSessionController(kcp.NewSessionController(cliConfig, false))

		if args.MonitorInterval != 0 {
			sess.EnableMonitor(uint64(args.MonitorInterval), args.DetectRate)
		}

		for {
			data, dataLen, err := getRandomData(args.EnableVerifyMode, args.DataRandomMode, args.MinBufferSize, args.MaxBufferSize)
			if err != nil {
				return err
			}

			if _, err := sess.Write([]byte(data)); err == nil {
				kcp.LogTest("sent: %d", dataLen)
			} else {
				return err
			}
			// time.Sleep(time.Second)
		}
	} else {
		return err
	}

	return nil
}

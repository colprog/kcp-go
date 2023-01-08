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
		min_buffer_size        int     `json:"min_buffer_size"`
		max_buffer_size        int     `json:"max_buffer_size"`
		remote_address         string  `json:"remote_address"`
		data_random_mode       int     `json:"data_random_mode"`
		remote_slow_port       int     `json:"remote_slow_port"`
		remote_metered_address string  `json:"remote_metered_address"`
		statistical_interval   int     `json:"statistical_interval"`
		monitor_interval       int     `json:"monitor_interval"`
		detect_rate            float64 `json:"detect_rate"`
	}

	BenchSerOps struct {
		listen_slow_port     int     `json:"listen_slow_port"`
		drop_rate            float64 `json:"drop_rate"`
		max_buffer_size      int     `json:"max_buffer_size"`
		metered_address      string  `json:"metered_address"`
		statistical_interval int     `json:"statistical_interval"`
	}

	BenchOps struct {
		metered_address string
		serverOps       BenchSerOps
		cliOps          BenchCliOps
	}
)

type RunMode int32

const (
	InvalidMode RunMode = 0
	ServerMode  RunMode = 1
	ClientMode  RunMode = 2
	DirectRun   RunMode = 3
)

func main() {
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
						Name:  "drop_rate",
						Usage: "Rate of drop ack package in slow port.",
						Value: 0.3,
						Action: func(ctx *cli.Context, v float64) error {
							if v < 0 || v > 1 {
								return fmt.Errorf("flag drop_rate value %f out of range[0-1], 1 means drop all datas", v)
							}
							return nil
						},
					},
					&cli.IntFlag{
						Name:  "max_buffer_size",
						Usage: "Maximum Buffer size",
						Value: 128 * 1024,
						Action: func(ctx *cli.Context, v int) error {
							if v > 128*1024 || v <= 0 {
								return fmt.Errorf("flag max_buffer_size value %d out of range[1-128k]", v)
							}

							return nil
						},
					},
					&cli.StringFlag{
						Name:  "metered_address",
						Usage: "Meter ip address.",
						Action: func(ctx *cli.Context, v string) error {
							return nil
						},
					},
					&cli.IntFlag{
						Name:  "statistical_interval",
						Usage: "The interval to print statistical",
						Value: 30,
					},
				},
				Action: func(c *cli.Context) error {
					runMode = ServerMode
					benchSerOps.listen_slow_port = c.Int("listen_slow_port")
					benchSerOps.drop_rate = c.Float64("drop_rate")
					benchSerOps.max_buffer_size = c.Int("max_buffer_size")
					benchSerOps.metered_address = c.String("metered_address")
					benchSerOps.statistical_interval = c.Int("statistical_interval")
					if len(benchSerOps.metered_address) == 0 {
						return errors.New("invalid metered_address")
					}
					return nil
				},
			},
			{
				Name:  "bench_client",
				Usage: "Running as bench server",
				Flags: []cli.Flag{
					&cli.IntFlag{
						Name:  "min_buffer_size",
						Usage: "Minimum Buffer size",
						Value: 1,
						Action: func(ctx *cli.Context, v int) error {
							if v > 4*1024 || v <= 0 {
								return fmt.Errorf("flag min_buffer_size value %d out of range[1-4k]", v)
							}
							return nil
						},
					},
					&cli.IntFlag{
						Name:  "max_buffer_size",
						Usage: "Maximum Buffer size",
						Value: 128 * 1024,
						Action: func(ctx *cli.Context, v int) error {
							if v > 128*1024 || v <= 0 {
								return fmt.Errorf("flag max_buffer_size value %d out of range[1-128k]", v)
							}

							return nil
						},
					},
					&cli.StringFlag{
						Name:  "remote_address",
						Usage: "Remote ip address.",
						Value: "0.0.0.0",
						Action: func(ctx *cli.Context, v string) error {
							// todo: check
							return nil
						},
					},
					&cli.StringFlag{
						Name:  "remote_metered_address",
						Usage: "Remote metered ip address.",
						Action: func(ctx *cli.Context, v string) error {
							// todo: check
							return nil
						},
					},
					&cli.IntFlag{
						Name:  "data_random_mode",
						Usage: "0 - no random, 1 - simple random(default), 2 - full random",
						Value: 1,
						Action: func(ctx *cli.Context, v int) error {
							if v < 0 || v > 2 {
								return fmt.Errorf("flag data_random_mode value %d out of range[0, 1, 2]", v)
							}
							return nil
						},
					},
					&cli.IntFlag{
						Name:  "remote_slow_port",
						Usage: "Remote slow port address.",
						Value: 10086,
					},
					&cli.IntFlag{
						Name:  "statistical_interval",
						Usage: "The interval to print statistical",
						Value: 30,
					},
					&cli.IntFlag{
						Name:  "monitor_interval",
						Usage: "Enable the monitor in client side and set the interval. 0 means not enable monitor.",
						Value: 0,
						Action: func(ctx *cli.Context, v int) error {
							if v < 0 || v > 60 {
								return fmt.Errorf("flag monitor_interval value %d out of range[0, 60]", v)
							}
							return nil
						},
					},
					&cli.Float64Flag{
						Name:  "detect_rate",
						Usage: "After enabled monitor, the rate will effect mode change",
						Value: 0.9,
					},
				},
				Action: func(c *cli.Context) error {
					runMode = ClientMode
					benchCliOps.max_buffer_size = c.Int("max_buffer_size")
					benchCliOps.min_buffer_size = c.Int("min_buffer_size")
					benchCliOps.remote_address = c.String("remote_address")
					benchCliOps.data_random_mode = c.Int("data_random_mode")
					benchCliOps.remote_slow_port = c.Int("remote_slow_port")
					benchCliOps.remote_metered_address = c.String("remote_metered_address")
					benchCliOps.statistical_interval = c.Int("statistical_interval")
					benchCliOps.monitor_interval = c.Int("monitor_interval")
					benchCliOps.detect_rate = c.Float64("detect_rate")
					if len(benchCliOps.remote_metered_address) == 0 {
						return errors.New("invalid remote_metered_address")
					}
					return nil
				},
			},
		},
		Flags: []cli.Flag{
			&cli.StringFlag{
				Name:  "metered_address",
				Usage: "Meter ip address.",
				Action: func(ctx *cli.Context, v string) error {
					return nil
				},
			},
		},
		Action: func(c *cli.Context) error {
			runMode = DirectRun
			benchOps.metered_address = c.String("metered_address")
			benchOps.serverOps.drop_rate = 0.3
			benchOps.serverOps.listen_slow_port = 10086
			benchOps.serverOps.max_buffer_size = 128 * 1024
			benchOps.serverOps.metered_address = benchOps.metered_address
			benchOps.serverOps.statistical_interval = 30

			benchOps.cliOps.data_random_mode = 1
			benchOps.cliOps.max_buffer_size = 64 * 1024
			benchOps.cliOps.min_buffer_size = 1 * 1024
			benchOps.cliOps.remote_address = "0.0.0.0"
			benchOps.cliOps.remote_metered_address = benchOps.metered_address
			benchOps.cliOps.remote_slow_port = benchOps.serverOps.listen_slow_port
			return nil
		},
	}

	if err := app.Run(os.Args); err != nil {
		log.Fatal(err)
	}

	switch runMode {
	case ServerMode:
		err := startServer(benchSerOps)
		if err != nil {
			log.Println("server side got error:", err)
		}
		return
	case ClientMode:
		err := start(benchCliOps)
		if err != nil {
			log.Fatal(err)
		}
	case DirectRun:
		// TBD
		log.Fatal("No support yet!")
	default:
		return
	}
}

var staticByteData []byte

func genRandomFromRange(min int, max int) int {
	rand.Seed(time.Now().UnixNano())
	return rand.Intn(max-min) + min
}

func getRandomData(randMode int, minSize int, maxSize int) ([]byte, int, error) {
	dataLen := maxSize
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
		return nil, 0, errors.New("invalid data_random_mode")
	}
	return staticByteData[:dataLen], dataLen, nil
}

func startServerSnmpTricker(args *BenchSerOps) *time.Ticker {
	snmpTicker := time.NewTicker(time.Duration(args.statistical_interval) * time.Second)

	go func(t *time.Ticker) {
		for {
			<-t.C
			log.Println(kcp.DefaultSnmp.ToString())
		}
	}(snmpTicker)

	return snmpTicker
}

func startClientSnmpTricker(args *BenchCliOps) *time.Ticker {
	snmpTicker := time.NewTicker(time.Duration(args.statistical_interval) * time.Second)

	go func(t *time.Ticker) {
		for {
			<-t.C
			log.Println(kcp.DefaultSnmp.ToString())
		}
	}(snmpTicker)

	return snmpTicker
}

func startServer(args *BenchSerOps) error {

	log.Printf("Benchmark server side started. options: %+v \n", args)
	listenSlowAddrStr := fmt.Sprintf("%s:%d", "0.0.0.0", args.listen_slow_port)

	log.Println("Server slow path listen to:", listenSlowAddrStr)
	slowListener, err := kcp.ListenWithDrop(listenSlowAddrStr, args.drop_rate)
	if err != nil {
		return err
	}

	slowListener.NewControllerConfig(nil)

	snmpTicker := startServerSnmpTricker(args)
	defer snmpTicker.Stop()

	for {
		s, err := slowListener.AcceptKCP()
		s.SetMeteredAddr(args.metered_address, uint16(args.listen_slow_port), true)
		log.Println("Server slow path got session on")
		if err != nil {
			return err
		}
		go handleMessage(s, args.max_buffer_size)
	}

	return nil
}

func handleMessage(conn *kcp.UDPSession, maxSize int) {
	buf := make([]byte, maxSize)
	for {
		n, err := conn.Read(buf)
		log.Println("Server side revc:", n)
		if err != nil {
			log.Println(err)
			return
		}
	}
}

func start(args *BenchCliOps) error {
	log.Printf("Benchmark client side started. options: %+v \n", args)

	snmpTicker := startClientSnmpTricker(args)
	defer snmpTicker.Stop()

	remoteSlowAddrStr := fmt.Sprintf("%s:%d", args.remote_address, args.remote_slow_port)
	log.Println("Connect to:", remoteSlowAddrStr)
	if sess, err := kcp.Dial(remoteSlowAddrStr); err == nil {
		sess.SetMeteredAddr(args.remote_metered_address, uint16(args.remote_slow_port), true)

		if args.monitor_interval != 0 {
			err := sess.EnableMonitor(uint64(args.monitor_interval), args.detect_rate)
			if err != nil {
				return err
			}
		}

		for {
			data, dataLen, err := getRandomData(args.data_random_mode, args.min_buffer_size, args.max_buffer_size)
			if err != nil {
				return err
			}

			if _, err := sess.Write([]byte(data)); err == nil {
				log.Println("sent:", dataLen)
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

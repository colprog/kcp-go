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
	BenchmarkOps struct {
		min_buffer_size  int     `json:"min_buffer_size"`
		max_buffer_size  int     `json:"max_buffer_size"`
		remote_address   string  `json:"remote_address"`
		slow_rate        float64 `json:"slow_rate"`
		data_random_mode int     `json:"data_random_mode"`
		remote_slow_port int     `json:"remote_slow_port"`
	}

	BnechServerOps struct {
		listen_slow_port int     `json:"listen_slow_port"`
		drop_rate        float64 `json:"drop_rate"`
		max_buffer_size  int     `json:"max_buffer_size"`
	}
)

func setupLoopUpIps() error {
	// TBD
	// sudo ifconfig lo0 alias 127.0.0.2
	return nil
}

func main() {
	serverMode := false
	benchOps := new(BenchmarkOps)
	benchSerOps := new(BnechServerOps)
	justHelp := true
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
				},
				Action: func(c *cli.Context) error {
					serverMode = true
					justHelp = false
					benchSerOps.listen_slow_port = c.Int("listen_slow_port")
					benchSerOps.drop_rate = c.Float64("drop_rate")
					benchSerOps.max_buffer_size = c.Int("max_buffer_size")
					return setupLoopUpIps()
				},
			},
		},
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
				Value: "127.0.0.1",
				Action: func(ctx *cli.Context, v string) error {
					// todo: check
					return nil
				},
			},
			&cli.Float64Flag{
				// todo: should add it into server size?
				Name:  "slow_rate",
				Usage: "Rate of slowdown in slow port.",
				Value: 0.3,
				Action: func(ctx *cli.Context, v float64) error {
					if v < 0 || v >= 1 {
						return fmt.Errorf("flag slow_rate value %f out of range[0-1)", v)
					}
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
		},
		Action: func(c *cli.Context) error {
			justHelp = false
			benchOps.max_buffer_size = c.Int("max_buffer_size")
			benchOps.min_buffer_size = c.Int("min_buffer_size")
			benchOps.remote_address = c.String("remote_address")
			benchOps.slow_rate = c.Float64("slow_rate")
			benchOps.data_random_mode = c.Int("data_random_mode")
			benchOps.remote_slow_port = c.Int("remote_slow_port")
			return setupLoopUpIps()
		},
	}

	if err := app.Run(os.Args); err != nil {
		log.Fatal(err)
	}

	if justHelp {
		return
	}

	if serverMode {
		err := startServer(benchSerOps)
		if err != nil {
			log.Println("server side got error:", err)
		}
		return
	}

	err := start(benchOps)
	if err != nil {
		log.Fatal(err)
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

func startServer(args *BnechServerOps) error {
	log.Printf("Benchmark server side started. options: %+v \n", args)
	listenSlowAddrStr := fmt.Sprintf("%s:%d", "127.0.0.1", args.listen_slow_port)

	log.Println("Server slow path listen to:", listenSlowAddrStr)
	slowListener, err := kcp.ListenWithDrop(listenSlowAddrStr, args.drop_rate)
	if err != nil {
		return err
	}

	for {
		s, err := slowListener.AcceptKCP()
		s.SetMeteredAddr("127.0.0.2", 10086, true)
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

func start(args *BenchmarkOps) error {
	log.Printf("Benchmark client side started. options: %+v \n", args)

	remoteSlowAddrStr := fmt.Sprintf("%s:%d", args.remote_address, args.remote_slow_port)
	log.Println("Connect to:", remoteSlowAddrStr)
	if sess, err := kcp.Dial(remoteSlowAddrStr); err == nil {
		sess.SetMeteredAddr("127.0.0.2", uint16(args.remote_slow_port), true)

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

			time.Sleep(time.Second)
		}
	} else {
		return err
	}

	return nil
}

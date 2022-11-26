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
		min_buffer_size    int
		max_buffer_size    int
		remote_address     string
		slow_rate          float64
		data_random_mode   int
		remote_slow_port   int
		remote_stable_port int
	}

	BnechServerOps struct {
		listen_slow_port   int
		listen_stable_port int
		drop_rate          float64
	}
)

func main() {
	serverMode := false
	benchOps := new(BenchmarkOps)
	benchSerOps := new(BnechServerOps)
	app := &cli.App{
		Commands: []*cli.Command{
			{
				Name:  "bench_server",
				Usage: "Running as bench server",
				Flags: []cli.Flag{
					&cli.IntFlag{
						Name:  "listen_slow_port",
						Usage: "Server size listen to slow port.",
						Value: 10086,
						Action: func(ctx *cli.Context, v int) error {
							benchSerOps.listen_slow_port = v
							return nil
						},
					},
					&cli.IntFlag{
						Name:  "listen_stable_port",
						Usage: "Server size listen to stable port.",
						Value: 10010,
						Action: func(ctx *cli.Context, v int) error {
							benchSerOps.listen_stable_port = v
							return nil
						},
					},
					&cli.Float64Flag{
						Name:  "drop_rate",
						Usage: "Rate of drop ack package in slow port.",
						Value: 0.3,
						Action: func(ctx *cli.Context, v float64) error {
							if v < 0 || v >= 1 {
								return fmt.Errorf("flag drop_rate value %f out of range[0-1)", v)
							}
							benchOps.slow_rate = v
							return nil
						},
					},
				},
				Action: func(ctx *cli.Context) error {
					serverMode = true
					return nil
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
					benchOps.min_buffer_size = v
					return nil
				},
			},
			&cli.IntFlag{
				Name:  "max_buffer_size",
				Usage: "Maximum Buffer size",
				Value: 1,
				Action: func(ctx *cli.Context, v int) error {
					if v > 128*1024 || v <= 0 {
						return fmt.Errorf("flag max_buffer_size value %d out of range[1-128k]", v)
					}
					benchOps.max_buffer_size = v
					return nil
				},
			},
			&cli.StringFlag{
				Name:  "remote_address",
				Usage: "Remote ip address.",
				Action: func(ctx *cli.Context, v string) error {
					// todo: check
					benchOps.remote_address = v
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
					benchOps.slow_rate = v
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
					benchOps.data_random_mode = v
					return nil
				},
			},
			&cli.IntFlag{
				Name:  "remote_slow_port",
				Usage: "Remote slow port address.",
				Value: 10086,
				Action: func(ctx *cli.Context, v int) error {
					benchOps.remote_slow_port = v
					return nil
				},
			},
			&cli.IntFlag{
				Name:  "remote_stable_port",
				Usage: "Remote stable port address.",
				Value: 10010,
				Action: func(ctx *cli.Context, v int) error {
					benchOps.remote_stable_port = v
					return nil
				},
			},
		},
	}

	app.Name = "KCP-GO benchmark"

	if err := app.Run(os.Args); err != nil {
		log.Fatal(err)
	}

	if serverMode {
		startServer(benchSerOps)
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
	dataLen := genRandomFromRange(minSize, maxSize)

	if (randMode == 0 || randMode == 1) && staticByteData == nil {
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

func startStableServer(args *BnechServerOps) error {
	listenStableAddrStr := fmt.Sprintf("%s:%d", "127.0.0.1", args.listen_stable_port)

	stableListener, err := kcp.Listen(listenStableAddrStr)
	if err != nil {
		return err
	}

	for {
		_, err := stableListener.AcceptKCP()
		if err != nil {
			return err
		}
		// todo
	}

	return nil
}

func startServer(args *BnechServerOps) error {

	listenSlowAddrStr := fmt.Sprintf("%s:%d", "127.0.0.1", args.listen_slow_port)

	slowListener, err := kcp.ListenWithDrop(listenSlowAddrStr, args.drop_rate)
	if err != nil {
		return err
	}

	go startStableServer(args)
	for {
		_, err := slowListener.AcceptKCP()
		if err != nil {
			return err
		}
		// todo: send something back?
	}

	return nil
}

func start(args *BenchmarkOps) error {
	// data, dataLen, err := getRandomData(args.data_random_mode, args.min_buffer_size, args.max_buffer_size)
	// if err != nil {
	// 	return err
	// }

	// remoteSlowAddrStr := fmt.Sprintf("%s:%s", args.remote_address, args.remote_slow_port)
	// remoteStableAddrStr := fmt.Sprintf("%s:%s", args.remote_address, args.remote_stable_port)
	// if sess, err := kcp.Dial(remoteStableAddrStr); err == nil {

	// }

	return nil
}

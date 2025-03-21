package main

import (
	"crypto/sha1"
	"io"
	"log"
	"time"

	"github.com/xtaci/kcp-go/v5"
	"golang.org/x/crypto/pbkdf2"
)

func main() {
	err := kcp.LoggerDefault()
	if err != nil {
		log.Fatal(err)
	}

	key := pbkdf2.Key([]byte("demo pass"), []byte("demo salt"), 1024, 32, sha1.New)
	block, _ := kcp.NewAESBlockCrypt(key)
	if listener, err := kcp.ListenWithDetailOptions("127.0.0.1:12345", block, 10, 3, nil, kcp.DebugLevelLog); err == nil {
		// spin-up the client
		go client()
		for {
			s, err := listener.AcceptKCP()
			if err != nil {
				kcp.LogFatalf("AcceptKCP failed, error: %s", err)
			}
			go handleEcho(s)
		}
	} else {
		kcp.LogFatalf("ListenWithDetailOptions failed, error: %s", err)
	}
}

// handleEcho send back everything it received
func handleEcho(conn *kcp.UDPSession) {
	buf := make([]byte, 4096)
	for {
		n, err := conn.Read(buf)
		if err != nil {
			kcp.LogTest("Read buff got error: %s", err)
			return
		}

		_, err = conn.Write(buf[:n])
		if err != nil {
			kcp.LogTest("Write buff got error: %s", err)
			return
		}
	}
}

func client() {
	key := pbkdf2.Key([]byte("demo pass"), []byte("demo salt"), 1024, 32, sha1.New)
	block, _ := kcp.NewAESBlockCrypt(key)

	// wait for server to become ready
	time.Sleep(time.Second)

	// dial to the echo server
	if sess, err := kcp.DialWithDetailOptions("127.0.0.1:12345", block, 10, 3, nil, kcp.DebugLevelLog); err == nil {
		for {
			data := time.Now().String()
			buf := make([]byte, len(data))
			kcp.LogTest("sent: %s", data)
			if _, err := sess.Write([]byte(data)); err == nil {
				// read back the data
				if _, err := io.ReadFull(sess, buf); err == nil {
					kcp.LogTest("recv: %s", string(buf))
				} else {
					kcp.LogFatalf("ReadFull buff got error: %s", err)
				}
			} else {
				kcp.LogFatalf("Write buff got error: %s", err)
			}
			time.Sleep(time.Second)
		}
	} else {
		kcp.LogFatalf("DialWithDetailOptions got error: %s", err)
	}
}

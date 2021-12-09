package denorunner

import (
	"encoding/json"
	"fmt"
	"math/rand"
	"net"
	"time"
)

var handedOutPorts = map[int]bool{}

func FindFreePort(startPort int) int {
	rand.Seed(time.Now().UnixNano())
	port := startPort + rand.Intn(10000)

	iterations := 0
	for {
		if !handedOutPorts[port] {
			l, err := net.Listen("tcp", fmt.Sprintf("0.0.0.0:%d", port))
			if err == nil {
				l.Close()
				handedOutPorts[port] = true
				return port
			}
		}
		port = startPort + rand.Intn(10000)
		iterations++
		if iterations > 1000 {
			// Let's not go too crazy
			return -1
		}
	}
}

func MustJsonString(v interface{}) string {
	return string(MustJsonByteSlice(v))
}

func MustJsonByteSlice(v interface{}) []byte {
	buf, err := json.Marshal(v)
	if err != nil {
		fmt.Printf("JSON serialization error: %s\n", err)
	}
	return buf
}

// Command fsctl runs one FreeSWITCH API command over the event socket, using
// the password from .env (fs_cli only knows the factory default).
//
//	go run ./cmd/fsctl sofia status
package main

import (
	"fmt"
	"os"
	"strings"
	"time"

	"lca-backend/internal/config"
	"lca-backend/internal/esl"
)

func main() {
	if len(os.Args) < 2 {
		fmt.Fprintln(os.Stderr, "usage: fsctl <freeswitch api command>")
		os.Exit(2)
	}
	if _, err := config.Load(); err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
	addr := os.Getenv("FREESWITCH_ESL_ADDR")
	if addr == "" {
		addr = "127.0.0.1:8021"
	}
	conn, err := esl.Dial(addr, os.Getenv("FREESWITCH_ESL_PASSWORD"), 5*time.Second)
	if err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
	defer conn.Close()
	out, err := conn.API(strings.Join(os.Args[1:], " "), 30*time.Second)
	fmt.Println(out)
	if err != nil {
		os.Exit(1)
	}
}

// Command sipcall dials a number like a softphone and speaks a WAV file once
// answered, e.g. to call the support line through FreeSWITCH:
//
//	go run ./cmd/sipcall -server 192.168.24.136:5060 -user 1001 -password 1234 -to 5000 -wav customer.wav
package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"time"

	"lca-backend/internal/srctest"
)

func main() {
	server := flag.String("server", "", "SIP server host:port (required)")
	localIP := flag.String("local-ip", "", "this machine's IP (default: the server's host)")
	user := flag.String("user", "1001", "SIP account")
	password := flag.String("password", "", "SIP password (required)")
	to := flag.String("to", "5000", "number to dial")
	wav := flag.String("wav", "", "16-bit PCM WAV to speak (required)")
	ring := flag.Duration("ring", 60*time.Second, "how long to wait for an answer")
	flag.Parse()
	if *server == "" || *password == "" || *wav == "" {
		flag.Usage()
		os.Exit(2)
	}
	if *localIP == "" {
		*localIP = hostOf(*server)
	}
	audio, err := srctest.ReadWAV(*wav)
	if err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()
	err = srctest.Dial(ctx, srctest.PhoneCall{
		Server: *server, LocalIP: *localIP, User: *user, Password: *password, To: *to,
		Audio: audio, RingTimeout: *ring,
		Log: func(format string, args ...any) { fmt.Printf("[caller] "+format+"\n", args...) },
	})
	if err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
}

func hostOf(hostport string) string {
	for i := len(hostport) - 1; i >= 0; i-- {
		if hostport[i] == ':' {
			return hostport[:i]
		}
	}
	return hostport
}

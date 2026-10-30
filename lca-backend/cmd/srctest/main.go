// Command srctest places a test SIPREC recording session into the recorder:
// two participants, each with a WAV file of what they say.
//
//	go run ./cmd/srctest -agent-wav agent.wav -customer-wav customer.wav
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
	srs := flag.String("srs", "127.0.0.1:5090", "recorder SIP address")
	localIP := flag.String("local-ip", "127.0.0.1", "this machine's IP as the recorder sees it")
	transport := flag.String("transport", "tcp", "SIP transport: tcp or udp")
	agentAOR := flag.String("agent", "sip:BobS@pbx.local", "agent's SIP address (user part should be an agent ID)")
	customerAOR := flag.String("customer", "tel:+17035550100", "customer's address")
	agentWAV := flag.String("agent-wav", "", "16-bit PCM WAV of the agent's side (required)")
	customerWAV := flag.String("customer-wav", "", "16-bit PCM WAV of the customer's side (required)")
	flag.Parse()

	if *agentWAV == "" || *customerWAV == "" {
		flag.Usage()
		os.Exit(2)
	}
	if err := run(*srs, *localIP, *transport, *agentAOR, *customerAOR, *agentWAV, *customerWAV); err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
}

func run(srs, localIP, transport, agentAOR, customerAOR, agentWAV, customerWAV string) error {
	agent, err := srctest.ReadWAV(agentWAV)
	if err != nil {
		return fmt.Errorf("%s: %w", agentWAV, err)
	}
	customer, err := srctest.ReadWAV(customerWAV)
	if err != nil {
		return fmt.Errorf("%s: %w", customerWAV, err)
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()
	ctx, cancel := context.WithTimeout(ctx, 10*time.Minute)
	defer cancel()

	return srctest.Run(ctx, srctest.Call{
		SRS: srs, LocalIP: localIP, Transport: transport,
		// Caller first, agent second: the usual SRC order
		Parties: []srctest.Party{
			{AOR: customerAOR, Name: "Customer", Audio: customer},
			{AOR: agentAOR, Name: "Agent", Audio: agent},
		},
		Log: func(format string, args ...any) { fmt.Printf("[src] "+format+"\n", args...) },
	})
}

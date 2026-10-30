package esl

import (
	"bufio"
	"fmt"
	"net"
	"strings"
	"testing"
	"time"
)

// fakeFS speaks just enough of the event socket protocol.
func fakeFS(t *testing.T, password string) string {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { ln.Close() })
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			go func() {
				defer c.Close()
				r := bufio.NewReader(c)
				fmt.Fprint(c, "Content-Type: auth/request\n\n")
				for {
					line, err := r.ReadString('\n')
					if err != nil {
						return
					}
					_, _ = r.ReadString('\n') // blank line ending the command
					line = strings.TrimSpace(line)
					switch {
					case line == "auth "+password:
						fmt.Fprint(c, "Content-Type: command/reply\nReply-Text: +OK accepted\n\n")
					case strings.HasPrefix(line, "auth "):
						fmt.Fprint(c, "Content-Type: command/reply\nReply-Text: -ERR invalid\n\n")
						return
					case strings.HasPrefix(line, "api "):
						out := "echo:" + strings.TrimPrefix(line, "api ") + "\n"
						if strings.Contains(line, "bad") {
							out = "-ERR no such command\n"
						}
						fmt.Fprint(c, "Content-Type: log/data\nContent-Length: 3\n\nlog")
						fmt.Fprintf(c, "Content-Type: api/response\nContent-Length: %d\n\n%s", len(out), out)
					}
				}
			}()
		}
	}()
	return ln.Addr().String()
}

func TestAPI(t *testing.T) {
	addr := fakeFS(t, "secret")
	if _, err := Dial(addr, "wrong", time.Second); err != ErrAuth {
		t.Fatalf("wrong password err = %v", err)
	}
	c, err := Dial(addr, "secret", time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	out, err := c.API("sofia status", time.Second)
	if err != nil || out != "echo:sofia status" {
		t.Fatalf("out=%q err=%v", out, err)
	}
	if _, err := c.API("bad command", time.Second); err == nil {
		t.Error("-ERR not reported")
	}
	if _, err := c.API("two\nlines", time.Second); err == nil {
		t.Error("multi-line command accepted")
	}
}

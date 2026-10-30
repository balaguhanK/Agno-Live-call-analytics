// Package esl is a minimal FreeSWITCH Event Socket client for running API
// commands (inbound mode).
package esl

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"net"
	"net/textproto"
	"strconv"
	"strings"
	"time"
)

type Conn struct {
	conn net.Conn
	r    *textproto.Reader
}

var ErrAuth = errors.New("event socket password rejected")

// Dial connects and authenticates.
func Dial(addr, password string, timeout time.Duration) (*Conn, error) {
	nc, err := net.DialTimeout("tcp", addr, timeout)
	if err != nil {
		return nil, err
	}
	c := &Conn{conn: nc, r: textproto.NewReader(bufio.NewReader(nc))}
	_ = nc.SetDeadline(time.Now().Add(timeout))
	hdr, _, err := c.read()
	if err != nil {
		nc.Close()
		return nil, err
	}
	if hdr.Get("Content-Type") != "auth/request" {
		nc.Close()
		return nil, fmt.Errorf("unexpected greeting %q", hdr.Get("Content-Type"))
	}
	if _, err := fmt.Fprintf(nc, "auth %s\n\n", password); err != nil {
		nc.Close()
		return nil, err
	}
	hdr, _, err = c.read()
	if err != nil {
		nc.Close()
		return nil, err
	}
	if !strings.HasPrefix(hdr.Get("Reply-Text"), "+OK") {
		nc.Close()
		return nil, ErrAuth
	}
	_ = nc.SetDeadline(time.Time{})
	return c, nil
}

// read returns one message's headers and body.
func (c *Conn) read() (textproto.MIMEHeader, []byte, error) {
	hdr, err := c.r.ReadMIMEHeader()
	if err != nil {
		return nil, nil, err
	}
	var body []byte
	if n, _ := strconv.Atoi(hdr.Get("Content-Length")); n > 0 {
		body = make([]byte, n)
		if _, err := io.ReadFull(c.r.R, body); err != nil {
			return nil, nil, err
		}
	}
	return hdr, body, nil
}

// API runs a FreeSWITCH API command (as typed in fs_cli) and returns its output.
// Output starting with "-ERR" is returned as an error.
func (c *Conn) API(cmd string, timeout time.Duration) (string, error) {
	if strings.ContainsAny(cmd, "\r\n") {
		return "", errors.New("command must be one line")
	}
	_ = c.conn.SetDeadline(time.Now().Add(timeout))
	defer c.conn.SetDeadline(time.Time{})
	if _, err := fmt.Fprintf(c.conn, "api %s\n\n", cmd); err != nil {
		return "", err
	}
	for {
		hdr, body, err := c.read()
		if err != nil {
			return "", err
		}
		if hdr.Get("Content-Type") != "api/response" {
			continue // e.g. log or event messages
		}
		out := strings.TrimSpace(string(body))
		if strings.HasPrefix(out, "-ERR") {
			return out, errors.New(out)
		}
		return out, nil
	}
}

func (c *Conn) Close() error { return c.conn.Close() }

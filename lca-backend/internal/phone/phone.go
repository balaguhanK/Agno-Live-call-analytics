// Package phone holds what the dashboard's browser phone and FreeSWITCH must
// agree on: each agent's SIP account and how its password is derived.
package phone

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
)

// passwordChars is how many hex characters of the HMAC form a SIP password
const passwordChars = 32

// Config is the FreeSWITCH side of the browser phone.
type Config struct {
	WSURL       string // SIP over WebSocket, e.g. ws://192.168.24.136:5066
	Domain      string // SIP domain agents register in
	QueueNumber string // what customers dial to reach an agent
	Secret      string // derives agent SIP passwords; never sent to browsers
}

func (c Config) Enabled() bool { return c.WSURL != "" && c.Domain != "" && c.Secret != "" }

// Password derives an agent's SIP password, so FreeSWITCH's directory and the
// backend agree without storing a password per agent.
func Password(secret, agentID string) string {
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write([]byte("sip-password:" + agentID))
	return hex.EncodeToString(mac.Sum(nil))[:passwordChars]
}

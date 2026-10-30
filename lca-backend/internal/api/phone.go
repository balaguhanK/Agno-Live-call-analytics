package api

import (
	"net/http"

	"lca-backend/internal/phone"
)

type phoneConfigResponse struct {
	Enabled     bool   `json:"enabled"`
	Reason      string `json:"reason,omitempty"`
	WSURL       string `json:"wsUrl,omitempty"`
	Domain      string `json:"domain,omitempty"`
	SIPUser     string `json:"sipUser,omitempty"`
	Password    string `json:"password,omitempty"` // the agent's own SIP password
	QueueNumber string `json:"queueNumber,omitempty"`
}

// handlePhoneConfig serves GET /api/phone/config: the signed-in agent's
// FreeSWITCH account for the browser phone.
func (s *Server) handlePhoneConfig(w http.ResponseWriter, r *http.Request) {
	u := current(r).user
	switch {
	case !s.phone.Enabled():
		writeJSON(w, http.StatusOK, phoneConfigResponse{Reason: "The phone isn't set up on the server. Set FS_WS_URL, FS_SIP_DOMAIN and FS_SIP_SECRET in .env."})
	case u.AgentName == nil || *u.AgentName == "":
		writeJSON(w, http.StatusOK, phoneConfigResponse{Reason: "An agent ID is needed to take calls. Ask an admin to set one on the Users page."})
	default:
		writeJSON(w, http.StatusOK, phoneConfigResponse{
			Enabled: true, WSURL: s.phone.WSURL, Domain: s.phone.Domain, SIPUser: *u.AgentName,
			Password: phone.Password(s.phone.Secret, *u.AgentName), QueueNumber: s.phone.QueueNumber,
		})
	}
}

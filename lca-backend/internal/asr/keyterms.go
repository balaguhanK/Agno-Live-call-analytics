package asr

import "strings"

// maxKeyterms is Deepgram's limit on key terms per request
const maxKeyterms = 100

// DefaultKeyterms are words the speech model is told to favour, so names and
// banking words are heard right. Add more with DEEPGRAM_KEYTERMS (comma separated).
var DefaultKeyterms = []string{
	"Acme Bank", "personal loan", "EMI", "KYC", "UPI", "NEFT", "IFSC", "OTP", "PIN", "ATM",
	"credit score", "processing fee", "reservation fee", "foreclosure", "instalment", "interest rate",
	"debit card", "credit card", "virtual card", "replacement card", "mobile banking", "net banking",
	"refund", "chargeback", "fraud dispute", "overdraft", "account statement", "standing instruction",
	"cancellation", "retention offer", "one-time password",
}

// Keyterms joins the defaults with extra comma-separated terms, without repeats.
func Keyterms(extra string) []string {
	seen := map[string]bool{}
	var out []string
	add := func(term string) {
		term = strings.TrimSpace(term)
		if key := strings.ToLower(term); term != "" && !seen[key] && len(out) < maxKeyterms {
			seen[key] = true
			out = append(out, term)
		}
	}
	for _, t := range strings.Split(extra, ",") {
		add(t)
	}
	for _, t := range DefaultKeyterms {
		add(t)
	}
	return out
}

package asr

import "regexp"

// Speech models often mishear a few banking words. These fixes are kept to
// mistakes that are very unlikely to be what was really said.
type fix struct {
	re  *regexp.Regexp
	use string
}

var fixes = []fix{
	// "ATM" is often heard as "18" or "eighteen": "I lost my 18 card" → "I lost my ATM card".
	// Only before words about the machine or its card, and after a word that
	// points to it, so a real "18 cards" in a list of numbers is left alone.
	{regexp.MustCompile(`(?i)\b(my|the|your|this|that|an|a|at|new|old|lost|stolen|blocked|swallowed|damaged|expired|using|use)\s+(?:18|eighteen|8 am|a team)\s+(cards?|machines?|pins?|withdrawals?|transactions?|booths?|centres?|centers?)\b`), `${1} ATM ${2}`},
	{regexp.MustCompile(`(?i)\b(?:18|eighteen)\s+(card|machine|pin|withdrawal|booth)\b`), `ATM ${1}`},
	// Letters spoken one by one and written apart
	{regexp.MustCompile(`(?i)\ba\.?\s*t\.?\s*m\b`), `ATM`},
	{regexp.MustCompile(`(?i)\be\.?\s*m\.?\s*i\b`), `EMI`},
	{regexp.MustCompile(`(?i)\bk\.?\s*y\.?\s*c\b`), `KYC`},
	{regexp.MustCompile(`(?i)\bu\.?\s*p\.?\s*i\b`), `UPI`},
	{regexp.MustCompile(`(?i)\bn\.?\s*e\.?\s*f\.?\s*t\b`), `NEFT`},
	{regexp.MustCompile(`(?i)\bi\.?\s*f\.?\s*s\.?\s*c\b`), `IFSC`},
	{regexp.MustCompile(`(?i)\bo\.?\s*t\.?\s*p\b`), `OTP`},
}

// Correct fixes banking words the speech model usually mishears, in a
// finished line of the transcript.
func Correct(text string) string {
	for _, f := range fixes {
		text = f.re.ReplaceAllString(text, f.use)
	}
	return text
}

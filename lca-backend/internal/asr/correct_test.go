package asr

import "testing"

func TestCorrect(t *testing.T) {
	for in, want := range map[string]string{
		"Actually, I lost my 18 cards. So that I...": "Actually, I lost my ATM cards. So that I...",
		"My 18 card is blocked":                      "My ATM card is blocked",
		"The eighteen machine took my card":          "The ATM machine took my card",
		"I withdrew cash at the a t m yesterday.":    "I withdrew cash at the ATM yesterday.",
		"What is the e m i and the k y c status?":    "What is the EMI and the KYC status?",
		"Send the o t p to my u p i app":             "Send the OTP to my UPI app",
		// Real numbers are left alone
		"I am 18 years old":            "I am 18 years old",
		"I have 18 cards in my wallet": "I have 18 cards in my wallet",
		"The order is 18":              "The order is 18",
	} {
		if got := Correct(in); got != want {
			t.Errorf("Correct(%q)\n got %q\nwant %q", in, got, want)
		}
	}
}

package phone

import "testing"

func TestPassword(t *testing.T) {
	a, b := Password("s1", "BobS"), Password("s1", "BabuS")
	if len(a) != passwordChars || a == b || a != Password("s1", "BobS") || a == Password("s2", "BobS") {
		t.Errorf("passwords not unique and stable: %s %s", a, b)
	}
}

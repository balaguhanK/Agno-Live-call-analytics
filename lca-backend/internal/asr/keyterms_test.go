package asr

import (
	"slices"
	"testing"
)

func TestKeyterms(t *testing.T) {
	got := Keyterms(" Babu , emi, Acme Bank ,, Zoya ")
	if got[0] != "Babu" || got[1] != "emi" || !slices.Contains(got, "Zoya") || !slices.Contains(got, "personal loan") {
		t.Errorf("Keyterms = %q", got)
	}
	// "emi" and the default "EMI" are the same word, listed once
	count := 0
	for _, term := range got {
		if len(term) == 3 && (term == "EMI" || term == "emi") {
			count++
		}
	}
	if count != 1 || len(got) > maxKeyterms {
		t.Errorf("repeats or too many: %d, %d terms", count, len(got))
	}
}

func TestStreamQueryKeyterms(t *testing.T) {
	c := NewClient("key", "nova-3")
	c.Keyterms = []string{"Acme Bank", "EMI"}
	q := c.streamQuery(StreamOptions{Language: "en-IN", Encoding: "linear16", SampleRate: 8000})
	if !slices.Equal(q["keyterm"], []string{"Acme Bank", "EMI"}) || q.Get("language") != "en-IN" {
		t.Errorf("query = %v", q)
	}
	// Older models reject key terms
	old := NewClient("key", "nova-2")
	old.Keyterms = c.Keyterms
	if q := old.streamQuery(StreamOptions{Language: "en-US"}); len(q["keyterm"]) != 0 {
		t.Errorf("nova-2 query has key terms: %v", q)
	}
}

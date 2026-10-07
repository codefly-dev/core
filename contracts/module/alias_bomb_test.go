package module

import (
	"fmt"
	"strings"
	"testing"
	"time"
)

// TestAnAliasBombIsAnsweredAtOnce: the contract reader shared the walk that
// followed every alias, so a 579-byte document of fan-ten aliases took over a
// second at seven levels and two minutes at nine. An alias target is walked
// once now, at its anchor; twelve levels are refused — the bomb's keys are
// unknown to a contract — in well under a second.
func TestAnAliasBombIsAnsweredAtOnce(t *testing.T) {
	var b strings.Builder
	b.Write(fixture(t))
	b.WriteString("x0: &a0 [lol]\n")
	for n := 1; n <= 12; n++ {
		b.WriteString(fmt.Sprintf("x%d: &a%d [", n, n))
		for i := 0; i < 10; i++ {
			if i > 0 {
				b.WriteString(", ")
			}
			b.WriteString(fmt.Sprintf("*a%d", n-1))
		}
		b.WriteString("]\n")
	}
	start := time.Now()
	if _, err := Parse([]byte(b.String())); err == nil {
		t.Fatal("the bomb's keys are unknown to a module contract and must be refused")
	}
	if elapsed := time.Since(start); elapsed > 5*time.Second {
		t.Fatalf("the walk took %s; an alias target is walked once", elapsed)
	}
}

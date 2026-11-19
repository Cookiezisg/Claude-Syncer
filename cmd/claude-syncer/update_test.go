package main

import (
	"os/exec"
	"testing"
)

func TestNewer(t *testing.T) {
	cases := []struct {
		a, b string
		want bool
	}{
		{"0.2.0", "0.1.9", true},
		{"0.10.0", "0.9.0", true},
		{"1.0.0", "1.0.0", false},
		{"1.0.0", "1.0.0-rc1", true},
		{"1.0.0-rc1", "1.0.0", false},
		{"1.0", "1.0.1", false},
		{"v1.2.0", "1.1.0", true},
		{"", "1.0.0", false},
		{"1.0.0", "dev", false},
		{"garbage", "1.0.0", false},
	}
	for _, c := range cases {
		if got := newer(c.a, c.b); got != c.want {
			t.Errorf("newer(%q, %q) = %v, want %v", c.a, c.b, got, c.want)
		}
	}
}

func TestParseFormulaVersion(t *testing.T) {
	out, err := exec.Command("../../scripts/render-formula.sh", "1.2.3", "abc").Output()
	if err != nil {
		t.Fatal(err)
	}
	if got := parseFormulaVersion(out); got != "1.2.3" {
		t.Errorf("version = %q", got)
	}
	if got := parseFormulaVersion([]byte("nothing")); got != "" {
		t.Errorf("version = %q", got)
	}
}

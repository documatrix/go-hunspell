package core

import "testing"

// TestAtoiGlibc checks atoi against the results of glibc's atoi, which is
// strtol saturating at the range of the 64-bit long, cast to int.
func TestAtoiGlibc(t *testing.T) {
	for in, want := range map[string]int{
		"1099511627777": 1, "2000000000000": -1454759936, "-2000000000000": 1454759936,
		"99999999999999999999": -1, "-99999999999999999999": 0,
		"9223372036854775807": -1, "9223372036854775808": -1,
		"-9223372036854775808": 0, "-9223372036854775809": 0,
		"4294967297": 1, " +12x": 12, "-0012": -12, "": 0, "x1": 0,
	} {
		if got := atoi(in); got != want {
			t.Errorf("atoi(%q) = %d, want %d", in, got, want)
		}
	}
}

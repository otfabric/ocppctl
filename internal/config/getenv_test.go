// SPDX-License-Identifier: MIT

package config

import "testing"

func TestGetenvFormatSpellings(t *testing.T) {
	for _, c := range []struct{ canonical, legacy, want string }{
		{"", "", ""},
		{"json", "", "json"},
		{"", "json", "json"},
		{"table", "json", "table"},
	} {
		t.Setenv("OCPPCTL_FORMAT", c.canonical)
		t.Setenv("OCPPCTL_OUTPUT_FORMAT", c.legacy)
		if got := Getenv("OCPPCTL_FORMAT"); got != c.want {
			t.Errorf("canonical %q, legacy %q: got %q, want %q", c.canonical, c.legacy, got, c.want)
		}
	}
}

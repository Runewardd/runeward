package cli

import "testing"

func TestClampTermDimension(t *testing.T) {
	for _, tc := range []struct {
		name  string
		input int
		want  uint16
	}{
		{name: "negative", input: -1, want: 0},
		{name: "zero", input: 0, want: 0},
		{name: "ordinary", input: 120, want: 120},
		{name: "maximum", input: 65535, want: 65535},
		{name: "overflow", input: 65536, want: 65535},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := clampTermDimension(tc.input); got != tc.want {
				t.Fatalf("clampTermDimension(%d) = %d, want %d", tc.input, got, tc.want)
			}
		})
	}
}

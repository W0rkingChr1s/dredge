package report

import "testing"

func TestFmtSize(t *testing.T) {
	cases := map[int64]string{
		0:          "0 B",
		512:        "512 B",
		1024:       "1.0 KB",
		1048576:    "1.0 MB",
		1610612736: "1.5 GB",
	}
	for in, want := range cases {
		if got := FmtSize(in); got != want {
			t.Errorf("FmtSize(%d) = %q, want %q", in, got, want)
		}
	}
}

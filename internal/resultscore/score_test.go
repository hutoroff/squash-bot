package resultscore

import (
	"strings"
	"testing"
)

func TestValidate(t *testing.T) {
	a, b, other := int64(1), int64(2), int64(3)
	for _, tt := range []struct {
		score  string
		winner *int64
		valid  bool
	}{
		{"", &a, true}, {"", nil, true}, {"", &other, false},
		{"11:9", &a, true}, {"9:11", &b, true}, {"11:0", &a, true},
		{"9:11", &a, false}, {"11:9", nil, false}, {"2:2", &a, false},
		{"0:0", nil, true}, {"0:0", &a, false}, {"2:2", nil, true},
		{"-1:2", &b, false}, {"1.0:2", &b, false}, {"1:2:3", &b, false},
		{"1000000:0", &a, false}, {strings.Repeat("9", 1000) + ":0", &a, false},
	} {
		if err := Validate(tt.score, a, b, tt.winner); (err == nil) != tt.valid {
			t.Errorf("Validate(%q, %v) = %v", tt.score, tt.winner, err)
		}
	}
}

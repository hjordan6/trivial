package content

import "testing"

func TestBandForRating(t *testing.T) {
	tests := []struct {
		rating int
		want   Difficulty
		valid  bool
	}{
		{1, Easy, true}, {4, Easy, true}, {5, Medium, true}, {7, Medium, true}, {8, Hard, true}, {10, Hard, true}, {0, "", false}, {11, "", false},
	}
	for _, tt := range tests {
		got, err := BandForRating(tt.rating)
		if (err == nil) != tt.valid || got != tt.want {
			t.Errorf("BandForRating(%d)=(%q,%v), want (%q, valid=%v)", tt.rating, got, err, tt.want, tt.valid)
		}
	}
}

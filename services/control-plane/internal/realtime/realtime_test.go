package realtime

import (
	"errors"
	"testing"
)

func TestIsNotFound(t *testing.T) {
	for _, tc := range []struct {
		err  error
		want bool
	}{
		{nil, false},
		{errors.New("twirp error not_found: requested room does not exist"), true},
		{errors.New("room not found"), true},
		{errors.New("twirp error unavailable: connection refused"), false},
	} {
		if got := isNotFound(tc.err); got != tc.want {
			t.Errorf("isNotFound(%v) = %v, want %v", tc.err, got, tc.want)
		}
	}
}

package mgr

import "testing"

func TestIsGitCommitSHA(t *testing.T) {
	tests := []struct {
		in   string
		want bool
	}{
		{"abc", false},
		{"v0.1.0", false},
		{"f8dc0385aa4f7b8c913407d27dcc308f70362930", true},
		{"F8DC0385AA4F7B8C913407D27DCC308F70362930", true},
		{"f8dc0385aa4f7b8c913407d27dcc308f7036293", false},
	}
	for _, tc := range tests {
		if got := isGitCommitSHA(tc.in); got != tc.want {
			t.Errorf("isGitCommitSHA(%q) = %v, want %v", tc.in, got, tc.want)
		}
	}
}

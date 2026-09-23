package paths

import "testing"

func TestLooksLikeWorkspaceID(t *testing.T) {
	cases := []struct {
		in   string
		want bool
	}{
		{"44847637afdcf3dc29f818063cd7cd8c", true},
		{"44847637AFDCF3DC29F818063CD7CD8C", true},
		{"/home/ulap92/44847637afdcf3dc29f818063cd7cd8c", true},
		{"/home/ulap92/Documents/Arpit/Projects/PledgeBall", false},
		{"1f6b9b2ad3ed61f81bd55495f041a05e", true},
		{"short", false},
		{"44847637afdcf3dc29f818063cd7cd8c0", false}, // 33
		{"gg847637afdcf3dc29f818063cd7cd8c", false},
		{"", false},
	}
	for _, tc := range cases {
		if got := LooksLikeWorkspaceID(tc.in); got != tc.want {
			t.Fatalf("LooksLikeWorkspaceID(%q)=%v want %v", tc.in, got, tc.want)
		}
	}
}

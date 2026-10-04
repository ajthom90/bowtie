package parental

import "testing"

func TestPickPrefersUSTV(t *testing.T) {
	cases := []struct {
		in   []Rated
		want string
	}{
		{[]Rated{{System: "MPAA", Code: "PG-13"}, {System: "VCHIP", Code: "TV-14"}}, "TV-14"},
		{[]Rated{{System: "USA Parental Rating", Code: "TVPG"}}, "TVPG"},
		{[]Rated{{System: "MPAA", Code: "R"}}, "R"},
		{[]Rated{{System: "Canadian Parental Rating", Code: "14+"}, {System: "", Code: "TV-G"}}, "TV-G"},
		{[]Rated{{System: "Canadian Parental Rating", Code: "14+"}}, ""},
		{nil, ""},
	}
	for _, c := range cases {
		if got := Pick(c.in); got != c.want {
			t.Errorf("Pick(%v)=%q want %q", c.in, got, c.want)
		}
	}
}

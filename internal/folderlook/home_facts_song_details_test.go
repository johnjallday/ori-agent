package folderlook

import "testing"

// What an existing Home's consent reaches is a fact the plan words its promise
// from, so the host's report is bounded: only the two known values, and only for
// a Home that exists (a Home the run creates reads nothing yet).
func TestCleanHomeFacts_KeepsWhatAnExistingHomeReadsAndNothingElse(t *testing.T) {
	for _, tc := range []struct {
		name string
		in   HomeFacts
		want string
	}{
		{"numbers", HomeFacts{HomeExists: true, SongDetails: SongDetailsNumbers}, SongDetailsNumbers},
		{"names", HomeFacts{HomeExists: true, SongDetails: SongDetailsNames}, SongDetailsNames},
		{"a Home that does not exist yet", HomeFacts{SongDetails: SongDetailsNames}, ""},
		{"a value the host never reports", HomeFacts{HomeExists: true, SongDetails: "everything"}, ""},
		{"nothing reported", HomeFacts{HomeExists: true}, ""},
	} {
		if got := cleanHomeFacts(tc.in).SongDetails; got != tc.want {
			t.Errorf("%s: SongDetails = %q, want %q", tc.name, got, tc.want)
		}
	}
}

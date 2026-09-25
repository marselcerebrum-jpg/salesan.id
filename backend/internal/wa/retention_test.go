package wa

import "testing"

// How long a file is kept is two settings meeting, and getting it wrong in
// either direction is expensive: too long fills the disk, which is what took
// Postgres down, and too short deletes a file whose message the inbox is still
// showing.
func TestMediaWindowTakesTheShorterOfTheTwo(t *testing.T) {
	cases := []struct {
		name   string
		window int
		limit  int
		want   int
	}{
		{"batas media lebih pendek dari jendela inbox", 7, 2, 2},
		{"jendela inbox sudah lebih pendek", 1, 2, 1},
		{"keduanya sama", 2, 2, 2},
		{"batas tidak dipakai", 7, 0, 7},
		{"batas tidak masuk akal", 7, -1, 7},
		{"jendela nol tetap menyimpan sehari", 0, 0, 1},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := mediaWindowDays(c.window, c.limit); got != c.want {
				t.Errorf("mediaWindowDays(%d, %d) = %d, want %d", c.window, c.limit, got, c.want)
			}
		})
	}
}

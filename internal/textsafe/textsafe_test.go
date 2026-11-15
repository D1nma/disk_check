package textsafe

import "testing"

func TestClean(t *testing.T) {
	cases := []struct{ name, in, want string }{
		{"ascii unchanged", "hello world.txt", "hello world.txt"},
		{"utf8 unchanged", "été 日本語 🎉", "été 日本語 🎉"},
		{"newline", "a\nb", `a\nb`},
		{"tab and cr", "a\tb\rc", `a\tb\rc`},
		{"osc title", "evil\x1b]0;PWNED\x07x", "evil?]0;PWNED?x"},
		{"csi color", "\x1b[31mRED", "?[31mRED"},
		{"del", "a\x7fb", "a?b"},
		{"c1 csi", "a\u009bb", "a?b"},
		{"invalid utf8", "a\xffb", "a�b"},
		{"bidi override", "txt‮exe.sh", "txt?exe.sh"},
		{"bidi isolate", "a⁦b⁩", "a?b?"},
		{"nul", "a\x00b", "a?b"},
		{"empty", "", ""},
	}
	for _, c := range cases {
		if got := Clean(c.in); got != c.want {
			t.Errorf("%s: Clean(%q) = %q, want %q", c.name, c.in, got, c.want)
		}
	}
}

func TestCleanNoAllocASCII(t *testing.T) {
	s := "/var/lib/docker/overlay2/abc/diff"
	allocs := testing.AllocsPerRun(100, func() { _ = Clean(s) })
	if allocs != 0 {
		t.Fatalf("Clean allocated %.0f times on printable ASCII", allocs)
	}
}

func TestCleanNoAllocValidUTF8(t *testing.T) {
	s := "/home/élodie/Téléchargements"
	allocs := testing.AllocsPerRun(100, func() { _ = Clean(s) })
	if allocs != 0 {
		t.Fatalf("Clean allocated %.0f times on clean UTF-8", allocs)
	}
}

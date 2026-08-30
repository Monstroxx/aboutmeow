package main

import "testing"

// makeAboutInput must always send a valid pointer for Text and convert the
// duration correctly.
func TestMakeAboutInput(t *testing.T) {
	in := makeAboutInput("test bio", "", 86400)
	if in.Text == nil || *in.Text != "test bio" {
		t.Fatalf("text not set correctly: %v", in.Text)
	}
	if s := in.Duration.Seconds(); s != 86400 {
		t.Fatalf("duration mismatch: %f", s)
	}
	if in.Emoji != nil {
		t.Fatal("emoji should be nil when empty")
	}

	in = makeAboutInput("x", "🙂", 0)
	if in.Emoji == nil || in.Emoji.Content != "🙂" {
		t.Fatalf("emoji not set: %v", in.Emoji)
	}
}

func TestParseDurationSeconds(t *testing.T) {
	for _, c := range []struct {
		in      string
		want    int64
		wantErr bool
	}{
		{"", 0, false},
		{"86400", 86400, false},
		{"-1", 0, true},
		{"abc", 0, true},
	} {
		got, err := parseDurationSeconds(c.in)
		if (err != nil) != c.wantErr {
			t.Fatalf("parse %q err=%v wantErr=%v", c.in, err, c.wantErr)
		}
		if got != c.want {
			t.Fatalf("parse %q = %d want %d", c.in, got, c.want)
		}
	}
}
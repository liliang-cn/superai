package backend

import (
	"errors"
	"io"
	"strings"
	"testing"
)

func TestALineTooLongIsPassedOverNotTheEndOfTheStream(t *testing.T) {
	huge := "data: " + strings.Repeat("x", 300<<10)
	in := "data: one\n" + huge + "\ndata: two\n\ndata: three"
	sc := newEventLines(strings.NewReader(in), 100<<10)
	var got []string
	for sc.Scan() {
		got = append(got, sc.Text())
	}
	want := []string{"data: one", "data: two", "", "data: three"}
	if strings.Join(got, "|") != strings.Join(want, "|") {
		t.Fatalf("got %q", got)
	}
	if sc.Err() != nil {
		t.Fatalf("err %v", sc.Err())
	}
}

func TestALineAtTheLimitIsKept(t *testing.T) {
	line := strings.Repeat("y", 1000)
	sc := newEventLines(strings.NewReader(line+"\n"), 1000)
	if !sc.Scan() || sc.Text() != line {
		t.Fatal("a line of exactly max was dropped")
	}
}

type brokenReader struct{}

func (brokenReader) Read([]byte) (int, error) { return 0, errors.New("connection reset") }

func TestAReadErrorIsReported(t *testing.T) {
	sc := newEventLines(io.MultiReader(strings.NewReader("data: a\n"), brokenReader{}), 1<<10)
	if !sc.Scan() || sc.Text() != "data: a" {
		t.Fatal("lost the line before the error")
	}
	if sc.Scan() || sc.Err() == nil || !strings.Contains(sc.Err().Error(), "reset") {
		t.Fatalf("err %v", sc.Err())
	}
}

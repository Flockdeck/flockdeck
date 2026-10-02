package session

import (
	"slices"
	"testing"
	"time"
)

// A window showing a pane has to be told the size of the pty at the byte of
// the output it changed at, so that what the program drew for one size is
// never drawn into a terminal of another.
func TestSizeMarksSayWhereInTheOutputThePaneWasResized(t *testing.T) {
	s := fakeSession(newFakePTY())
	marks, changed := s.SizeMarks(0)
	if len(marks) != 1 || marks[0].At != 0 || marks[0].Cols != 80 || marks[0].Rows != 24 {
		t.Fatalf("a pane never resized has marks %+v, want only its starting size from byte 0", marks)
	}
	select {
	case <-changed:
		t.Fatal("the size was reported changed before anything changed it")
	default:
	}

	s.publish([]byte("0123456789"))
	s.Resize(100, 30)
	select {
	case <-changed:
	case <-time.After(5 * time.Second):
		t.Fatal("a resize was not reported")
	}
	s.publish([]byte("abc"))
	s.Resize(60, 20)

	marks, _ = s.SizeMarks(0)
	got := make([][3]int, len(marks))
	for i, m := range marks {
		got[i] = [3]int{int(m.At), m.Cols, m.Rows}
	}
	want := [][3]int{{0, 80, 24}, {10, 100, 30}, {13, 60, 20}}
	if !slices.Equal(got, want) {
		t.Fatalf("marks are %v, want %v", got, want)
	}

	// Asking after the last one seen gives only what came later.
	later, _ := s.SizeMarks(marks[1].Seq)
	if len(later) != 1 || later[0].Cols != 60 {
		t.Fatalf("the marks after the second are %+v, want only the third", later)
	}

	// A resize to the size it already is changes nothing, and the program is
	// not redrawn for it, so there is nothing to say.
	s.Resize(60, 20)
	if again, _ := s.SizeMarks(0); len(again) != len(marks) {
		t.Fatalf("a resize to the same size was marked: %+v", again)
	}
}

// Marks are bounded, with the newest kept.
func TestSizeMarksAreBounded(t *testing.T) {
	s := fakeSession(newFakePTY())
	for i := 0; i < maxSizeMarks+50; i++ {
		s.Resize(20+i%100, 10+i%30+i/100)
	}
	marks, _ := s.SizeMarks(0)
	if len(marks) != maxSizeMarks {
		t.Fatalf("%d marks kept, want %d", len(marks), maxSizeMarks)
	}
	if cols, rows := s.Size(); marks[len(marks)-1].Cols != cols || marks[len(marks)-1].Rows != rows {
		t.Fatalf("the newest mark is %+v, the pane is %dx%d", marks[len(marks)-1], cols, rows)
	}
}

// More resizes than marks are kept for must not leave the start of what is
// held with no size: the last mark dropped stands in for it.
func TestTheStartOfHeldOutputKeepsASizeWhenMarksAreDropped(t *testing.T) {
	s := fakeSession(newFakePTY())
	for i := 0; i < maxSizeMarks+50; i++ {
		s.publish([]byte("x"))
		s.Resize(20+i%100, 10+i%30+i/100)
	}
	marks, _ := s.SizeMarks(0)
	if len(marks) != maxSizeMarks {
		t.Fatalf("%d marks kept, want %d", len(marks), maxSizeMarks)
	}
	if marks[0].At != 0 {
		t.Fatalf("the first mark kept begins at byte %d, so the bytes before it have no size", marks[0].At)
	}
	for i := 1; i < len(marks); i++ {
		if marks[i].At < marks[i-1].At || marks[i].Seq <= marks[i-1].Seq {
			t.Fatalf("marks out of order at %d: %+v then %+v", i, marks[i-1], marks[i])
		}
	}
}

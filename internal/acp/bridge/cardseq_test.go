package bridge

import "testing"

// The tag is never zero, even from bytes that are, and the lowest of its 21 bits
// is always set.
func TestCardTagIsNeverZero(t *testing.T) {
	if got, want := cardTag([4]byte{}), uint64(1)<<32; got != want {
		t.Errorf("cardTag(0) = %#x, want %#x", got, want)
	}
}

// Only 21 bits of the bytes are used, so a card number stays under 2^53.
func TestCardTagKeepsTwentyOneBits(t *testing.T) {
	got := cardTag([4]byte{0xff, 0xff, 0xff, 0xff})
	if want := uint64(1<<21-1) << 32; got != want {
		t.Errorf("cardTag(all ones) = %#x, want %#x", got, want)
	}
	if got|(1<<32-1) >= 1<<53 {
		t.Errorf("a card number with this tag reaches 2^53: %#x", got|(1<<32-1))
	}
	// A bit above the 21st is dropped, and the lowest is forced.
	if got, want := cardTag([4]byte{0x00, 0x00, 0x20, 0x00}), uint64(1)<<32; got != want {
		t.Errorf("cardTag(bit 21 only) = %#x, want %#x", got, want)
	}
	if got, want := cardTag([4]byte{0x02, 0x00, 0x00, 0x00}), uint64(3)<<32; got != want {
		t.Errorf("cardTag(bit 1) = %#x, want %#x", got, want)
	}
}

// The count wraps inside its 32 bits and leaves the tag alone.
func TestCardSeqCountWrapsWithoutTouchingTheTag(t *testing.T) {
	was := cardSeqNext.Load()
	t.Cleanup(func() { cardSeqNext.Store(was) })
	cardSeqNext.Store(1<<32 - 2)
	a, b, c := nextCardSeq(), nextCardSeq(), nextCardSeq()
	for _, seq := range []uint64{a, b, c} {
		if seq>>32 != cardSeqTag>>32 {
			t.Errorf("seq %#x lost the tag %#x", seq, cardSeqTag)
		}
	}
	if a&(1<<32-1) != 1<<32-1 || b&(1<<32-1) != 0 || c&(1<<32-1) != 1 {
		t.Errorf("counts %#x %#x %#x, want the last, then 0, then 1", a&(1<<32-1), b&(1<<32-1), c&(1<<32-1))
	}
}

// The tag's lowest bit is the count's 33rd, so the count is told from the tag
// by its mask and not by that bit being set already: a count past 2^33 would
// otherwise reach into the tag.
func TestCardSeqCountPastTwoToTheThirtyThirdStaysOutOfTheTag(t *testing.T) {
	was := cardSeqNext.Load()
	t.Cleanup(func() { cardSeqNext.Store(was) })
	cardSeqNext.Store(1<<33 - 1)
	if seq := nextCardSeq(); seq != cardSeqTag {
		t.Errorf("seq %#x, want the bare tag %#x", seq, cardSeqTag)
	}
}

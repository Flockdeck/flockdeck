package tool

import (
	"bufio"
	"unicode/utf16"
	"unicode/utf8"
)

// utf16Text is r's text as UTF-8, where r starts with a UTF-16 byte-order
// mark, and whether it did.
//
// Windows PowerShell's > and Out-File write UTF-16, so a log a Windows user
// saved from a command is as likely as not to be in it. Every other character
// of such a file is a NUL, which is the mark of a binary file, and the model
// was told a log it had been asked to read looked like one.
func utf16Text(r *bufio.Reader) (*bufio.Reader, bool) {
	head, _ := r.Peek(2)
	if !hasUTF16BOM(head) {
		return r, false
	}
	be := head[0] == 0xfe
	r.Discard(2)
	return bufio.NewReaderSize(&utf16Reader{r: r, be: be}, 64<<10), true
}

// hasUTF16BOM reports whether data starts with a UTF-16 byte-order mark,
// little-endian or big.
func hasUTF16BOM(data []byte) bool {
	return len(data) >= 2 && (data[0] == 0xff && data[1] == 0xfe || data[0] == 0xfe && data[1] == 0xff)
}

// utf16Reader decodes UTF-16 to UTF-8 as it is read, so that a large file is
// read a piece at a time like any other.
type utf16Reader struct {
	r  *bufio.Reader
	be bool
	// pending is the rest of a character that did not fit in the last read.
	pending []byte
}

func (u *utf16Reader) Read(p []byte) (int, error) {
	n := copy(p, u.pending)
	u.pending = u.pending[n:]
	for n < len(p) {
		unit, err := u.unit()
		if err != nil {
			if n > 0 {
				return n, nil
			}
			return 0, err
		}
		r := rune(unit)
		// A surrogate is half of a character, and a high one is followed by
		// the low one that completes it -- where the file is well formed. The
		// unit after a high surrogate is taken only when it is that low half:
		// taken regardless, a surrogate left unpaired, as a file name cut in
		// two can leave one, swallowed the character after it.
		switch {
		case r >= 0xd800 && r < 0xdc00:
			if next, ok := u.peekUnit(); ok && next >= 0xdc00 && next < 0xe000 {
				u.r.Discard(2)
				r = utf16.DecodeRune(r, rune(next))
			} else {
				r = utf8.RuneError
			}
		case utf16.IsSurrogate(r):
			r = utf8.RuneError
		}
		var buf [utf8.UTFMax]byte
		w := utf8.EncodeRune(buf[:], r)
		c := copy(p[n:], buf[:w])
		n += c
		if c < w {
			u.pending = append(u.pending[:0], buf[c:w]...)
		}
	}
	return n, nil
}

// unit reads one UTF-16 code unit. An odd byte left at the end is dropped.
//
// It reads the two bytes one at a time from the buffered reader, which costs
// nothing. Read into an array through io.ReadFull, the array escaped to the
// heap: one allocation for every character of the file, four million of them
// for an 8 MB log.
func (u *utf16Reader) unit() (uint16, error) {
	b0, err := u.r.ReadByte()
	if err != nil {
		return 0, err
	}
	b1, err := u.r.ReadByte()
	if err != nil {
		return 0, err
	}
	return u.decode(b0, b1), nil
}

// peekUnit is the next code unit, without reading past it.
func (u *utf16Reader) peekUnit() (uint16, bool) {
	b, err := u.r.Peek(2)
	if err != nil {
		return 0, false
	}
	return u.decode(b[0], b[1]), true
}

// decode is two bytes as one code unit, in the file's own byte order.
func (u *utf16Reader) decode(b0, b1 byte) uint16 {
	if u.be {
		return uint16(b0)<<8 | uint16(b1)
	}
	return uint16(b1)<<8 | uint16(b0)
}

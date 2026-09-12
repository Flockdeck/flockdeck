package tool

import (
	"bufio"
	"io"
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
		if utf16.IsSurrogate(r) {
			if next, err := u.unit(); err == nil {
				r = utf16.DecodeRune(r, rune(next))
			} else {
				r = utf8.RuneError
			}
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
func (u *utf16Reader) unit() (uint16, error) {
	var b [2]byte
	if _, err := io.ReadFull(u.r, b[:]); err != nil {
		if err == io.ErrUnexpectedEOF {
			return 0, io.EOF
		}
		return 0, err
	}
	if u.be {
		return uint16(b[0])<<8 | uint16(b[1]), nil
	}
	return uint16(b[1])<<8 | uint16(b[0]), nil
}

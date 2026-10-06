package baton

import (
	"math/rand"
	"strconv"
	"strings"
)

// gateCases2Extra are the lines added to the second gate after the first 12000 (which are
// what they were): secrets that are numbers, masks that are not masks, and numbers under
// names that end as a time. Each is a secret that has to go, so that a mask rule that is too
// broad, a time rule that is applied to every name, or one that is widened, makes the gate
// fail.
func gateCases2Extra() []gateCase {
	r := rand.New(rand.NewSource(11))
	digits := func(first string, n int) string {
		var b strings.Builder
		b.WriteString(first)
		for b.Len() < n {
			b.WriteString(strconv.Itoa(r.Intn(10)))
		}
		return b.String()
	}
	out := make([]gateCase, 0, 2400)
	for len(out) < 2400 {
		k := gateKeys[r.Intn(len(gateKeys))]
		var line, v string
		switch r.Intn(12) {
		case 0: // a mask of four to seven stars is not a mask
			v = strings.Repeat("*", 4+r.Intn(4))
			line = k + "=" + v
		case 1: // x is not a mask
			v = strings.Repeat("x", 6+r.Intn(8))
			line = k + ": " + v
		case 2: // a number is a password
			v = digits("", 6+r.Intn(3))
			line = k + "=" + v
		case 3: // a long number under a name that ends as a time, with no epoch shape
			v = digits(strconv.Itoa(2+r.Intn(8)), 10)
			line = "password_expires=" + v
		case 4: // nine digits under _at
			v = digits(strconv.Itoa(1+r.Intn(9)), 9)
			line = []string{"password_at", "secret_at", "api_key_at"}[r.Intn(3)] + "=" + v
		case 5: // an epoch shape under a name whose secret word never lets a time through
			v = digits("1", 10)
			line = []string{"password_expires", "secret_expires_at", "api_key_expires", "auth_token_expires"}[r.Intn(4)] + "=" + v
		case 6: // thirteen digits under such a name
			v = digits("1", 13)
			line = []string{"password_at", "api_key_at", "secret_date"}[r.Intn(3)] + ": " + v
		case 7: // a token's time must have the shape, which this does not
			v = digits(strconv.Itoa(2+r.Intn(8)), 10)
			line = "token_expires_at=" + v
		case 8: // a token's time with no word that says what it is of
			v = digits("1", 10)
			line = "token_at=" + v
		case 9:
			v = digits(strconv.Itoa(2+r.Intn(8)), 13)
			line = "session_token_expires_at: " + v
		case 10: // a number under a name with the words of a time and of a secret
			v = digits("", 9+r.Intn(5))
			line = []string{"auth_token_time", "secret_time", "password_date"}[r.Intn(3)] + "=" + v
		default: // x of any length under a secret name
			v = strings.Repeat("x", 8+r.Intn(5))
			line = k + "=" + v
		}
		out = append(out, gateCase{line, v})
	}
	return out
}

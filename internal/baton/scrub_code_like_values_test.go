package baton

import (
	"strings"
	"testing"
)

// A time as a number is kept under a name that is not a secret's, and under a secret's
// only by the rule in epochSecretWords.
func TestNumbersUnderSecretNamesThatEndAsTimesAreSecretsUnlessTheRuleKeepsThem(t *testing.T) {
	for _, in := range []string{
		"password_at=987654321", "token_at=1712345678", "password_expires=1712345678", "api_key_expires = 4412345678",
		"secret_expires_at=1712345678", "api_key_at=1712345678123", "password_date=1712345678", "auth_token_time=987654321",
		"token_expires_at=4712345678", "session_token_expires_at=17123456789", "auth_token_expires_at=1712345678",
	} {
		got, _ := NewScrubber().Scrub(in)
		if got == in {
			t.Errorf("Scrub(%q) kept the number", in)
		}
	}
	for _, in := range []string{
		"token_expires_at=1700000000", "session_token_expires_at: 1700000000000", "access_key_id_expires=1700000000",
		"created_at=1712345678", "expires=987654321", "last_login_time=987654321", "updated_at: 1712345678123", "token_created_at=1712345678",
	} {
		untouched(t, in)
	}
}

// Under a name that ends in pass, pwd, pw or key, nothing is left alone for looking like
// code: a password with brackets, braces or dots in it looks like code.
func TestPasswordsThatLookLikeCodeUnderPassAndKeyNamesAreTaken(t *testing.T) {
	for in, secret := range map[string]string{
		"signing_key=Hunter2(2024)Abc":                   "Hunter2(2024)Abc",
		"app_pass=Hunter2[prod]x":                        "Hunter2[prod]x",
		"db_pass: {Winter2024!}":                         "Winter2024!",
		"admin_pass=Ab3$[xyz]Q9wer":                      "Ab3$[xyz]Q9wer",
		"user_pass=<Qwerty123456>":                       "Qwerty123456",
		"deploy_key: Qwerty.AsdfGhjk":                    "Qwerty.AsdfGhjk",
		"master_key=Winter.Spring.Summer":                "Winter.Spring.Summer",
		"license_key=ABCD.EFGH.IJKL":                     "ABCD.EFGH.IJKL",
		"pw = Gh7.k2.z(9)":                               "Gh7.k2.z(9)",
		`password = os.getenv("X")+"hunter2"`:            "hunter2",
		`password = os.Getenv("X") # old Hunter2Hunter2`: "Hunter2Hunter2",
		`token = os.environ.get("X") + "Hunter2Hunter2"`: "Hunter2Hunter2",
		"dbPass = getUserPassword":                       "getUserPassword",
		"key = \"Zk3jQ9xLm2vBqW8e\"":                     "Zk3jQ9xLm2vBqW8e",
	} {
		taken(t, in, []string{secret}, nil)
	}
}

// What is left alone: reading the environment by name, and calls and slices of plain names.
func TestEnvironmentReadsAndPlainCodeUnderPassAndKeyNamesAreLeftAlone(t *testing.T) {
	for _, in := range []string{
		`password = os.getenv("DB_PASSWORD")`, `password = os.Getenv("X") # from the vault`, `token = os.environ.get("TOKEN"),`, `app_pass = env.get("APP_PASS");`,
		"tripleDESKey = append(tripleDESKey, ede2Key[:16]...)", "clientKey = keyMaterial[:keyLen]", "binderKey = earlySecret.ResumptionBinderKey()",
		`secret = ENV.fetch("SECRET")`, "db_pass = <%= ENV['DB_PASS'] %>",
	} {
		untouched(t, in)
	}
}

// The two that are not taken, and why: a value of 5 characters under a name that ends in key
// is too like an identifier or an index (key = x[0]) to take without marking code.
func TestShortValuesUnderKeyNamesAreNotTaken(t *testing.T) {
	for _, in := range []string{"ssh_key=a.b.c", "key = x[0]x"} {
		got, _ := NewScrubber().Scrub(in)
		if strings.Contains(got, "REDACTED") {
			t.Errorf("Scrub(%q) = %q: the length rule was changed, and the page that lists what is missed has to change too", in, got)
		}
	}
}

// A key that is wrapped over two lines takes its second line whatever the length of the
// first: a first line of any length under 32 characters still leaves a second line to take.
func TestTheSecondLineOfATwoLineWrappedKeyIsTakenWhateverTheFirstLinesLength(t *testing.T) {
	for _, in := range []string{
		"password: lYVFnKNDDv2hVCayEm7CoiRNI4GjP\nHFEamYjt\n",
		"token = WAK33rFJWxUGwMw5uMREjuPymiv\nohtyyGl\n",
		"value=twdut6DdOhnspe19klQpWYhL3l9\nwG59OiK\n",
	} {
		lines := strings.Split(strings.TrimSpace(in), "\n")
		got, _ := NewScrubber().Scrub(in)
		if strings.Contains(got, lines[1]) {
			t.Errorf("Scrub(%q) = %q, kept the tail of the key", in, got)
		}
	}
}

// A comment after an exact environment read that holds a chosen-looking word is taken under
// a bare key or pass name too, not only under password and api_key names.
func TestACommentAfterAnEnvironmentReadIsTakenUnderBareKeyAndPassNames(t *testing.T) {
	for _, in := range []string{
		`key = os.getenv("X") // Hunter2Hunter2`,
		`pass: os.getenv("X") # Hunter2Hunter2`,
		`db_pass = os.Getenv("X") # Hunter2Hunter2`,
		`signing_key = os.Getenv("X") + "Hunter2Hunter2"`,
	} {
		taken(t, in, []string{"Hunter2Hunter2"}, nil)
	}
	for _, in := range []string{`key = os.getenv("X") // from the vault`, `pass: os.getenv("X")`} {
		untouched(t, in)
	}
}

// A value that ends in a backslash on the last line has no next line to be wrapped on to,
// and is taken as an ordinary value; with a line after it the wrapped rules decide.
func TestATrailingBackslashOnTheLastLineIsAnOrdinaryValue(t *testing.T) {
	bs := string(rune(92))
	for _, in := range []string{"key=Hunter2abcdefgh" + bs, "key=Hunter2abcdefgh" + bs + "\n", "pass: Hunter2abcdefgh" + bs + "  \n"} {
		taken(t, in, []string{"Hunter2abcdefgh"}, nil)
	}
	// with a line after it the wrapped-value rules decide, as before
	untouched(t, "key=Hunter2abcdefgh"+bs+"\nnext line")
}

// The least lengths: a value under a name that ends in key needs 12 characters, under pass,
// pwd and pw 8. One short of that is kept, and the length itself is taken.
func TestTheLeastLengthsUnderKeyAndPassNamesAreTested(t *testing.T) {
	untouched(t, "signing_key=Ab3dEf6hIj9")
	taken(t, "signing_key=Ab3dEf6hIj9K", []string{"Ab3dEf6hIj9K"}, nil)
	untouched(t, "db_pass=Ab3dEf6")
	taken(t, "db_pass=Ab3dEf6h", []string{"Ab3dEf6h"}, nil)
}

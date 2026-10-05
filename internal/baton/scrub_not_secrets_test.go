package baton

import "testing"

const pw13 = "Zk3jQ9xLm2vBqW8e"

// What reads the environment is not a secret only when it is exactly that.
func TestOnlyAnExactCallThatReadsTheEnvironmentIsAPlaceholder(t *testing.T) {
	for _, in := range []string{
		`password = os.Getenv("DB_PASSWORD")`, `password: process.env.DB_PASSWORD`, `password = ENV['DB_PASSWORD']`,
		`password: <%= ENV['DB_PASSWORD'] %>`, `secret: <%= ENV.fetch("SECRET") %>`,
	} {
		untouched(t, in)
	}
	taken(t, `password = os.Getenv("X")`+pw13, []string{pw13}, nil)
	taken(t, `password: <%= "`+pw13+`" %>`, []string{pw13}, []string{"password:"})
	taken(t, `password: <%= ENV['X'] %>`+pw13, []string{pw13}, nil)
	taken(t, `secret = ENV.fetch("SECRET", "`+pw13+`")`, []string{pw13}, nil)
}

// A mask is eight or more of one masking character. An x is not one.
func TestOnlyAMaskOfEightOrMoreMaskingCharactersIsLeftAlone(t *testing.T) {
	for _, in := range []string{"secret: ********", "password=********", "password: ••••••••", "token=################"} {
		untouched(t, in)
	}
	for _, in := range []string{"password=xxxxxxxx", "secret: xxxxxxxx", "password: XXXXXXXXXX", "password=****", "secret: *******"} {
		got, _ := NewScrubber().Scrub(in)
		if got == in {
			t.Errorf("Scrub(%q) left it as it was", in)
		}
	}
}

// A time as a number under a name for a time is not a secret; under any other name it is.
func TestEpochNumbersUnderTimeNamesAreKept(t *testing.T) {
	for _, in := range []string{"token_expires_at: 1712345678", "session_expires=1712345678123", "refresh_token_expires_at=1712345678", "token_created_at: 1712345678"} {
		untouched(t, in)
	}
	taken(t, "api_token: 1712345678", []string{"1712345678"}, nil)
}

// A line of amounts is not taken for the value of its first name.
func TestSeveralAmountsOnOneLineAreKept(t *testing.T) {
	for _, in := range []string{"input_tokens=1200 output_tokens=340", "tokens: input_tokens=1200 output_tokens=340 cache_tokens=5", "num_tokens=12 max_tokens=4096"} {
		untouched(t, in)
	}
	taken(t, "api_token=1200 output_tokens=340", []string{"1200"}, nil)
}

// Names that end in pass, pwd, pw or key with a random-looking value.
func TestNamesEndingInPassPwdPwOrKeyWithRandomValuesAreTaken(t *testing.T) {
	for _, in := range []string{
		"db_pass=" + pw13, "DBPW=" + pw13, "userpass: " + pw13, "signing_key=" + pw13, "redis_pwd = " + pw13, "ssh_key: " + pw13,
		`{"app_pw": "` + pw13 + `"}`, "mysql_pass: '" + pw13 + "'",
	} {
		taken(t, in, []string{pw13}, nil)
	}
	for _, in := range []string{
		"cache_key=user-profile-page", "public_key: " + pw13, "primary_key: id", "sort_key=createdAtIndex", "pass=true", "key=value", "ssh_key: ~/.ssh/deploy",
		"KEY=C:/Users/x/AppData/8940aecf-4631",
	} {
		untouched(t, in)
	}
}

// setx and export with a lower case name.
func TestSetxAndExportWithALowerCaseName(t *testing.T) {
	taken(t, "setx db_password "+pw13, []string{pw13}, []string{"db_password"})
	taken(t, "export db_password "+pw13, []string{pw13}, nil)
	untouched(t, "export db_password hello")
	untouched(t, "export api_token abc")
}

// Tools that take a secret as an argument.
func TestMoreCommandsThatTakeASecretOnTheLine(t *testing.T) {
	for _, in := range []string{
		"vault login " + pw13,
		"az login -u bob -p " + pw13,
		"az login --service-principal --password=" + pw13,
		"doctl auth init -t " + pw13,
		"doctl --access-token " + pw13 + " compute droplet list",
		"unzip -P " + pw13 + " a.zip",
		"zip -P " + pw13 + " a.zip f",
		"gpg --batch --passphrase " + pw13 + " -d a.gpg",
		"gpg --passphrase='" + pw13 + "' -d a.gpg",
		"7z a -p" + pw13 + " x.7z f",
		"aws configure set aws_secret_access_key " + pw13 + pw13,
		"aws configure set --profile ci aws_session_token " + pw13 + pw13,
		"http -a bob:" + pw13 + " GET example.com",
		"https --auth bob:" + pw13 + " example.com",
		"echo " + pw13 + " | docker login -u bob --password-stdin reg.example.com",
		`echo "` + pw13 + `" | docker login --password-stdin -u bob`,
		"printf '%s' " + pw13 + " | podman login --password-stdin -u bob",
		"docker login -u bob --password-stdin reg.example.com <<< " + pw13,
	} {
		taken(t, in, []string{pw13}, nil)
	}
	taken(t, "http -a bob:"+pw13+" GET example.com", nil, []string{"bob:", "GET example.com"})
	taken(t, "unzip -P "+pw13+" a.zip", nil, []string{"a.zip"})
	for _, in := range []string{
		"vault login -method=userpass username=bob", "vault login -", "az login", "az login -u bob", "doctl compute droplet list",
		"unzip a.zip", "gpg -d a.gpg", "gpg --passphrase-file pw.txt -d a.gpg", "7z x x.7z", "7z x -p x.7z",
		"aws configure set region eu-west-1", "aws configure set aws_access_key_id", "http GET example.com",
		"cat pw.txt | docker login --password-stdin -u bob", `echo $REGISTRY_PW | docker login --password-stdin -u bob`,
		"echo hello | docker login -u bob", "docker login", "vault login $VAULT_TOKEN", "unzip -P $ZIP_PW a.zip",
	} {
		untouched(t, in)
	}
}

// Code that is assigned to a name that ends in key, pass or pw is not a value.
func TestCodeAssignedToAKeyNameIsNotAValue(t *testing.T) {
	for _, in := range []string{
		"tripleDESKey = append(tripleDESKey, ede2Key[:16]...)",
		"binderKey = earlySecret.ResumptionBinderKey()", "clientKey = keyMaterial[:keyLen]",
	} {
		untouched(t, in)
	}
}

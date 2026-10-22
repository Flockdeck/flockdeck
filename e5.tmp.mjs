import fs from 'node:fs';
let f='.github/workflows/test.yml', s=fs.readFileSync(f,'utf8');
const a=`        run: go test -count=1 -timeout 20m -run '^TestPurgeScript' ./cmd/release
`;
if(!s.includes(a)) throw 1;
s=s.replace(a,()=>`        # -v and the list after it show where the 8 to 12 minutes go, test by
        # test, in the log and in the job summary.
        run: |
          set -o pipefail
          go test -v -count=1 -timeout 20m -run '^TestPurgeScript' ./cmd/release 2>&1 | tee "$RUNNER_TEMP/purge-test.log"
          status=$?
          {
            echo '### TestPurgeScript timings (seconds)'
            echo '\`\`\`'
            grep -E -- '^ *--- (PASS|FAIL|SKIP)' "$RUNNER_TEMP/purge-test.log" | sed -E 's/^ *--- ([A-Z]+): (.*) \(([0-9.]+)s\)$/\3 \1 \2/' | sort -rn
            echo '\`\`\`'
          } >> "$GITHUB_STEP_SUMMARY"
          exit $status
`);
fs.writeFileSync(f,s);
f='docs/testing.md'; s=fs.readFileSync(f,'utf8');
s=s.trimEnd()+`

## What a pull request runs

The test workflow (\`.github/workflows/test.yml\`) runs the whole suite on Linux,
Windows and macOS for each pull request and each push to main, with the race
detector on Linux, and the purge script's tests in a job of their own.

A pull request that changes only documentation runs less. That means every
changed file is a \`.md\` or \`.json\` file under \`docs/\`, or a \`.md\` file at the
repository root. A rename counts as a delete and an add, so both paths must
qualify. Then:

- \`test (ubuntu-latest)\`, \`test (windows-latest)\` and \`test (macos-latest)\`
  still report, because the branch protection requires them, but their steps
  are skipped except on Ubuntu, which runs the tests that read documentation
  (help, record, the ACP manifest, docgen, sitegen, deploydrift, the notices
  test in \`cmd/release\`, and the schema and graph page tests in the server and
  the workspace). A failure there is reported under \`test (ubuntu-latest)\`.
- The purge script's job is skipped.

To get the full run for such a pull request, change one file that is not
documentation in it (a comment in \`.github/workflows/test.yml\` will do). A push
to main and a release always run everything, so a test that reads a document
from a package the Ubuntu step does not cover is first run there. If the list of
changed files cannot be read, everything runs.
`;
fs.writeFileSync(f,s);

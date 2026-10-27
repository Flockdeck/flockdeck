// The ESLint rule that keeps the web interface inside ui/. It is the second lock
// beside the import check in importcheck.go: that one reads the files in a test, this
// one shows the error in an editor.
//
// The interface is plain ES modules loaded by the browser with no bundler, so the
// only thing to forbid is an import that leaves ui/. Inside ui/ the code is in
// src/ (the page) and test/ (the node tests); a file in either may import the
// other within ui/, and the node tests may read api/fixtures/ for the recorded
// messages. Nothing may import the server, a Go file or anything above ui/.
//
// Not used until step 3 of the separation plan has moved the interface into ui/.
// The paths below are the ones after the move.
export default [
  {
    files: ["ui/src/**/*.js", "ui/src/**/*.mjs", "ui/test/**/*.mjs"],
    languageOptions: { ecmaVersion: 2023, sourceType: "module" },
    rules: {
      "no-restricted-imports": [
        "error",
        {
          patterns: [
            {
              // api/ is reached only for the recorded messages in api/fixtures/. How far
              // up a path climbs depends on the file, so the check in importcheck.go reads that.
              regex: "(\\.\\./)+api/(?!fixtures/)",
              message: "The interface may read api/fixtures/ and nothing else outside ui/. Ask the API for the rest.",
            },
            {
              regex: "(^|/)(internal|cmd)/",
              message: "The interface may not import the server.",
            },
            {
              regex: "\\.go$",
              message: "The interface may not import Go.",
            },
          ],
        },
      ],
    },
  },
];

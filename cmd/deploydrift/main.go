// Command deploydrift reports repos whose main branch has shipping changes no
// version tag has picked up yet, and keeps one tracking issue in this repo in
// step with that.
//
// For flockdeck-relay, -billing, -site and -docs, merging to main deploys
// nothing: a v* tag builds the image and Flux rolls out the new pin.
// flockdeck-remote is embedded in the relay, so a remote change reaches users
// only through a remote tag, a relay go.mod bump and a relay tag. Merged work
// was being read as shipped; this makes the gap visible.
//
// Usage (the workflow runs this daily):
//
//	DEPLOY_DRIFT_TOKEN=... GITHUB_TOKEN=... GITHUB_REPOSITORY=Flockdeck/flockdeck \
//	    go run ./cmd/deploydrift -threshold-hours 24
//	DEPLOY_DRIFT_TOKEN=... go run ./cmd/deploydrift -dry-run    print, change nothing
//
// DEPLOY_DRIFT_TOKEN reads the other repos (Contents: read). GITHUB_TOKEN
// writes the one issue. Missing DEPLOY_DRIFT_TOKEN is an error, never a pass.
// Any repo that cannot be read aborts the run before the issue is touched, so
// partial data can never close it.
package main

import (
	"context"
	"flag"
	"fmt"
	"math"
	"os"
	"strconv"
	"time"
)

func main() {
	if err := run(context.Background(), os.Args[1:], os.Getenv, time.Now(), os.Stdout); err != nil {
		fmt.Fprintln(os.Stderr, "deploydrift:", err)
		os.Exit(1)
	}
}

func run(ctx context.Context, args []string, getenv func(string) string, now time.Time, out interface{ Write([]byte) (int, error) }) error {
	fs := flag.NewFlagSet("deploydrift", flag.ContinueOnError)
	thresholdHours := fs.String("threshold-hours", "24", "hours the oldest untagged shipping commit may reach before its repo is flagged")
	dryRun := fs.Bool("dry-run", false, "print the picture and the issue body, change nothing")
	if err := fs.Parse(args); err != nil {
		return err
	}
	hours, err := strconv.ParseFloat(*thresholdHours, 64)
	if err != nil || math.IsNaN(hours) || math.IsInf(hours, 0) || hours < 0 {
		return fmt.Errorf("-threshold-hours %q is not a non-negative number", *thresholdHours)
	}
	threshold := time.Duration(hours * float64(time.Hour))

	readToken := getenv("DEPLOY_DRIFT_TOKEN")
	if readToken == "" {
		return fmt.Errorf("DEPLOY_DRIFT_TOKEN is not set. Create a fine-grained token (resource owner Flockdeck; repositories flockdeck, flockdeck-relay, flockdeck-billing, flockdeck-site, flockdeck-docs, flockdeck-remote; permission Contents: read-only, nothing else) and store it as the DEPLOY_DRIFT_TOKEN secret on Flockdeck/flockdeck. Without it the private repos cannot be read, so no drift check is possible")
	}
	api := getenv("GITHUB_API_URL")
	if api == "" {
		api = "https://api.github.com"
	}

	pic, err := analyse(ctx, newClient(api, readToken), now, threshold)
	if err != nil {
		return err
	}
	body := renderBody(pic)
	fmt.Fprintln(out, body)
	if path := getenv("GITHUB_STEP_SUMMARY"); path != "" {
		if f, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY|os.O_CREATE, 0o644); err == nil {
			fmt.Fprintln(f, body)
			f.Close()
		}
	}
	if *dryRun {
		fmt.Fprintln(out, "dry run: issue not touched")
		return nil
	}

	self, writeToken := getenv("GITHUB_REPOSITORY"), getenv("GITHUB_TOKEN")
	if self == "" || writeToken == "" {
		return fmt.Errorf("GITHUB_REPOSITORY and GITHUB_TOKEN are needed to update the tracking issue (the workflow provides both; use -dry-run locally)")
	}
	res, err := syncIssue(ctx, newClient(api, writeToken), self, pic)
	if err != nil {
		return fmt.Errorf("updating the tracking issue: %w", err)
	}
	fmt.Fprintf(out, "tracking issue: %s (commented: %v)\n", res.Action, res.Commented)
	return nil
}

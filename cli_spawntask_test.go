package main

import "testing"

func TestParseSpawnTakesATaskInPlaceOfText(t *testing.T) {
	req, err := parseSpawn([]string{"-task", "T5"})
	if err != nil {
		t.Fatal(err)
	}
	if req.TaskID != "T5" || req.Task != "" {
		t.Errorf("got task id %q and text %q", req.TaskID, req.Task)
	}
	if _, err := parseSpawn([]string{"-shell", "-task", "T5"}); err == nil {
		t.Error("a shell was given a task")
	}
}

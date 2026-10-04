//go:build linux

package helpers

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

const ownerCheckSupported = true

// procRoot is where /proc is, a variable so a test can point it at a tree it
// made.
var procRoot = "/proc"

// platformListenerOwner finds the sockets listening on the port in
// /proc/net/tcp and tcp6, and checks that each is held open by a process in the
// helper's group, by matching the socket's inode against the group's file
// descriptors.
func platformListenerOwner(port int, group []int) (ownerResult, string) {
	var inodes []uint64
	read := false
	for _, name := range []string{"tcp", "tcp6"} {
		b, err := os.ReadFile(filepath.Join(procRoot, "net", name))
		if err != nil {
			continue
		}
		read = true
		inodes = append(inodes, parseProcNetTCP(string(b), port)...)
	}
	if !read {
		return ownerUnknown, "/proc/net/tcp could not be read"
	}
	if len(inodes) == 0 {
		return ownerUnknown, "no listener on the port was found"
	}
	held := map[uint64]bool{}
	for _, pid := range group {
		dir := filepath.Join(procRoot, strconv.Itoa(pid), "fd")
		entries, err := os.ReadDir(dir)
		if err != nil {
			continue
		}
		for _, e := range entries {
			target, err := os.Readlink(filepath.Join(dir, e.Name()))
			if err != nil || !strings.HasPrefix(target, "socket:[") {
				continue
			}
			if n, err := strconv.ParseUint(strings.TrimSuffix(strings.TrimPrefix(target, "socket:["), "]"), 10, 64); err == nil {
				held[n] = true
			}
		}
	}
	for _, in := range inodes {
		if !held[in] {
			return ownerMismatch, "a socket on the port is not held by the helper"
		}
	}
	return ownerVerified, ""
}

// groupMembers lists the processes in a process group, from /proc/*/stat.
func groupMembers(pgid int) []int {
	entries, err := os.ReadDir(procRoot)
	if err != nil {
		return []int{pgid}
	}
	out := []int{pgid}
	for _, e := range entries {
		pid, err := strconv.Atoi(e.Name())
		if err != nil || pid == pgid {
			continue
		}
		b, err := os.ReadFile(filepath.Join(procRoot, e.Name(), "stat"))
		if err != nil {
			continue
		}
		if g, ok := parseStatPgrp(string(b)); ok && g == pgid {
			out = append(out, pid)
		}
	}
	return out
}

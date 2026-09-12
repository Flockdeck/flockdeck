package main

import "syscall"

// dupOnto makes newfd refer to what oldfd does.
func dupOnto(oldfd, newfd int) error { return syscall.Dup2(oldfd, newfd) }

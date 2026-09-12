package main

import "syscall"

// dupOnto makes newfd refer to what oldfd does. Linux on arm64 has only dup3.
func dupOnto(oldfd, newfd int) error { return syscall.Dup3(oldfd, newfd, 0) }

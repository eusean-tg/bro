package main

import "errors"

var ErrNoWorkspace = errors.New("no workspace specified")

type Config struct {
	Identity  string
	Workspace string
}

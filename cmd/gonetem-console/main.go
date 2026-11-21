package main

import (
	"github.com/satcomprojects/gonetem/internal/console"
	"github.com/satcomprojects/gonetem/internal/options"
)

func main() {
	options.InitConsoleConfig()
	console.Init()
	console.Execute()
}

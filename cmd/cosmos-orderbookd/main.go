package main

import "os"

func main() {
	home, err := defaultHome()
	if err != nil {
		os.Exit(1)
	}
	if err := Execute(home); err != nil {
		os.Exit(1)
	}
}

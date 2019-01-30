package main

import "runtime"

func main() {
	runtime.GOMAXPROCS(runtime.NumCPU())

	cli := CLI{}
	cli.Run()
}

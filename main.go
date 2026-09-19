package main

import (
	"flag"
	"fmt"
	"os"
)

func main() {
	showVersion := flag.Bool("version", false, "print version")
	showHelp := flag.Bool("help", false, "show help")
	flag.Parse()
	if *showVersion {
		fmt.Println("lzpody dev (Go)")
		return
	}
	if *showHelp {
		fmt.Println("lzpody - native Podman Libpod TUI\n\nUsage: lzpody [--version|--help]\n\nEnvironment: LZPODY_SOCKET, LZPODY_API_VERSION")
		return
	}
	if err := runBubbleTUI(NewPodmanClient("")); err != nil {
		fmt.Fprintln(os.Stderr, "lzpody:", err)
		os.Exit(1)
	}
}

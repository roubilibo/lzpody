package main

import (
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"time"
)

var version = "dev"

func main() {
	showVersion := flag.Bool("version", false, "print version")
	showHelp := flag.Bool("help", false, "show help")
	flag.Parse()
	if *showVersion {
		fmt.Println("lzpody " + version)
		return
	}
	if *showHelp {
		fmt.Println("lzpody - native Podman Libpod TUI\n\nUsage: lzpody [--version|--help]\n\nEnvironment: LZPODY_SOCKET, LZPODY_URL, LZPODY_API_VERSION")
		return
	}
	config, _ := loadUserConfig()
	socketPath := ""
	if os.Getenv("LZPODY_SOCKET") == "" && os.Getenv("PODMAN_TUI_SOCKET") == "" {
		socketPath = config.SocketPath
		if config.EndpointURL != "" {
			socketPath = config.EndpointURL
		}
	}
	client := NewPodmanClient(socketPath)
	// Starting the user socket is best-effort: systems without systemd, custom
	// sockets, and remote setups should still reach the normal TUI error state.
	_ = ensurePodmanSocket(client)
	if err := runBubbleTUI(client); err != nil {
		fmt.Fprintln(os.Stderr, "lzpody:", err)
		os.Exit(1)
	}
}

func ensurePodmanSocket(client *PodmanClient) error {
	if client == nil || client.SocketPath == "" {
		return nil
	}
	if client.BaseURL != "" {
		return nil
	}
	if _, err := os.Stat(client.SocketPath); err == nil {
		return nil
	}
	if os.Getenv("LZPODY_SOCKET") != "" || os.Getenv("PODMAN_TUI_SOCKET") != "" {
		return nil
	}

	command := exec.Command("systemctl", "--user", "start", "podman.socket")
	command.Stdout = io.Discard
	command.Stderr = io.Discard
	if err := command.Run(); err != nil {
		return fmt.Errorf("start podman socket: %w", err)
	}
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if _, err := os.Stat(client.SocketPath); err == nil {
			return nil
		}
		time.Sleep(50 * time.Millisecond)
	}
	return fmt.Errorf("podman socket did not appear at %s", client.SocketPath)
}

package main

import (
	"fmt"
	"os"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/jim/dockertool/internal/docker"
	"github.com/jim/dockertool/internal/ui"
)

func main() {
	dc, err := docker.NewClient()
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error connecting to Docker: %v\n", err)
		fmt.Fprintf(os.Stderr, "Make sure Docker Desktop (or Docker daemon) is running.\n")
		os.Exit(1)
	}
	defer dc.Close()

	app := ui.NewApp(dc)
	p := tea.NewProgram(app, tea.WithAltScreen())
	if _, err := p.Run(); err != nil {
		fmt.Fprintf(os.Stderr, "Fatal error: %v\n", err)
		os.Exit(1)
	}
}

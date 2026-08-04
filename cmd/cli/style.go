package main

import (
	"os"

	"github.com/charmbracelet/lipgloss"

	"github.com/r3dpan/project-descendence/internal/client"
)

// Shared lipgloss styles. Colours are adaptive so the CLI is readable on
// both light and dark terminals, and lipgloss degrades them automatically
// when the terminal can't do colour at all.
var (
	styleLabel = lipgloss.NewStyle().Foreground(lipgloss.AdaptiveColor{Light: "241", Dark: "245"})
	styleValue = lipgloss.NewStyle().Foreground(lipgloss.AdaptiveColor{Light: "236", Dark: "252"})
	styleBold  = lipgloss.NewStyle().Bold(true)
	styleError = lipgloss.NewStyle().Foreground(lipgloss.AdaptiveColor{Light: "160", Dark: "203"}).Bold(true)
	styleHint  = lipgloss.NewStyle().Foreground(lipgloss.AdaptiveColor{Light: "245", Dark: "240"}).Italic(true)
)

// stateStyle returns the style for a run state - the one piece of colour
// that actually carries meaning here, so it is the only thing that gets a
// strong colour.
func stateStyle(state string) lipgloss.Style {
	base := lipgloss.NewStyle().Bold(true)

	switch state {
	case client.StateQueued:
		return base.Foreground(lipgloss.AdaptiveColor{Light: "136", Dark: "179"})
	case client.StateRunning:
		return base.Foreground(lipgloss.AdaptiveColor{Light: "26", Dark: "75"})
	case client.StateSucceeded:
		return base.Foreground(lipgloss.AdaptiveColor{Light: "28", Dark: "78"})
	case client.StateFailed:
		return base.Foreground(lipgloss.AdaptiveColor{Light: "160", Dark: "203"})
	case client.StateCancelled:
		return base.Foreground(lipgloss.AdaptiveColor{Light: "94", Dark: "180"})
	case client.StateLost:
		return base.Foreground(lipgloss.AdaptiveColor{Light: "90", Dark: "176"})
	default:
		return base
	}
}

// isTTY reports whether f is an interactive terminal. Used to decide
// between the bubbletea view and plain line output - a CLI piped into
// another program must not emit spinners and cursor movement.
func isTTY(f *os.File) bool {
	info, err := f.Stat()
	return err == nil && info.Mode()&os.ModeCharDevice != 0
}

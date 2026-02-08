package tui

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/lipgloss"
	"github.com/stewartpark/private-llm/cli/tui/assets"
)

func renderOperation(m OperationModel) string {
	w := m.Width
	if w <= 0 {
		w = 80
	}
	h := m.Height
	if h <= 0 {
		h = 24
	}

	iw := w - 6
	if iw < 40 {
		iw = 40
	}

	var top []string

	// Logo + title (same pattern as serve dashboard)
	logo := assets.GetDashFrame(0)
	logoLines := strings.Split(logo, "\n")
	logoStyle := lipgloss.NewStyle().Foreground(colorCyan)

	ver := lipgloss.NewStyle().Foreground(colorGray).Render("v0.0.1")
	titleLine := lipgloss.NewStyle().Bold(true).Foreground(colorCyan).Render("Private LLM") + " " + ver

	infoLines := []string{titleLine}

	logoWidth := 0
	for _, l := range logoLines {
		if lw := lipgloss.Width(l); lw > logoWidth {
			logoWidth = lw
		}
	}
	gap := 3
	maxLines := len(logoLines)
	if len(infoLines) > maxLines {
		maxLines = len(infoLines)
	}
	for i := range maxLines {
		left := ""
		if i < len(logoLines) {
			left = logoStyle.Render(logoLines[i])
		}
		leftWidth := lipgloss.Width(left)
		padding := logoWidth + gap - leftWidth
		if padding < 1 {
			padding = 1
		}
		right := ""
		if i < len(infoLines) {
			right = infoLines[i]
		}
		top = append(top, left+strings.Repeat(" ", padding)+right)
	}

	top = append(top, divider(iw))

	// Status section — varies by phase
	top = append(top, renderOpStatus(m, iw)...)
	top = append(top, "")
	top = append(top, divider(iw))

	// Calculate log area height
	// chrome: border(2) + padding(2) + footer(2) = 6
	topLines := len(top)
	logHeaderLines := 1
	errLines := 0
	if m.ErrorMessage != "" {
		errLines = 1
	}
	availLogLines := h - 6 - topLines - logHeaderLines - errLines
	if availLogLines < 1 {
		availLogLines = 1
	}

	// Logs section (fixed height, scrollable)
	var logSection []string

	// Header with scroll indicator
	logHeaderText := "Logs"
	if m.LogScrollBack > 0 {
		logHeaderText += dimText(fmt.Sprintf(" (scrolled +%d)", m.LogScrollBack))
	}
	logHeader := lipgloss.NewStyle().Foreground(colorWhite).Bold(true).Render(logHeaderText)
	logSection = append(logSection, logHeader)

	logStyle := lipgloss.NewStyle().Foreground(colorGray)
	// endIdx is the last line to show (exclusive); scrollBack shifts the window up
	endIdx := len(m.LogLines) - m.LogScrollBack
	if endIdx < 0 {
		endIdx = 0
	}
	startIdx := endIdx - availLogLines
	if startIdx < 0 {
		startIdx = 0
	}
	visibleLogs := m.LogLines[startIdx:endIdx]
	for _, line := range visibleLogs {
		logSection = append(logSection, logStyle.Render("  "+truncate(line, iw-2)))
	}
	// Pad to exact height so layout is stable
	for i := len(visibleLogs); i < availLogLines; i++ {
		logSection = append(logSection, "")
	}

	// Assemble content
	var all []string
	all = append(all, top...)
	all = append(all, logSection...)
	if m.ErrorMessage != "" {
		all = append(all, lipgloss.NewStyle().Foreground(colorRed).Render("  Error: "+truncate(m.ErrorMessage, iw-8)))
	}

	content := lipgloss.JoinVertical(lipgloss.Left, all...)

	box := lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(colorGray).
		Padding(1, 2).
		Width(w - 2).
		MaxHeight(h - 2). // hard clamp: leave room for footer
		Render(content)

	// Footer
	footer := renderOpFooter(m)

	return box + "\n" + footer
}

func renderOpStatus(m OperationModel, _ int) []string {
	var lines []string

	switch m.Phase {
	case OpPhaseInit, OpPhasePreview, OpPhaseApply, OpPhaseDestroy, OpPhaseCerts:
		// Spinner + step label
		spinLine := lipgloss.NewStyle().Foreground(colorCyan).Render(m.Spinner.View()) +
			" " + lipgloss.NewStyle().Foreground(colorWhite).Render(m.StepLabel)
		lines = append(lines, spinLine)

	case OpPhaseConfirm:
		// Show summary + prompt
		if m.Summary != nil {
			var parts []string
			if n := m.Summary["create"]; n > 0 {
				parts = append(parts, lipgloss.NewStyle().Foreground(colorGreen).Render(fmt.Sprintf("%d create", n)))
			}
			if n := m.Summary["update"]; n > 0 {
				parts = append(parts, lipgloss.NewStyle().Foreground(colorYellow).Render(fmt.Sprintf("%d update", n)))
			}
			if n := m.Summary["delete"]; n > 0 {
				parts = append(parts, lipgloss.NewStyle().Foreground(colorRed).Render(fmt.Sprintf("%d delete", n)))
			}
			if n := m.Summary["same"]; n > 0 {
				parts = append(parts, dimText(fmt.Sprintf("%d unchanged", n)))
			}
			if len(parts) > 0 {
				lines = append(lines, "  "+strings.Join(parts, "  "))
			}
		}
		promptLabel := "Apply changes?"
		if m.Kind == OpKindDown {
			promptLabel = "Destroy all infrastructure?"
		}
		prompt := lipgloss.NewStyle().Bold(true).Foreground(colorYellow).Render(promptLabel) +
			"  " + dimText("(y/n)")
		lines = append(lines, prompt)

	case OpPhaseDone:
		if m.Cancelled {
			lines = append(lines, lipgloss.NewStyle().Foreground(colorYellow).Render("  Cancelled."))
		} else if m.ErrorMessage != "" {
			lines = append(lines, lipgloss.NewStyle().Foreground(colorRed).Render("  Failed."))
		} else {
			doneLabel := "Infrastructure provisioned."
			if m.Kind == OpKindDown {
				doneLabel = "Infrastructure destroyed."
			}
			lines = append(lines, lipgloss.NewStyle().Foreground(colorGreen).Render("  "+doneLabel))
		}
	}

	return lines
}

func renderOpFooter(m OperationModel) string {
	keyStyle := lipgloss.NewStyle().Foreground(colorWhite).Bold(true)
	descStyle := lipgloss.NewStyle().Foreground(colorGray)
	sep := descStyle.Render("  ")

	shortcuts := keyStyle.Render("q") + descStyle.Render(" quit")
	if m.Phase == OpPhaseConfirm {
		shortcuts += sep +
			keyStyle.Render("y") + descStyle.Render(" apply") + sep +
			keyStyle.Render("n") + descStyle.Render(" cancel")
	}
	shortcuts += sep + keyStyle.Render("↑↓") + descStyle.Render(" scroll")

	return lipgloss.NewStyle().PaddingLeft(2).Render(shortcuts)
}

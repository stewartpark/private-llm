package tui

import (
	"fmt"
	"strings"
	"time"

	"github.com/charmbracelet/lipgloss"
	"github.com/stewartpark/private-llm/cli/tui/assets"
)

// Color palette.
var (
	colorGreen  = lipgloss.Color("#4ade80")
	colorYellow = lipgloss.Color("#facc15")
	colorRed    = lipgloss.Color("#f87171")
	colorCyan   = lipgloss.Color("#22d3ee")
	colorBlue   = lipgloss.Color("#60a5fa")
	colorWhite  = lipgloss.Color("#e5e7eb")
	colorGray   = lipgloss.Color("#6b7280")
	colorDim    = lipgloss.Color("#374151")
)

func resolveColor(name string) lipgloss.Color {
	switch name {
	case "green":
		return colorGreen
	case "yellow":
		return colorYellow
	case "red":
		return colorRed
	case "cyan":
		return colorCyan
	case "blue":
		return colorBlue
	case "white":
		return colorWhite
	default:
		return colorGray
	}
}

// Helpers for key-value rendering.
func kv(k, v string, vc lipgloss.Color) string {
	return lipgloss.NewStyle().Foreground(colorGray).Render(k+" ") +
		lipgloss.NewStyle().Foreground(vc).Render(v)
}

func kvBold(k, v string, vc lipgloss.Color) string {
	return lipgloss.NewStyle().Foreground(colorGray).Render(k+" ") +
		lipgloss.NewStyle().Foreground(vc).Bold(true).Render(v)
}

func twoCol(left, right string, width int) string {
	lw := lipgloss.Width(left)
	rw := lipgloss.Width(right)
	gap := width - lw - rw
	if gap < 2 {
		gap = 2
	}
	return left + strings.Repeat(" ", gap) + right
}

func divider(w int) string {
	return lipgloss.NewStyle().Foreground(colorDim).Render(strings.Repeat("─", w))
}

func dimText(s string) string {
	return lipgloss.NewStyle().Foreground(colorGray).Render(s)
}

func orDash(s string) string {
	if s == "" {
		return "—"
	}
	return s
}

// ── Dashboard ────────────────────────────────────────────────────

func renderDashboard(m Model) string {
	w := m.Width
	if w <= 0 {
		w = 80
	}
	h := m.Height
	if h <= 0 {
		h = 24
	}

	// Inner content width: subtract border (2) + padding (2*2)
	iw := w - 6
	if iw < 40 {
		iw = 40
	}

	// ── Logo + title ──
	var top []string

	logo := assets.GetDashFrame(m.LogoFrame)
	logoLines := strings.Split(logo, "\n")
	logoStyle := lipgloss.NewStyle().Foreground(colorCyan)
	if m.IsAnimating() {
		logoStyle = logoStyle.Bold(true)
	}

	// Status info lines to display beside the logo
	ver := lipgloss.NewStyle().Foreground(colorGray).Render("v0.0.1")
	addrLine := ""
	if m.ListenAddr != "" {
		addrLine = lipgloss.NewStyle().Foreground(colorDim).Render(m.ListenAddr)
	}
	titleLine := lipgloss.NewStyle().Bold(true).Foreground(colorCyan).Render("Private LLM") + " " + ver
	cloudLine := ""
	if m.Provider != "" {
		cloudLine = kv(m.Provider, m.MachineType, colorWhite) +
			"  " + kv("Zone", m.Zone, colorWhite)
	}
	modelLine := ""
	if m.ModelName != "" {
		ctxStr := formatContextLength(m.ContextLength)
		modelLine = kv("Model", m.ModelName+" ("+ctxStr+")", colorWhite)
	}

	// Build right-side info to sit beside logo
	infoLines := []string{titleLine, addrLine, cloudLine, modelLine}

	// Merge logo (left) and info (right) side by side
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
	for i := 0; i < maxLines; i++ {
		left := ""
		if i < len(logoLines) {
			left = logoStyle.Render(logoLines[i])
		}
		// Pad left side to consistent width
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

	// Row 1: VM status + External IP + Idle
	vmColor := resolveColor(m.Status.VMStatusColor)
	idleVal := "—"
	idleColor := colorGray
	if m.Status.IdleTime > 0 && m.Status.IdleTimeColor != "" {
		idleVal = formatDuration(m.Status.IdleTime)
		idleColor = resolveColor(m.Status.IdleTimeColor)
	}
	timeoutVal := "—"
	if m.Status.IdleTimeout > 0 {
		timeoutVal = formatDuration(m.Status.IdleTimeout)
	}
	row1 := kvBold("VM", m.Status.VMStatus, vmColor) +
		"  " + kv("IP", orDash(m.Status.ExternalIP), colorCyan) +
		"  " + kv("Idle", idleVal+" / "+timeoutVal, idleColor)
	top = append(top, row1)

	// Row 2: Encryption — mTLS status, CA key, cert & token age
	encIcon := "✓"
	encColor := colorGreen
	encLabel := "ACTIVE"
	if !m.Status.EncryptionActive {
		encIcon = "✗"
		encColor = colorRed
		encLabel = "INACTIVE"
	}
	certVal := "—"
	certColor := colorGray
	if !m.Status.CertCreated.IsZero() {
		certVal = fmt.Sprintf("%dd old", m.Status.CertAgeDays)
		certColor = resolveColor(m.Status.CertAgeColor)
	}
	tokenVal := "—"
	tokenColor := colorGray
	if !m.Status.TokenCreated.IsZero() {
		tokenVal = fmt.Sprintf("%dd old", m.Status.TokenAgeDays)
		tokenColor = resolveColor(m.Status.TokenAgeColor)
	}
	row2 := kvBold("mTLS 1.3", encIcon+" "+encLabel, encColor) +
		"  " + kv("Cert", certVal, certColor) +
		"  " + kv("Token", tokenVal, tokenColor) +
		"  " + kv("CA Key", m.Status.CAKeyLocation, colorBlue)
	top = append(top, row2)

	// Row 3: Firewall — status + source IP
	fwIcon := "✓"
	fwColor := colorGreen
	fwLabel := "OPEN"
	fwSrc := "—"
	if !m.Status.FirewallActive {
		fwIcon = "●"
		fwColor = colorRed
		fwLabel = "CLOSED"
	} else if m.Status.SourceIPRange != "" {
		fwSrc = m.Status.SourceIPRange
	}
	row3 := kvBold("Firewall", fwIcon+" "+fwLabel, fwColor) +
		"  " + kv("Source", fwSrc, colorWhite)
	top = append(top, row3)

	// Row 4: Token throughput with flash animation and tok/sec
	const tokenFlashDur = 2 * time.Second
	inFlash := !m.InputTokensChangedAt.IsZero() && time.Since(m.InputTokensChangedAt) < tokenFlashDur
	outFlash := !m.OutputTokensChangedAt.IsZero() && time.Since(m.OutputTokensChangedAt) < tokenFlashDur
	pulse := time.Now().Second()%2 == 0

	inTok := formatTokenCount(m.Status.InputTokens)
	outTok := formatTokenCount(m.Status.OutputTokens)
	totalTok := formatTokenCount(m.Status.InputTokens + m.Status.OutputTokens)

	// ● In — cyan dot, flash to bright white pulsing
	inDot := lipgloss.NewStyle().Foreground(colorCyan).Render("●")
	inValStyle := lipgloss.NewStyle().Foreground(colorCyan)
	if inFlash {
		if pulse {
			inDot = lipgloss.NewStyle().Foreground(lipgloss.Color("#ffffff")).Bold(true).Render("●")
		} else {
			inDot = lipgloss.NewStyle().Foreground(colorCyan).Render("○")
		}
		inValStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("#ffffff")).Bold(true)
	}
	inPart := inDot + lipgloss.NewStyle().Foreground(colorGray).Render(" In ") + inValStyle.Render(inTok)

	// ● Out — green dot, flash to bright white pulsing
	outDot := lipgloss.NewStyle().Foreground(colorGreen).Render("●")
	outValStyle := lipgloss.NewStyle().Foreground(colorGreen)
	if outFlash {
		if pulse {
			outDot = lipgloss.NewStyle().Foreground(lipgloss.Color("#ffffff")).Bold(true).Render("●")
		} else {
			outDot = lipgloss.NewStyle().Foreground(colorGreen).Render("○")
		}
		outValStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("#ffffff")).Bold(true)
	}
	outPart := outDot + lipgloss.NewStyle().Foreground(colorGray).Render(" Out ") + outValStyle.Render(outTok)

	// ⚡ tok/sec — live rate during streaming, last request rate otherwise
	ratePart := ""
	if m.CurrentTokPerSec > 0 {
		rateStr := formatRate(m.CurrentTokPerSec)
		if m.IsStreaming {
			ratePart = lipgloss.NewStyle().Foreground(colorYellow).Bold(true).Render("⚡ "+rateStr+" tok/s")
		} else {
			ratePart = lipgloss.NewStyle().Foreground(colorGray).Render(rateStr+" tok/s")
		}
		if m.MaxTokPerSec > 0 {
			peakStr := formatRate(m.MaxTokPerSec)
			ratePart += lipgloss.NewStyle().Foreground(colorDim).Render(" (peak "+peakStr+" tok/s)")
		}
	}

	// Σ total — right aligned
	totalPart := lipgloss.NewStyle().Foreground(colorGray).Render("Σ ") +
		lipgloss.NewStyle().Foreground(colorCyan).Bold(true).Render(totalTok)

	row4Left := inPart + "   " + outPart
	if ratePart != "" {
		row4Left += "   " + ratePart
	}
	top = append(top, twoCol(row4Left, totalPart, iw))

	top = append(top, divider(iw))

	// ── Requests (fixed: header + up to MaxRequestLog lines) ──
	maxReqLines := m.MaxRequestLog
	var reqSection []string
	reqHeader := lipgloss.NewStyle().Foreground(colorWhite).Bold(true).Render("Requests")
	reqSection = append(reqSection, reqHeader)
	if len(m.RequestLog) == 0 {
		reqSection = append(reqSection, dimText("  No requests yet"))
		// Pad to fixed height
		for i := 1; i < maxReqLines; i++ {
			reqSection = append(reqSection, "")
		}
	} else {
		for _, req := range m.RequestLog {
			icon := "●"
			ic := colorGray
			if req.Status >= 200 && req.Status < 300 {
				icon = "✓"
				ic = colorGreen
			} else if req.Status >= 400 {
				icon = "✗"
				ic = colorRed
			}
			left := lipgloss.NewStyle().Foreground(ic).Render(icon) + " " +
				lipgloss.NewStyle().Foreground(colorWhite).Render(fmt.Sprintf("%-7s %s", req.Method, req.Path))
			durStr := lipgloss.NewStyle().Foreground(colorGray).Render(
				fmt.Sprintf("%d  %s", req.Status, req.Duration.Round(time.Millisecond)))
			tokStr := ""
			if req.InputTokens > 0 || req.OutputTokens > 0 {
				tokStr = "  " +
					lipgloss.NewStyle().Foreground(colorCyan).Render("●"+formatTokenCount(req.InputTokens)) +
					" " +
					lipgloss.NewStyle().Foreground(colorGreen).Render("●"+formatTokenCount(req.OutputTokens))
				if req.OutputTokPerSec > 0 {
					tokStr += " " + lipgloss.NewStyle().Foreground(colorDim).Render(formatRate(req.OutputTokPerSec)+"t/s")
				}
			}
			reqSection = append(reqSection, twoCol("  "+left, durStr+tokStr, iw))
		}
		// Pad to fixed height
		for i := len(m.RequestLog); i < maxReqLines; i++ {
			reqSection = append(reqSection, "")
		}
	}

	reqSection = append(reqSection, "")
	reqSection = append(reqSection, divider(iw))
	reqSection = append(reqSection, "")

	// ── Error (if any, 1 line) ──
	var errLine string
	if m.ErrorMessage != "" {
		errLine = lipgloss.NewStyle().Foreground(colorRed).Render("  ⚠ "+m.ErrorMessage) + "\n"
	}

	// ── Calculate log area height ──
	// Total height budget: h
	// Used by: border(2) + padding(2) + top lines + req lines + log header(1) + footer(2) + error(0 or 1)
	topLines := len(top)
	reqLines := len(reqSection)
	errLines := 0
	if errLine != "" {
		errLines = 1
	}
	// border(2) + padding top/bottom(2) + footer(2)
	chrome := 6
	logHeaderLines := 1 // "Logs" header
	availLogLines := h - chrome - topLines - reqLines - logHeaderLines - errLines
	if availLogLines < 1 {
		availLogLines = 1
	}

	// ── Logs (fills remaining space) ──
	var logSection []string
	logHeader := lipgloss.NewStyle().Foreground(colorWhite).Bold(true).Render("Logs")
	logSection = append(logSection, logHeader)

	logStyle := lipgloss.NewStyle().Foreground(colorDim)
	startIdx := len(m.LogLines) - availLogLines
	if startIdx < 0 {
		startIdx = 0
	}
	visibleLogs := m.LogLines[startIdx:]
	for _, line := range visibleLogs {
		logSection = append(logSection, logStyle.Render("  "+truncate(line, iw-2)))
	}
	// Pad log area to fill remaining space
	for i := len(visibleLogs); i < availLogLines; i++ {
		logSection = append(logSection, "")
	}

	// ── Assemble ──
	var all []string
	all = append(all, top...)
	all = append(all, reqSection...)
	all = append(all, logSection...)
	if errLine != "" {
		all = append(all, lipgloss.NewStyle().Foreground(colorRed).Render("  ⚠ "+m.ErrorMessage))
	}

	content := lipgloss.JoinVertical(lipgloss.Left, all...)

	box := lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(colorGray).
		Padding(1, 2).
		Width(w - 2).
		Render(content)

	// Footer: shortcuts + action status
	keyStyle := lipgloss.NewStyle().Foreground(colorWhite).Bold(true)
	descStyle := lipgloss.NewStyle().Foreground(colorGray)
	sep := descStyle.Render("  ")

	vmRunning := m.Status.VMStatus == "RUNNING"

	toggleLabel := "stop VM"
	if !vmRunning {
		toggleLabel = "start VM"
	}

	shortcuts := keyStyle.Render("q") + descStyle.Render(" quit") + sep +
		keyStyle.Render("S") + descStyle.Render(" "+toggleLabel)
	if vmRunning {
		shortcuts += sep + keyStyle.Render("r") + descStyle.Render(" restart VM")
	}
	shortcuts += sep + keyStyle.Render("R") + descStyle.Render(" reset VM")

	var footer string
	if m.ActionInProgress {
		footer = lipgloss.NewStyle().PaddingLeft(2).Render(
			lipgloss.NewStyle().Foreground(colorYellow).Render(m.Spinner.View()+" "+m.ActionLabel) +
				sep + sep + dimText(shortcuts))
	} else {
		footer = lipgloss.NewStyle().PaddingLeft(2).Render(shortcuts)
	}

	return box + "\n" + footer
}

// ── Boot Screen ──────────────────────────────────────────────────

func renderBoot(m Model) string {
	w := m.Width
	if w <= 0 {
		w = 80
	}
	h := m.Height
	if h <= 0 {
		h = 24
	}

	var sb strings.Builder

	// Logo
	logo := assets.GetBootLogo()
	sb.WriteString(lipgloss.NewStyle().Foreground(colorCyan).Render(logo))
	sb.WriteString("\n\n")

	// Title
	sb.WriteString(lipgloss.NewStyle().Foreground(colorGreen).Bold(true).Render("  Private LLM Agent"))
	sb.WriteString("\n")
	sb.WriteString(lipgloss.NewStyle().Foreground(colorGray).Render("  Secure Local AI Proxy"))
	sb.WriteString("\n\n")

	// Boot steps with progress
	for i, step := range m.BootSteps {
		var line string
		if i < m.BootStep {
			line = lipgloss.NewStyle().Foreground(colorGreen).Render("  ✓ " + step)
		} else if i == m.BootStep {
			line = lipgloss.NewStyle().Foreground(colorYellow).Render("  " + m.Spinner.View() + " " + step)
		} else {
			line = lipgloss.NewStyle().Foreground(colorGray).Render("  ○ " + step)
		}
		sb.WriteString(line + "\n")
	}

	content := sb.String()

	// Center vertically (roughly 1/3 from top)
	contentLines := strings.Count(content, "\n") + 1
	topPad := (h - contentLines) / 3
	if topPad < 0 {
		topPad = 0
	}
	content = strings.Repeat("\n", topPad) + content

	// Fill remaining height
	totalLines := strings.Count(content, "\n") + 1
	if totalLines < h {
		content += strings.Repeat("\n", h-totalLines)
	}

	return content
}

// ── Spinner View ─────────────────────────────────────────────────

func renderSpinner(m Model) string {
	h := m.Height
	if h <= 0 {
		h = 24
	}

	var sb strings.Builder

	sb.WriteString(lipgloss.NewStyle().Bold(true).Foreground(colorCyan).Render("  Private LLM"))
	sb.WriteString("\n\n")

	// Progress bar
	total := len(m.BootSteps)
	if total > 0 {
		progress := float64(m.BootStep) / float64(total)
		barWidth := 40
		filled := int(progress * float64(barWidth))
		if filled > barWidth {
			filled = barWidth
		}
		bar := strings.Repeat("█", filled) + strings.Repeat("░", barWidth-filled)
		pct := int(progress * 100)
		sb.WriteString(fmt.Sprintf("  [%s] %d%%\n\n", bar, pct))
	}

	// Current step
	sb.WriteString("  " + m.Spinner.View())
	if m.BootStep < len(m.BootSteps) {
		sb.WriteString(" " + m.BootSteps[m.BootStep])
	} else {
		sb.WriteString(" Finalizing...")
	}
	sb.WriteString("\n\n")

	// Completed steps
	for i := 0; i < m.BootStep && i < len(m.BootSteps); i++ {
		sb.WriteString(lipgloss.NewStyle().Foreground(colorGreen).Render(
			fmt.Sprintf("  ✓ %s\n", m.BootSteps[i])))
	}

	content := sb.String()
	totalLines := strings.Count(content, "\n") + 1
	if totalLines < h {
		content += strings.Repeat("\n", h-totalLines)
	}

	return content
}

// ── Utility ──────────────────────────────────────────────────────

func truncate(s string, maxWidth int) string {
	if lipgloss.Width(s) <= maxWidth {
		return s
	}
	runes := []rune(s)
	if len(runes) > maxWidth-1 {
		return string(runes[:maxWidth-1]) + "…"
	}
	return s
}

func formatRate(r float64) string {
	if r < 10 {
		return fmt.Sprintf("%.1f", r)
	}
	return fmt.Sprintf("%.0f", r)
}

func formatTokenCount(n int64) string {
	if n == 0 {
		return "0"
	}
	if n < 1000 {
		return fmt.Sprintf("%d", n)
	}
	if n < 1_000_000 {
		return fmt.Sprintf("%.1fk", float64(n)/1000)
	}
	return fmt.Sprintf("%.1fM", float64(n)/1_000_000)
}

func formatContextLength(n int) string {
	if n <= 0 {
		return "—"
	}
	k := n / 1024
	if k >= 1 && n%1024 == 0 {
		return fmt.Sprintf("%dk", k)
	}
	if n >= 1000 {
		return fmt.Sprintf("%.1fk", float64(n)/1000)
	}
	return fmt.Sprintf("%d", n)
}

func formatDuration(d time.Duration) string {
	if d < time.Minute {
		return fmt.Sprintf("%ds", int(d.Seconds()))
	}
	if d < time.Hour {
		m := int(d.Minutes())
		s := int(d.Seconds()) % 60
		return fmt.Sprintf("%dm %ds", m, s)
	}
	h := int(d.Hours())
	m := int(d.Minutes()) % 60
	return fmt.Sprintf("%dh %dm", h, m)
}

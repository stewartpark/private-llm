package tui

import (
	"github.com/charmbracelet/bubbles/spinner"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

// OpKind distinguishes up vs down operations.
type OpKind int

const (
	OpKindUp OpKind = iota
	OpKindDown
)

// OpPhase tracks the current phase of an up/down operation.
type OpPhase int

const (
	OpPhaseInit    OpPhase = iota
	OpPhasePreview         // up only
	OpPhaseConfirm         // both
	OpPhaseApply           // up only
	OpPhaseDestroy         // down only
	OpPhaseCerts           // up only
	OpPhaseDone
)

// Message types for the operation model.
type (
	opPhaseMsg   struct{ Phase OpPhase }
	opStepMsg    struct{ Label string }
	opSummaryMsg struct{ Summary map[string]int }
	opErrorMsg struct{ Err error }
	opLogMsg   struct{ Line string }
)

// OperationModel is the BubbleTea model for up/down operations.
type OperationModel struct {
	Kind      OpKind
	Phase     OpPhase
	Spinner   spinner.Model
	StepLabel string

	Summary      map[string]int
	LogLines     []string
	MaxLogLines  int
	ErrorMessage string
	Cancelled    bool

	// Log scrolling: 0 = pinned to bottom (follow), >0 = scrolled up by N lines
	LogScrollBack int

	confirmCh chan bool

	Width  int
	Height int
}

// NewOperationModel creates a new OperationModel.
func NewOperationModel(kind OpKind, confirmCh chan bool) OperationModel {
	s := spinner.New(
		spinner.WithSpinner(spinner.Dot),
		spinner.WithStyle(lipgloss.NewStyle().Foreground(colorCyan)),
	)
	label := "Initializing..."
	if kind == OpKindDown {
		label = "Initializing..."
	}
	return OperationModel{
		Kind:        kind,
		Phase:       OpPhaseInit,
		Spinner:     s,
		StepLabel:   label,
		LogLines:    make([]string, 0),
		MaxLogLines: 200,
		confirmCh:   confirmCh,
	}
}

// Init implements tea.Model.
func (m OperationModel) Init() tea.Cmd {
	return m.Spinner.Tick
}

// Update implements tea.Model.
func (m OperationModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.KeyMsg:
		switch msg.String() {
		case "q", "ctrl+c":
			if m.Phase == OpPhaseConfirm {
				select {
				case m.confirmCh <- false:
				default:
				}
				m.Cancelled = true
				m.Phase = OpPhaseDone
				return m, nil
			}
			return m, tea.Quit
		case "y", "Y":
			if m.Phase == OpPhaseConfirm {
				select {
				case m.confirmCh <- true:
				default:
				}
			}
		case "n", "N":
			if m.Phase == OpPhaseConfirm {
				select {
				case m.confirmCh <- false:
				default:
				}
				m.Cancelled = true
				m.Phase = OpPhaseDone
				return m, nil
			}
		case "up", "k":
			m.LogScrollBack++
			m.LogScrollBack = clampScroll(m.LogScrollBack, len(m.LogLines))
		case "down", "j":
			if m.LogScrollBack > 0 {
				m.LogScrollBack--
			}
		case "pgup":
			m.LogScrollBack += 10
			m.LogScrollBack = clampScroll(m.LogScrollBack, len(m.LogLines))
		case "pgdown":
			m.LogScrollBack -= 10
			if m.LogScrollBack < 0 {
				m.LogScrollBack = 0
			}
		}

	case tea.WindowSizeMsg:
		m.Width = msg.Width
		m.Height = msg.Height

	case spinner.TickMsg:
		var cmd tea.Cmd
		m.Spinner, cmd = m.Spinner.Update(msg)
		return m, cmd

	case opPhaseMsg:
		m.Phase = msg.Phase

	case opStepMsg:
		m.StepLabel = msg.Label

	case opSummaryMsg:
		m.Summary = msg.Summary

	case opErrorMsg:
		m.ErrorMessage = msg.Err.Error()
		m.Phase = OpPhaseDone

	case opLogMsg:
		wasAtBottom := m.LogScrollBack == 0
		m.LogLines = append(m.LogLines, msg.Line)
		if len(m.LogLines) > m.MaxLogLines {
			m.LogLines = m.LogLines[len(m.LogLines)-m.MaxLogLines:]
		}
		// If user had scrolled up, keep their position stable
		if !wasAtBottom {
			m.LogScrollBack++
			m.LogScrollBack = clampScroll(m.LogScrollBack, len(m.LogLines))
		}
	}

	return m, nil
}

func clampScroll(scrollBack, totalLines int) int {
	if scrollBack > totalLines {
		return totalLines
	}
	return scrollBack
}

// View implements tea.Model.
func (m OperationModel) View() string {
	return renderOperation(m)
}

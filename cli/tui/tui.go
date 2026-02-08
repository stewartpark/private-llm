package tui

import (
	"os"
	"strings"
	"sync"
	"time"

	"github.com/charmbracelet/bubbles/spinner"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

// Program wraps tea.Program with convenience methods.
type Program struct {
	program  *tea.Program
	actionCh chan Action
	exitErr  error
	exitMu   sync.Mutex
	ready    chan struct{}
}

// NewProgram creates a fullscreen TUI program (alt screen).
func NewProgram() *Program {
	actionCh := make(chan Action, 1)
	m := NewModel(actionCh)
	p := tea.NewProgram(
		m,
		tea.WithAltScreen(),
	)
	return &Program{program: p, actionCh: actionCh, ready: make(chan struct{})}
}

// Actions returns the channel that receives user-triggered actions.
func (p *Program) Actions() <-chan Action {
	return p.actionCh
}

// Start runs the TUI. Blocks until the program exits.
// Signals ready once bubbletea's internal message channel is initialized.
func (p *Program) Start() error {
	// Signal ready shortly after Run starts (Run initializes the msg channel
	// synchronously before entering the event loop).
	go func() {
		// Yield to let Run() initialize. In practice this is near-instant,
		// but 10ms gives plenty of margin.
		time.Sleep(10 * time.Millisecond)
		close(p.ready)
	}()
	_, err := p.program.Run()
	return err
}

// WaitReady blocks until the TUI program is ready to receive messages.
func (p *Program) WaitReady() {
	<-p.ready
}

// Send sends any tea.Msg to the program.
func (p *Program) Send(msg tea.Msg) {
	p.program.Send(msg)
}

// SendStatus sends a StatusUpdate to the TUI.
func (p *Program) SendStatus(u StatusUpdate) {
	p.program.Send(u)
}

// SendEvent sends a RequestEvent to the TUI.
func (p *Program) SendEvent(e RequestEvent) {
	p.program.Send(e)
}

// SetView changes the active view.
func (p *Program) SetView(v ViewType) {
	p.program.Send(viewChangeMsg{View: v})
}

// SetBootStep updates boot progress. Auto-transitions to dashboard when done.
func (p *Program) SetBootStep(step int) {
	p.program.Send(bootStepMsg{Step: step})
}

// SetSpinner switches to the spinner progress view.
func (p *Program) SetSpinner(steps []string, current int) {
	p.program.Send(spinnerSetupMsg{Steps: steps, Current: current})
}

// SetConfig sends static display config to the TUI.
func (p *Program) SetConfig(cfg ConfigMsg) {
	p.program.Send(cfg)
}

// Done signals the TUI to quit, optionally with an error.
// The error is stored and can be retrieved via ExitError() after Start() returns.
func (p *Program) Done(err error) {
	if err != nil {
		p.exitMu.Lock()
		p.exitErr = err
		p.exitMu.Unlock()
	}
	p.program.Send(doneMsg{Err: err})
}

// ExitError returns the error passed to Done(), if any.
func (p *Program) ExitError() error {
	p.exitMu.Lock()
	defer p.exitMu.Unlock()
	return p.exitErr
}

// Quit quits the TUI program.
func (p *Program) Quit() {
	p.program.Quit()
}

// LogWriter returns a writer that captures output and sends it to the TUI.
func (p *Program) LogWriter() *LogWriter {
	return &LogWriter{program: p.program}
}

// LogWriter captures log output and sends it to the TUI as logMsg.
type LogWriter struct {
	program *tea.Program
	closed  bool
	mu      sync.Mutex
}

func (w *LogWriter) Write(p []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.closed || w.program == nil {
		return len(p), nil
	}
	line := strings.TrimRight(string(p), "\n")
	if line != "" {
		w.program.Send(logMsg{Line: line})
	}
	return len(p), nil
}

// Close stops forwarding and silently discards further writes.
func (w *LogWriter) Close() {
	w.mu.Lock()
	w.closed = true
	w.mu.Unlock()
}

// RunWithSpinner shows a spinner while running fn. Returns fn's error.
func RunWithSpinner(label string, fn func() error) error {
	m := newSpinnerOnlyModel(label)
	p := tea.NewProgram(m, tea.WithOutput(os.Stderr))

	errCh := make(chan error, 1)
	go func() {
		err := fn()
		errCh <- err
		p.Send(doneMsg{Err: err})
	}()

	if _, err := p.Run(); err != nil {
		return err
	}
	return <-errCh
}

// spinnerOnlyModel is a minimal model for RunWithSpinner.
type spinnerOnlyModel struct {
	spinner spinner.Model
	label   string
	err     error
	done    bool
}

func newSpinnerOnlyModel(label string) spinnerOnlyModel {
	s := spinner.New(
		spinner.WithSpinner(spinner.Dot),
		spinner.WithStyle(lipgloss.NewStyle().Foreground(lipgloss.Color("#22d3ee"))),
	)
	return spinnerOnlyModel{spinner: s, label: label}
}

func (m spinnerOnlyModel) Init() tea.Cmd {
	return m.spinner.Tick
}

func (m spinnerOnlyModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.KeyMsg:
		if msg.String() == "ctrl+c" {
			return m, tea.Quit
		}
	case spinner.TickMsg:
		var cmd tea.Cmd
		m.spinner, cmd = m.spinner.Update(msg)
		return m, cmd
	case doneMsg:
		m.err = msg.Err
		m.done = true
		return m, tea.Quit
	}
	return m, nil
}

func (m spinnerOnlyModel) View() string {
	if m.done {
		if m.err != nil {
			return lipgloss.NewStyle().Foreground(lipgloss.Color("#f87171")).Render("  ✗ "+m.err.Error()) + "\n"
		}
		return lipgloss.NewStyle().Foreground(lipgloss.Color("#4ade80")).Render("  ✓ Done") + "\n"
	}
	return "  " + m.spinner.View() + " " + m.label + "\n"
}

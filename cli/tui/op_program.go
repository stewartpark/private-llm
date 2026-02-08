package tui

import (
	"context"
	"strings"
	"sync"
	"time"

	tea "github.com/charmbracelet/bubbletea"
)

// OperationProgram wraps tea.Program for up/down operation TUIs.
type OperationProgram struct {
	program   *tea.Program
	confirmCh chan bool
	ready     chan struct{}
	exitErr   error
	exitMu    sync.Mutex
}

// NewOperationProgram creates a fullscreen TUI for an up or down operation.
func NewOperationProgram(kind OpKind) *OperationProgram {
	confirmCh := make(chan bool, 1)
	m := NewOperationModel(kind, confirmCh)
	p := tea.NewProgram(m, tea.WithAltScreen())
	return &OperationProgram{
		program:   p,
		confirmCh: confirmCh,
		ready:     make(chan struct{}),
	}
}

// Start runs the TUI. Blocks until the program exits.
func (p *OperationProgram) Start() error {
	go func() {
		time.Sleep(10 * time.Millisecond)
		close(p.ready)
	}()
	_, err := p.program.Run()
	return err
}

// WaitReady blocks until the TUI is ready to receive messages.
func (p *OperationProgram) WaitReady() {
	<-p.ready
}

// Send sends any tea.Msg to the program.
func (p *OperationProgram) Send(msg tea.Msg) {
	p.program.Send(msg)
}

// SetPhase updates the current operation phase.
func (p *OperationProgram) SetPhase(phase OpPhase) {
	p.program.Send(opPhaseMsg{Phase: phase})
}

// SetStep updates the current step label.
func (p *OperationProgram) SetStep(label string) {
	p.program.Send(opStepMsg{Label: label})
}

// SetSummary sets the change summary for the confirm phase.
func (p *OperationProgram) SetSummary(summary map[string]int) {
	p.program.Send(opSummaryMsg{Summary: summary})
}

// Done signals the operation is complete. The TUI stays open until the user presses q.
func (p *OperationProgram) Done(err error) {
	if err != nil {
		p.exitMu.Lock()
		p.exitErr = err
		p.exitMu.Unlock()
		p.program.Send(opErrorMsg{Err: err})
		return
	}
	p.program.Send(opPhaseMsg{Phase: OpPhaseDone})
}

// ExitError returns the error stored by Done(), if any.
func (p *OperationProgram) ExitError() error {
	p.exitMu.Lock()
	defer p.exitMu.Unlock()
	return p.exitErr
}

// Quit quits the TUI program.
func (p *OperationProgram) Quit() {
	p.program.Quit()
}

// WaitConfirm blocks until the user presses y or n. Returns true for y.
// Also respects context cancellation to avoid deadlock.
func (p *OperationProgram) WaitConfirm(ctx context.Context) bool {
	select {
	case v := <-p.confirmCh:
		return v
	case <-ctx.Done():
		return false
	}
}

// LogWriter returns a writer that sends output to the TUI log area.
func (p *OperationProgram) LogWriter() *OpLogWriter {
	return &OpLogWriter{program: p.program}
}

// OpLogWriter captures output and sends it to the operation TUI.
type OpLogWriter struct {
	program *tea.Program
	closed  bool
	mu      sync.Mutex
}

func (w *OpLogWriter) Write(b []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.closed || w.program == nil {
		return len(b), nil
	}
	// Split multi-line writes into individual log lines
	text := strings.TrimRight(string(b), "\n")
	if text == "" {
		return len(b), nil
	}
	for _, line := range strings.Split(text, "\n") {
		if line != "" {
			w.program.Send(opLogMsg{Line: line})
		}
	}
	return len(b), nil
}

// Close stops forwarding and discards further writes.
func (w *OpLogWriter) Close() {
	w.mu.Lock()
	w.closed = true
	w.mu.Unlock()
}

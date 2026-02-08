package tui

import (
	"time"

	"github.com/charmbracelet/bubbles/spinner"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

// ViewType represents the current TUI view mode.
type ViewType int

const (
	ViewBoot ViewType = iota
	ViewDashboard
	ViewSpinner
)

// Action represents a user-triggered action from the TUI.
type Action int

const (
	ActionRestartVM Action = iota
	ActionResetVM
	ActionStopVM
	ActionStartVM
)

// RequestEvent represents a proxy request for the TUI request log.
type RequestEvent struct {
	Timestamp       time.Time
	Method          string
	Path            string
	Status          int
	Duration        time.Duration
	Encrypted       bool
	InputTokens     int64
	OutputTokens    int64
	OutputTokPerSec float64
}

// StatusUpdate carries polled status data into the model.
type StatusUpdate struct {
	VMStatus      string
	ExternalIP    string
	Firewall      bool
	SourceIP      string
	CertCreated   time.Time
	TokenCreated  time.Time
	IdleTime      time.Duration
	IdleTimeout   time.Duration
	InputTokens   int64
	OutputTokens  int64
	Error         error
}

// ConfigMsg sets static display configuration.
type ConfigMsg struct {
	Network       string
	ListenAddr    string
	Provider      string // e.g. "GCP"
	MachineType   string // e.g. "g4-standard-48"
	Zone          string // e.g. "us-central1-a"
	ModelName     string // e.g. "qwen3-coder-next:q8_0"
	ContextLength int    // e.g. 262144
}

// ActionStartMsg signals the TUI that a long-running action has begun.
type ActionStartMsg struct{ Label string }

// ActionDoneMsg signals the TUI that a long-running action has completed.
type ActionDoneMsg struct{ Err error }

// StreamingRate carries live tok/sec from the proxy during streaming.
type StreamingRate struct {
	OutputTokPerSec float64
}

// Internal message types for BubbleTea update loop.
type (
	viewChangeMsg   struct{ View ViewType }
	bootStepMsg     struct{ Step int }
	spinnerSetupMsg struct {
		Steps   []string
		Current int
	}
	doneMsg struct{ Err error }
	logMsg  struct{ Line string }
	tickMsg time.Time
)

func tickEvery(d time.Duration) tea.Cmd {
	return tea.Tick(d, func(t time.Time) tea.Msg {
		return tickMsg(t)
	})
}

// StatusData holds all status info displayed in the TUI.
type StatusData struct {
	VMStatus      string
	VMStatusColor string
	IdleTime      time.Duration
	IdleTimeout   time.Duration
	IdleTimeColor string

	ExternalIP     string
	FirewallActive bool
	FirewallColor  string
	SourceIP       string
	SourceIPRange  string

	CertCreated      time.Time
	CertAgeDays      int
	CertAgeColor     string
	TokenCreated     time.Time
	TokenAgeDays     int
	TokenAgeColor    string
	CAKeyLocation    string
	EncryptionActive bool

	InputTokens  int64
	OutputTokens int64
}

// Model is the main BubbleTea model.
type Model struct {
	// Display config (set once via ConfigMsg)
	Network       string
	ListenAddr    string
	Provider      string
	MachineType   string
	Zone          string
	ModelName     string
	ContextLength int

	// Polled status
	Status StatusData

	// View state
	ViewType  ViewType
	Spinner   spinner.Model
	BootStep  int
	BootSteps []string

	// Logo animation
	LogoFrame       int
	LastRequestTime time.Time

	// Request tracking
	RequestLog    []RequestEvent
	MaxRequestLog int

	// Log lines captured from log.Printf
	LogLines    []string
	MaxLogLines int

	// Timing
	StartTime time.Time

	// Actions
	ActionCh         chan Action
	ActionInProgress bool
	ActionLabel      string

	// Token animation state
	InputTokensDelta      int64
	OutputTokensDelta     int64
	InputTokensChangedAt  time.Time
	OutputTokensChangedAt time.Time

	// Token rate tracking
	CurrentTokPerSec float64
	MaxTokPerSec     float64
	IsStreaming       bool

	// Error
	ErrorMessage string

	// Terminal size
	Width  int
	Height int
}

// NewModel creates a new TUI model.
func NewModel(actionCh chan Action) Model {
	s := spinner.New(
		spinner.WithSpinner(spinner.Dot),
		spinner.WithStyle(lipgloss.NewStyle().Foreground(lipgloss.Color("#22d3ee"))),
	)

	return Model{
		Status: StatusData{
			VMStatus:      "UNKNOWN",
			VMStatusColor: "gray",
			CAKeyLocation: "LOCAL ONLY",
		},
		ViewType: ViewBoot,
		Spinner:  s,
		BootSteps: []string{
			"Loading configuration",
			"Verifying certificates",
			"Checking infrastructure",
			"Starting proxy server",
		},
		ActionCh:      actionCh,
		RequestLog:    make([]RequestEvent, 0),
		MaxRequestLog: 10,
		LogLines:      make([]string, 0),
		MaxLogLines:   100,
		StartTime:     time.Now(),
	}
}

// Init implements tea.Model.
func (m Model) Init() tea.Cmd {
	return tea.Batch(
		m.Spinner.Tick,
		tickEvery(time.Second),
	)
}

// IsAnimating returns true if the logo should be animated (request in last 3s).
func (m Model) IsAnimating() bool {
	if m.LastRequestTime.IsZero() {
		return false
	}
	return time.Since(m.LastRequestTime) < 3*time.Second
}

// Update implements tea.Model.
func (m Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.KeyMsg:
		switch msg.String() {
		case "q", "esc", "ctrl+c":
			return m, tea.Quit
		case "r":
			if m.ViewType == ViewDashboard && !m.ActionInProgress && m.Status.VMStatus == "RUNNING" {
				select {
				case m.ActionCh <- ActionRestartVM:
				default:
				}
			}
		case "R":
			if m.ViewType == ViewDashboard && !m.ActionInProgress {
				select {
				case m.ActionCh <- ActionResetVM:
				default:
				}
			}
		case "S":
			if m.ViewType == ViewDashboard && !m.ActionInProgress {
				if m.Status.VMStatus == "RUNNING" {
					select {
					case m.ActionCh <- ActionStopVM:
					default:
					}
				} else {
					select {
					case m.ActionCh <- ActionStartVM:
					default:
					}
				}
			}
		}

	case tea.WindowSizeMsg:
		m.Width = msg.Width
		m.Height = msg.Height

	case spinner.TickMsg:
		var cmd tea.Cmd
		m.Spinner, cmd = m.Spinner.Update(msg)
		return m, cmd

	case tickMsg:
		m.refreshTimers()
		if m.IsAnimating() {
			m.LogoFrame = (m.LogoFrame + 1) % 4
		} else {
			m.LogoFrame = 0
		}
		return m, tickEvery(time.Second)

	case ConfigMsg:
		m.Network = msg.Network
		m.ListenAddr = msg.ListenAddr
		m.Provider = msg.Provider
		m.MachineType = msg.MachineType
		m.Zone = msg.Zone
		m.ModelName = msg.ModelName
		m.ContextLength = msg.ContextLength

	case viewChangeMsg:
		m.ViewType = msg.View

	case bootStepMsg:
		m.BootStep = msg.Step
		if m.ViewType == ViewBoot && m.BootStep >= len(m.BootSteps) {
			m.ViewType = ViewDashboard
		}

	case spinnerSetupMsg:
		m.ViewType = ViewSpinner
		m.BootSteps = msg.Steps
		m.BootStep = msg.Current

	case doneMsg:
		if msg.Err != nil {
			m.ErrorMessage = msg.Err.Error()
		}
		return m, tea.Quit

	case ActionStartMsg:
		m.ActionInProgress = true
		m.ActionLabel = msg.Label
		m.ErrorMessage = ""

	case ActionDoneMsg:
		m.ActionInProgress = false
		m.ActionLabel = ""
		if msg.Err != nil {
			m.ErrorMessage = msg.Err.Error()
		}

	case StreamingRate:
		m.CurrentTokPerSec = msg.OutputTokPerSec
		if msg.OutputTokPerSec > m.MaxTokPerSec {
			m.MaxTokPerSec = msg.OutputTokPerSec
		}
		m.IsStreaming = true

	case StatusUpdate:
		m.handleStatusUpdate(msg)

	case RequestEvent:
		m.handleRequestEvent(msg)

	case logMsg:
		m.LogLines = append(m.LogLines, msg.Line)
		if len(m.LogLines) > m.MaxLogLines {
			m.LogLines = m.LogLines[len(m.LogLines)-m.MaxLogLines:]
		}
	}

	return m, nil
}

func (m *Model) refreshTimers() {
	if !m.Status.CertCreated.IsZero() {
		days := int(time.Since(m.Status.CertCreated).Hours() / 24)
		m.Status.CertAgeDays = days
		if days >= 3 {
			m.Status.CertAgeColor = "red"
		} else {
			m.Status.CertAgeColor = "green"
		}
	}
	if !m.Status.TokenCreated.IsZero() {
		days := int(time.Since(m.Status.TokenCreated).Hours() / 24)
		m.Status.TokenAgeDays = days
		if days >= 3 {
			m.Status.TokenAgeColor = "red"
		} else {
			m.Status.TokenAgeColor = "green"
		}
	}
	m.updateIdleDisplay()
}

func (m *Model) updateIdleDisplay() {
	// Show nothing if VM is not running or no requests have been made
	if m.Status.VMStatus != "RUNNING" || m.LastRequestTime.IsZero() {
		m.Status.IdleTime = 0
		m.Status.IdleTimeColor = ""
		return
	}
	m.Status.IdleTime = time.Since(m.LastRequestTime)
	if m.Status.IdleTimeout > 0 {
		threshold := time.Duration(float64(m.Status.IdleTimeout) * 0.75)
		if m.Status.IdleTime >= threshold {
			m.Status.IdleTimeColor = "red"
		} else {
			m.Status.IdleTimeColor = "green"
		}
	} else {
		m.Status.IdleTimeColor = "green"
	}
}

func (m *Model) handleStatusUpdate(u StatusUpdate) {
	if u.Error != nil {
		m.ErrorMessage = u.Error.Error()
		return
	}
	m.ErrorMessage = ""

	if u.VMStatus != "" {
		m.Status.VMStatus = u.VMStatus
		switch u.VMStatus {
		case "RUNNING":
			m.Status.VMStatusColor = "green"
		case "STOPPED", "TERMINATED":
			m.Status.VMStatusColor = "red"
		case "STOPPING", "STAGING":
			m.Status.VMStatusColor = "yellow"
		default:
			m.Status.VMStatusColor = "white"
		}
	}

	if u.ExternalIP != "" {
		m.Status.ExternalIP = u.ExternalIP
	}

	m.Status.FirewallActive = u.Firewall
	if u.Firewall {
		m.Status.FirewallColor = "green"
		m.Status.SourceIP = u.SourceIP
		if u.SourceIP != "" {
			m.Status.SourceIPRange = u.SourceIP + "/32"
		}
	} else {
		m.Status.FirewallColor = "red"
	}

	if !u.CertCreated.IsZero() {
		m.Status.CertCreated = u.CertCreated
		m.Status.EncryptionActive = true
		days := int(time.Since(u.CertCreated).Hours() / 24)
		m.Status.CertAgeDays = days
		if days >= 3 {
			m.Status.CertAgeColor = "red"
		} else {
			m.Status.CertAgeColor = "green"
		}
	}

	if !u.TokenCreated.IsZero() {
		m.Status.TokenCreated = u.TokenCreated
		days := int(time.Since(u.TokenCreated).Hours() / 24)
		m.Status.TokenAgeDays = days
		if days >= 3 {
			m.Status.TokenAgeColor = "red"
		} else {
			m.Status.TokenAgeColor = "green"
		}
	}

	if u.IdleTimeout > 0 {
		m.Status.IdleTimeout = u.IdleTimeout
	}
	if u.IdleTime > 0 {
		m.LastRequestTime = time.Now().Add(-u.IdleTime)
	}
	m.updateIdleDisplay()

	// Track token changes for flash animation
	if u.InputTokens > m.Status.InputTokens {
		m.InputTokensDelta = u.InputTokens - m.Status.InputTokens
		m.InputTokensChangedAt = time.Now()
	}
	if u.OutputTokens > m.Status.OutputTokens {
		m.OutputTokensDelta = u.OutputTokens - m.Status.OutputTokens
		m.OutputTokensChangedAt = time.Now()
	}
	m.Status.InputTokens = u.InputTokens
	m.Status.OutputTokens = u.OutputTokens
}

func (m *Model) handleRequestEvent(e RequestEvent) {
	m.RequestLog = append(m.RequestLog, e)
	if len(m.RequestLog) > m.MaxRequestLog {
		m.RequestLog = m.RequestLog[1:]
	}
	m.LastRequestTime = time.Now()
	m.LogoFrame = 1 // Immediately start animation

	// Record final rate from completed request
	m.IsStreaming = false
	if e.OutputTokPerSec > 0 {
		m.CurrentTokPerSec = e.OutputTokPerSec
		if e.OutputTokPerSec > m.MaxTokPerSec {
			m.MaxTokPerSec = e.OutputTokPerSec
		}
	}
}

// View implements tea.Model.
func (m Model) View() string {
	switch m.ViewType {
	case ViewBoot:
		return renderBoot(m)
	case ViewSpinner:
		return renderSpinner(m)
	default:
		return renderDashboard(m)
	}
}

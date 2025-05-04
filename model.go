// Package vimtea provides a Vim-like text editor component for terminal applications
// built with Bubble Tea. It supports normal, insert, visual, and command modes with
// key bindings similar to Vim.
package vimtea

import (
	"context"
	"strconv"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"golang.design/x/clipboard"
)

// EditorMode represents the current mode of the editor
type EditorMode int

type EditorModeMsg struct {
	Mode EditorMode
}

const (
	// ModeNormal is the default mode for navigation and commands
	ModeNormal EditorMode = iota
	// ModeInsert is for inserting and editing text
	ModeInsert
	// ModeVisual is for selecting text
	ModeVisual
	// ModeCommand is for entering commands with a colon prompt
	ModeCommand
)

// cursorBlinkMsg is used for cursor blinking animation
type cursorBlinkMsg time.Time

// String returns the string representation of the editor mode
func (m EditorMode) String() string {
	return [...]string{"NORMAL", "INSERT", "VISUAL", "COMMAND"}[m]
}

// Editor defines the interface for interacting with the editor component
type Editor interface {
	// Implements the bubbletea.Model interface
	tea.Model

	// AddBinding registers a new key binding
	AddBinding(binding KeyBinding)

	// AddCommand registers a new command that can be executed in command mode
	AddCommand(name string, cmd CommandFn)

	// GetBuffer returns the current buffer
	GetBuffer() Buffer

	// GetMode returns the current editor mode
	GetMode() EditorMode

	// SetMode changes the current editor mode
	SetMode(mode EditorMode) tea.Cmd

	// SetStatusMessage sets the status message displayed in the status bar
	SetStatusMessage(msg string) tea.Cmd

	// SetSize updates the editor's dimensions when the terminal window is resized
	SetSize(width, height int) (tea.Model, tea.Cmd)

	// Tick sends a tick message to the editor
	Tick() tea.Cmd

	// Reset restores the editor to its initial state
	Reset() tea.Cmd
}

// editorModel implements the Editor interface and maintains the editor state
type editorModel struct {
	buffer         *buffer // Text buffer with undo/redo
	cursor         Cursor  // Current cursor position
	yankBuffer     string  // Clipboard
	lastOp         string  // Last operation performed (for repeating with .)
	fullScreen     bool    // Whether to use the full terminal screen
	initialContent string  // Initial content used to create the editor

	mode              EditorMode // Current mode
	enableCommandMode bool       // Whether command mode is enabled
	desiredCol        int        // Desired column position for vertical movements
	keySequence       []string   // Current key sequence for vim-like commands
	lastKeyTime       time.Time  // Time of the last keypress for sequence timeout
	commandBuffer     string     // Command mode input buffer
	visualStart       Cursor     // Start position of visual selection
	isVisualLine      bool       // Whether we're in line-wise visual mode (V)

	countPrefix int // Numeric prefix for commands like "10j"

	relativeNumbers bool // Whether to show relative line numbers

	viewport        viewport.Model // For scrolling
	width           int            // Window width
	height          int            // Window height
	statusMessage   string         // Current status message
	cursorBlink     bool           // Whether cursor is visible (for blinking)
	lastBlinkTime   time.Time      // Time of last cursor blink
	blinkInterval   time.Duration  // Cursor blink interval
	enableStatusBar bool           // Whether to show the status bar
	waitReplace     bool           // Whether waiting for replace character

	lineNumberStyle        lipgloss.Style
	currentLineNumberStyle lipgloss.Style
	textStyle              lipgloss.Style
	statusStyle            lipgloss.Style
	cursorStyle            lipgloss.Style
	commandStyle           lipgloss.Style
	selectedStyle          lipgloss.Style

	highlighter *syntaxHighlighter

	yankHighlight yankHighlight

	registry *BindingRegistry // Registry for key bindings
	commands *CommandRegistry // Registry for commands
}

// options holds configuration options for creating a new editor
type options struct {
	Content                string         // Initial content for the editor
	EnableCommandMode      bool           // Whether to enable command mode
	EnableStatusBar        bool           // Whether to show the status bar
	DefaultSyntaxTheme     string         // Syntax highlighting theme
	BlinkInterval          time.Duration  // Cursor blink interval
	TextStyle              lipgloss.Style // Style for regular text
	LineNumberStyle        lipgloss.Style // Style for line numbers
	CurrentLineNumberStyle lipgloss.Style // Style for current line number
	StatusStyle            lipgloss.Style // Style for status bar
	CursorStyle            lipgloss.Style // Style for cursor
	CommandStyle           lipgloss.Style // Style for command line
	SelectedStyle          lipgloss.Style // Style for selected text
	FileName               string         // Filename for syntax highlighting
	RelativeNumbers        bool           // Whether to show relative line numbers
	FullScreen             bool           // Whether to use the full terminal screen
}

// EditorOption is a function that modifies the editor options
type EditorOption func(*options)

// NewEditor creates a new editor instance with the provided options
func NewEditor(opts ...EditorOption) Editor {
	options := &options{
		Content:                "",
		EnableCommandMode:      true,
		EnableStatusBar:        true,
		DefaultSyntaxTheme:     "catppuccin-macchiato",
		BlinkInterval:          1 * time.Second,
		TextStyle:              textStyle,
		LineNumberStyle:        lineNumberStyle,
		CurrentLineNumberStyle: currentLineNumberStyle,
		StatusStyle:            statusStyle,
		CursorStyle:            cursorStyle,
		CommandStyle:           commandStyle,
		SelectedStyle:          selectedStyle,
		FileName:               "",
		RelativeNumbers:        false,
		FullScreen:             false,
	}

	// Apply all options
	for _, opt := range opts {
		opt(options)
	}

	cpErr := clipboard.Init()

	m := &editorModel{
		buffer:                 newBuffer(options.Content),
		mode:                   ModeNormal,
		fullScreen:             options.FullScreen,
		enableCommandMode:      options.EnableCommandMode,
		enableStatusBar:        options.EnableStatusBar,
		cursor:                 newCursor(0, 0),
		keySequence:            []string{},
		viewport:               viewport.New(0, 0),
		cursorBlink:            true,
		lastBlinkTime:          time.Now(),
		blinkInterval:          options.BlinkInterval,
		lineNumberStyle:        options.LineNumberStyle,
		currentLineNumberStyle: options.CurrentLineNumberStyle,
		textStyle:              options.TextStyle,
		statusStyle:            options.StatusStyle,
		cursorStyle:            options.CursorStyle,
		commandStyle:           options.CommandStyle,
		selectedStyle:          options.SelectedStyle,
		relativeNumbers:        options.RelativeNumbers,
		countPrefix:            1,

		highlighter:    newSyntaxHighlighter(options.DefaultSyntaxTheme, options.FileName),
		yankHighlight:  newYankHighlight(),
		registry:       newBindingRegistry(),
		commands:       newCommandRegistry(),
		initialContent: options.Content,
	}
	go func() {
		if cpErr != nil {
			ch := clipboard.Watch(context.Background(), clipboard.FmtText)
			for data := range ch {
				m.yankBuffer = string(data)
			}
		}
	}()

	// Register default key bindings
	registerBindings(m)
	return m
}

// cursorBlinkCmd creates a command that triggers cursor blinking animation
func cursorBlinkCmd() tea.Cmd {
	return tea.Tick(time.Second*1, func(t time.Time) tea.Msg {
		return cursorBlinkMsg(t)
	})
}

// Init initializes the editor model and returns the cursor blink command
func (m *editorModel) Init() tea.Cmd {
	return cursorBlinkCmd()
}

// Update handles messages and updates the editor state
// This is part of the tea.Model interface
func (m *editorModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	var cmd tea.Cmd

	switch msg := msg.(type) {
	case tea.KeyMsg:
		// Reset cursor blink on keypress
		m.cursorBlink = true
		m.lastBlinkTime = time.Now()
		return m.handleKeypress(msg)
	case tea.WindowSizeMsg:
		if m.fullScreen {
			return m.SetSize(msg.Width, msg.Height)
		}

	case cursorBlinkMsg:
		// Handle cursor blinking animation
		now := time.Time(msg)
		if now.Sub(m.lastBlinkTime) >= m.blinkInterval {
			m.cursorBlink = !m.cursorBlink
			m.lastBlinkTime = now
		}

		// Handle yank highlight timeout
		if m.yankHighlight.Active && now.Sub(m.yankHighlight.StartTime) >= m.yankHighlight.Duration {
			m.yankHighlight.Active = false
		}
		cmd = cursorBlinkCmd()

	case statusMessageMsg:
		m.statusMessage = string(msg)

	case UndoRedoMsg:
		if msg.Success {
			m.cursor = msg.NewCursor
			m.ensureCursorVisible()

			if msg.IsUndo {
				m.statusMessage = "Undo"
			} else {
				m.statusMessage = "Redo"
			}
		}

	case CommandMsg:
		// Execute registered command
		registeredCmd := m.commands.Get(msg.Command)
		if registeredCmd != nil {
			cmd = registeredCmd(m)
		} else {
			m.statusMessage = "Unknown command"
		}
		m.commandBuffer = ""
		m.mode = ModeNormal
	}

	return m, cmd
}

// GetSelectionBoundary returns the start and end cursors of the current selection
// in visual mode. It ensures the start cursor is always before the end cursor.
func (m *editorModel) GetSelectionBoundary() (Cursor, Cursor) {
	var start, end Cursor

	// Determine start and end positions based on cursor and visual start
	if m.visualStart.Row < m.cursor.Row ||
		(m.visualStart.Row == m.cursor.Row && m.visualStart.Col <= m.cursor.Col) {
		start = m.visualStart
		end = m.cursor
	} else {
		start = m.cursor
		end = m.visualStart
	}

	// Handle line-wise visual mode (V)
	if m.isVisualLine {
		start.Col = 0
		end.Col = max(max(0, m.buffer.lineLength(end.Row)-1), 0)
	}

	return start, end
}

// SetSize updates the editor's dimensions when the terminal window is resized
func (m *editorModel) SetSize(width, height int) (tea.Model, tea.Cmd) {
	m.width = width
	m.height = height

	// Adjust height for status bar
	if m.enableStatusBar {
		m.height = height - 2
	}

	// Update viewport dimensions
	m.viewport.Width = width
	m.viewport.Height = height

	if m.enableStatusBar {
		m.viewport.Height = height - 2
	}

	// Ensure cursor is visible after resize
	m.ensureCursorVisible()
	return m, nil
}

// handleKeypress processes keyboard input based on the current editor mode
func (m *editorModel) handleKeypress(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch m.mode {
	case ModeNormal:
		// Normal mode uses key sequence handling for multi-key commands
		return m.handlePrefixKeypress(ModeNormal)(msg)

	case ModeInsert:
		// Check for registered keybindings first
		if binding := m.registry.FindExact(msg.String(), ModeInsert); binding != nil {
			cmd := binding.Command(m)
			m.ensureCursorVisible()
			return m, cmd
		} else {
			// Insert regular characters
			if len(msg.String()) == 1 {
				// if waitin for replace, insert and return to normal mode
				if m.waitReplace {
					m.waitReplace = false
					return replaceCurrentCharacter(m, msg.String())
				}
				return insertCharacter(m, msg.String())
			}
		}

	case ModeVisual:
		// Visual mode also uses key sequence handling
		return m.handlePrefixKeypress(ModeVisual)(msg)

	case ModeCommand:
		// Check for registered command mode keybindings
		if binding := m.registry.FindExact(msg.String(), ModeCommand); binding != nil {
			cmd := binding.Command(m)
			return m, cmd
		} else {
			// Add character to command buffer
			if len(msg.String()) == 1 {
				return addCommandCharacter(m, msg.String())
			}
		}
	}
	return m, nil
}

// handlePrefixKeypress creates a handler for key sequences and numeric prefixes
// This implements Vim-style command sequences like "3dw" or "dd"
func (m *editorModel) handlePrefixKeypress(mode EditorMode) func(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	return func(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
		now := time.Now()

		// Check for key sequence timeout - if the sequence hasn't been completed
		// in time, we should reset it
		if now.Sub(m.lastKeyTime) > 750*time.Millisecond && len(m.keySequence) > 0 {
			seq := strings.Join(m.keySequence, "")

			// Try to execute the sequence if it matches a binding
			if binding := m.registry.FindExact(seq, mode); binding != nil {
				cmd := binding.Command(m)
				m.keySequence = []string{}
				m.countPrefix = 1
				return m, cmd
			}

			// Reset sequence if timeout reached
			m.keySequence = []string{}
			m.countPrefix = 1
		}
		m.lastKeyTime = now

		keyStr := msg.String()

		// Handle numeric prefixes (like "3j" to move down 3 lines)
		if len(m.keySequence) == 0 && keyStr > "0" && keyStr <= "9" {
			// First digit in sequence
			count, _ := strconv.Atoi(keyStr)
			m.countPrefix = count
			m.keySequence = append(m.keySequence, keyStr)
			return m, nil
		} else if len(m.keySequence) > 0 && keyStr >= "0" && keyStr <= "9" {
			// Check if we're continuing a numeric prefix
			allDigits := true
			for _, k := range m.keySequence {
				if k < "0" || k > "9" {
					allDigits = false
					break
				}
			}

			if allDigits {
				// Multi-digit count (like "12j")
				countStr := strings.Join(m.keySequence, "") + keyStr
				count, _ := strconv.Atoi(countStr)
				m.countPrefix = count
				m.keySequence = append(m.keySequence, keyStr)
				return m, nil
			}
		}

		// Add the key to the sequence
		m.keySequence = append(m.keySequence, keyStr)
		seq := strings.Join(m.keySequence, "")

		// Check if the sequence exactly matches a binding
		if binding := m.registry.FindExact(seq, mode); binding != nil {
			cmd := binding.Command(m)
			m.keySequence = []string{}
			defer func() { m.countPrefix = 1 }()
			return m, cmd
		}

		// If the sequence is a prefix of a longer binding, wait for more input
		if m.registry.IsPrefix(seq, mode) {
			return m, nil
		}

		// Try to separate numeric prefix from command part
		nonDigitStart := 0
		for i, k := range m.keySequence {
			if k < "0" || k > "9" {
				nonDigitStart = i
				break
			}
		}

		// If we have a mixture of digits and commands, try to execute just the command part
		if nonDigitStart > 0 && nonDigitStart < len(m.keySequence) {
			cmdPart := strings.Join(m.keySequence[nonDigitStart:], "")
			if binding := m.registry.FindExact(cmdPart, mode); binding != nil {
				cmd := binding.Command(m)
				m.keySequence = []string{}
				defer func() { m.countPrefix = 1 }()
				return m, cmd
			}
		}

		// Fallback: try to execute just the single key
		if len(m.keySequence) == 1 {
			if binding := m.registry.FindExact(keyStr, mode); binding != nil {
				cmd := binding.Command(m)
				m.keySequence = []string{}
				defer func() { m.countPrefix = 1 }()
				return m, cmd
			}
		} else {
			// Try with just the last key in sequence
			lastKey := m.keySequence[len(m.keySequence)-1]
			m.keySequence = []string{lastKey}

			if binding := m.registry.FindExact(lastKey, mode); binding != nil {
				cmd := binding.Command(m)
				m.keySequence = []string{}
				defer func() { m.countPrefix = 1 }()
				return m, cmd
			}
		}

		// No match found, reset everything
		m.keySequence = []string{}
		m.countPrefix = 1
		return m, nil
	}
}

// GetBuffer returns a wrapped buffer that provides the Buffer interface
func (m *editorModel) GetBuffer() Buffer {
	return &wrappedBuffer{m}
}

// AddBinding registers a new key binding with the editor
func (m *editorModel) AddBinding(binding KeyBinding) {
	m.registry.Add(binding.Key, func(em *editorModel) tea.Cmd {
		return binding.Handler(m.GetBuffer())
	}, binding.Mode, binding.Description)
}

// AddCommand registers a new command that can be executed in command mode
// Commands are invoked by typing ":command" in command mode
func (m *editorModel) AddCommand(name string, cmd CommandFn) {
	internalCmd := func(m *editorModel) tea.Cmd {
		// Parse command arguments from the command buffer
		args := strings.Fields(m.commandBuffer)
		if len(args) > 0 {
			args = args[1:] // Remove the command name
		}

		return cmd(m.GetBuffer(), args)
	}

	m.commands.Register(name, internalCmd)
}

// GetMode returns the current editor mode
func (m *editorModel) GetMode() EditorMode {
	return m.mode
}

// SetMode changes the current editor mode
func (m *editorModel) SetMode(mode EditorMode) tea.Cmd {
	cmds := []tea.Cmd{
		func() tea.Msg {
			return EditorModeMsg{Mode: mode}
		},
	}
	var cmd tea.Cmd
	if mode == ModeVisual {
		cmd = beginVisualSelection(m)
	} else {
		cmd = switchMode(m, mode)
	}
	if cmd != nil {
		cmds = append(cmds, cmd)
	}
	return tea.Batch(cmds...)
}

func (m *editorModel) Tick() tea.Cmd {
	return cursorBlinkCmd()
}

// SetStatusMessage sets the status message shown in the status bar
// and returns a command that can be used with bubbletea
func (m *editorModel) SetStatusMessage(msg string) tea.Cmd {
	return func() tea.Msg {
		m.statusMessage = msg
		return nil
	}
}

// SetStatusMsg creates a command that sets the status message
// This can be used by external components to update the editor's status message
func SetStatusMsg(msg string) tea.Cmd {
	return func() tea.Msg {
		return statusMessageMsg(msg)
	}
}

// Reset restores the editor to its initial state
func (m *editorModel) Reset() tea.Cmd {
	// Save current state for undo if needed
	m.buffer.saveUndoState(m.cursor)

	// Reset buffer to initial content
	m.buffer = newBuffer(m.initialContent)

	// Reset cursor position
	m.cursor = newCursor(0, 0)

	// Reset editor state
	m.yankBuffer = ""
	m.keySequence = []string{}
	m.mode = ModeNormal
	m.commandBuffer = ""
	m.desiredCol = 0
	m.visualStart = newCursor(0, 0)
	m.isVisualLine = false
	m.countPrefix = 1

	// Reset viewport
	m.viewport.YOffset = 0
	m.ensureCursorVisible()

	// Return a command that updates the status message
	return SetStatusMsg("Editor reset")
}

// statusMessageMsg is a message type for updating the status message
type statusMessageMsg string

// WithContent sets the initial content for the editor
func WithContent(content string) EditorOption {
	return func(o *options) {
		o.Content = content
	}
}

// WithEnableModeCommand enables or disables command mode (:commands)
func WithEnableModeCommand(enable bool) EditorOption {
	return func(o *options) {
		o.EnableCommandMode = enable
	}
}

// WithEnableStatusBar enables or disables the status bar at the bottom
func WithEnableStatusBar(enable bool) EditorOption {
	return func(o *options) {
		o.EnableStatusBar = enable
	}
}

// WithDefaultSyntaxTheme sets the syntax highlighting theme
// Available themes include "catppuccin-macchiato" and others
func WithDefaultSyntaxTheme(theme string) EditorOption {
	return func(o *options) {
		o.DefaultSyntaxTheme = theme
	}
}

// WithBlinkInterval sets the cursor blink interval duration
func WithBlinkInterval(interval time.Duration) EditorOption {
	return func(o *options) {
		o.BlinkInterval = interval
	}
}

// WithTextStyle sets the style for regular text
func WithTextStyle(style lipgloss.Style) EditorOption {
	return func(o *options) {
		o.TextStyle = style
	}
}

// WithLineNumberStyle sets the style for line numbers
func WithLineNumberStyle(style lipgloss.Style) EditorOption {
	return func(o *options) {
		o.LineNumberStyle = style
	}
}

// WithCurrentLineNumberStyle sets the style for the current line number
func WithCurrentLineNumberStyle(style lipgloss.Style) EditorOption {
	return func(o *options) {
		o.CurrentLineNumberStyle = style
	}
}

// WithStatusStyle sets the style for the status bar
func WithStatusStyle(style lipgloss.Style) EditorOption {
	return func(o *options) {
		o.StatusStyle = style
	}
}

// WithCursorStyle sets the style for the cursor
func WithCursorStyle(style lipgloss.Style) EditorOption {
	return func(o *options) {
		o.CursorStyle = style
	}
}

// WithCommandStyle sets the style for the command line
func WithCommandStyle(style lipgloss.Style) EditorOption {
	return func(o *options) {
		o.CommandStyle = style
	}
}

// WithSelectedStyle sets the style for selected text
func WithSelectedStyle(style lipgloss.Style) EditorOption {
	return func(o *options) {
		o.SelectedStyle = style
	}
}

// WithFileName sets the filename for syntax highlighting
func WithFileName(fileName string) EditorOption {
	return func(o *options) {
		o.FileName = fileName
	}
}

// WithRelativeNumbers enables or disables relative line numbering
// When enabled, line numbers show the distance from the current line
func WithRelativeNumbers(enable bool) EditorOption {
	return func(o *options) {
		o.RelativeNumbers = enable
	}
}

func WithFullScreen() EditorOption {
	return func(o *options) {
		o.FullScreen = true
	}
}

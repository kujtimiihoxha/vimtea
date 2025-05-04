// Package vimtea provides a Vim-like text editor component for terminal applications
package vimtea

import (
	"fmt"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"golang.design/x/clipboard"
)

// CommandFn is a function that can be executed when a command is run in command mode
// It takes a buffer reference and command arguments, and returns a bubbletea command
type CommandFn func(Buffer, []string) tea.Cmd

// CommandMsg is sent when a command is executed from command mode
// It contains the command name that should be looked up in the CommandRegistry
type CommandMsg struct {
	Command string // Command name without arguments
}

// withCountPrefix executes a function multiple times based on the numeric prefix
// This implements commands like "5j" to move down 5 lines
func withCountPrefix(model *editorModel, fn func()) {
	count := model.countPrefix
	for range count {
		fn()
	}
	model.countPrefix = 1
}

// switchMode changes the editor mode and performs necessary setup for the new mode
// Different modes require different cursor handling and UI state
func switchMode(model *editorModel, newMode EditorMode) tea.Cmd {
	model.mode = newMode

	switch newMode {
	case ModeNormal:
		// In normal mode, cursor can't be at end of line
		if model.buffer.lineLength(model.cursor.Row) > 0 &&
			model.cursor.Col >= model.buffer.lineLength(model.cursor.Row) {
			model.cursor.Col = max(0, model.buffer.lineLength(model.cursor.Row)-1)
		}
		model.isVisualLine = false
		model.statusMessage = ""
	case ModeCommand:
		// Reset command buffer when entering command mode
		model.commandBuffer = ""
	}

	return func() tea.Msg {
		return EditorModeMsg{newMode}
	}
}

func registerBindings(m *editorModel) {
	m.registry.Add("i", enterModeInsert, ModeNormal, "Enter insert mode")
	m.registry.Add("v", beginVisualSelection, ModeNormal, "Enter visual mode")
	m.registry.Add("V", beginVisualLineSelection, ModeNormal, "Enter visual line mode")
	m.registry.Add("x", deleteCharAtCursor, ModeNormal, "Delete character at cursor")
	m.registry.Add("r", beginReplaceAtCursor, ModeNormal, "Delete character at cursor")
	if m.enableCommandMode {
		m.registry.Add(":", enterModeCommand, ModeNormal, "Enter command mode")
	}

	m.registry.Add("a", appendAfterCursor, ModeNormal, "Append after cursor")
	m.registry.Add("A", appendAtEndOfLine, ModeNormal, "Append at end of line")
	m.registry.Add("I", insertAtStartOfLine, ModeNormal, "Insert at start of line")
	m.registry.Add("o", openLineBelow, ModeNormal, "Open line below")
	m.registry.Add("O", openLineAbove, ModeNormal, "Open line above")

	m.registry.Add("yy", yankLine, ModeNormal, "Yank line")
	m.registry.Add("dd", deleteLine, ModeNormal, "Delete line")
	m.registry.Add("D", deleteToEndOfLine, ModeNormal, "Delete to end of line")
	m.registry.Add("C", changeToEndOfLine, ModeNormal, "Change to end of line")
	m.registry.Add("p", pasteAfter, ModeNormal, "Paste after cursor")
	m.registry.Add("P", pasteBefore, ModeNormal, "Paste before cursor")

	m.registry.Add("u", undo, ModeNormal, "Undo")
	m.registry.Add("ctrl+r", redo, ModeNormal, "Redo")
	m.registry.Add("diw", deleteInnerWord, ModeNormal, "Delete inner word")
	m.registry.Add("yiw", yankInnerWord, ModeNormal, "Yank inner word")
	m.registry.Add("ciw", changeInnerWord, ModeNormal, "Change inner word")

	for _, mode := range []EditorMode{ModeNormal, ModeVisual} {
		m.registry.Add("h", moveCursorLeft, mode, "Move cursor left")
		m.registry.Add("j", moveCursorDown, mode, "Move cursor down")
		m.registry.Add("k", moveCursorUp, mode, "Move cursor up")
		m.registry.Add("l", moveCursorRight, mode, "Move cursor right")
		m.registry.Add("w", moveToNextWordStart, mode, "Move to next word")
		m.registry.Add("b", moveToPrevWordStart, mode, "Move to previous word")

		m.registry.Add(" ", moveCursorRightOrNextLine, mode, "Move cursor right")
		m.registry.Add("0", moveToStartOfLine, mode, "Move to start of line")
		m.registry.Add("^", moveToFirstNonWhitespace, mode, "Move to first non-whitespace character")
		m.registry.Add("$", moveToEndOfLine, mode, "Move to end of line")
		m.registry.Add("gg", moveToStartOfDocument, mode, "Move to document start")
		m.registry.Add("G", moveToEndOfDocument, mode, "Move to document end")

		m.registry.Add("up", moveCursorUp, mode, "Move cursor up")
		m.registry.Add("down", moveCursorDown, mode, "Move cursor down")
		m.registry.Add("left", moveCursorLeft, mode, "Move cursor left")
		m.registry.Add("right", moveCursorRight, mode, "Move cursor right")
	}

	m.registry.Add("esc", exitModeVisual, ModeVisual, "Exit visual mode")
	m.registry.Add("v", exitModeVisual, ModeVisual, "Exit visual mode")
	m.registry.Add("V", exitModeVisual, ModeVisual, "Exit visual mode")
	m.registry.Add(":", enterModeCommand, ModeVisual, "Enter command mode")
	m.registry.Add("y", yankVisualSelection, ModeVisual, "Yank selection")
	m.registry.Add("d", deleteVisualSelection, ModeVisual, "Delete selection")
	m.registry.Add("x", deleteVisualSelection, ModeVisual, "Delete selection")
	m.registry.Add("p", replaceVisualSelectionWithYank, ModeVisual, "Replace with yanked text")

	m.registry.Add("esc", exitModeInsert, ModeInsert, "Exit insert mode")
	m.registry.Add("backspace", handleInsertBackspace, ModeInsert, "Backspace")
	m.registry.Add("tab", handleInsertTab, ModeInsert, "Tab")
	m.registry.Add("enter", handleInsertEnterKey, ModeInsert, "Enter")
	m.registry.Add("up", handleArrowKeys("up"), ModeInsert, "Move cursor up")
	m.registry.Add("down", handleArrowKeys("down"), ModeInsert, "Move cursor down")
	m.registry.Add("left", handleArrowKeys("left"), ModeInsert, "Move cursor left")
	m.registry.Add("right", handleArrowKeys("right"), ModeInsert, "Move cursor right")

	m.registry.Add("esc", exitModeCommand, ModeCommand, "Exit command mode")
	m.registry.Add("enter", executeCommand, ModeCommand, "Execute command")
	m.registry.Add("backspace", commandBackspace, ModeCommand, "Backspace")

	m.commands.Register("zr", toggleRelativeLineNumbers)
	m.commands.Register("clear", clearBuffer)
	m.commands.Register("reset", resetEditor)
}

func toggleRelativeLineNumbers(model *editorModel) tea.Cmd {
	model.relativeNumbers = !model.relativeNumbers
	if model.relativeNumbers {
		return SetStatusMsg("relative line numbers: on")
	} else {
		return SetStatusMsg("relative line numbers: off")
	}
}

func clearBuffer(model *editorModel) tea.Cmd {
	model.buffer.saveUndoState(model.cursor)
	model.buffer.clear()
	model.cursor = newCursor(0, 0)
	return SetStatusMsg("buffer cleared")
}

func resetEditor(model *editorModel) tea.Cmd {
	return model.Reset()
}

func moveToFirstNonWhitespace(model *editorModel) tea.Cmd {
	line := model.buffer.Line(model.cursor.Row)
	for i, char := range line {
		if char != ' ' && char != '\t' {
			model.cursor.Col = i
			model.desiredCol = model.cursor.Col
			break
		}
	}
	return nil
}

func changeToEndOfLine(model *editorModel) tea.Cmd {
	_ = deleteToEndOfLine(model)
	return enterModeInsert(model)
}

func deleteToEndOfLine(model *editorModel) tea.Cmd {
	row := model.cursor.Row
	col := model.cursor.Col
	line := model.buffer.Line(row)

	if len(line) > 0 {

		model.buffer.saveUndoState(model.cursor)

		start := Cursor{Row: row, Col: col}
		end := Cursor{Row: row, Col: len(line) - 1}

		model.yankBuffer = model.buffer.deleteRange(start, end)
		clipboard.Write(clipboard.FmtText, []byte(model.yankBuffer))
	}

	return nil
}

func exitModeCommand(model *editorModel) tea.Cmd {
	return switchMode(model, ModeNormal)
}

func exitModeVisual(model *editorModel) tea.Cmd {
	return switchMode(model, ModeNormal)
}

func exitModeInsert(model *editorModel) tea.Cmd {
	return switchMode(model, ModeNormal)
}

func enterModeInsert(model *editorModel) tea.Cmd {
	return switchMode(model, ModeInsert)
}

func enterModeCommand(model *editorModel) tea.Cmd {
	return switchMode(model, ModeCommand)
}

func beginVisualSelection(model *editorModel) tea.Cmd {
	model.visualStart = model.cursor.Clone()
	model.isVisualLine = false
	model.statusMessage = "-- VISUAL --"
	return switchMode(model, ModeVisual)
}

func beginVisualLineSelection(model *editorModel) tea.Cmd {
	model.visualStart = newCursor(model.cursor.Row, 0)
	model.isVisualLine = true
	model.statusMessage = "-- VISUAL LINE --"
	return switchMode(model, ModeVisual)
}

func appendAfterCursor(model *editorModel) tea.Cmd {
	if model.cursor.Col < model.buffer.lineLength(model.cursor.Row) {
		model.cursor.Col++
	}
	return switchMode(model, ModeInsert)
}

func appendAtEndOfLine(model *editorModel) tea.Cmd {
	model.cursor.Col = model.buffer.lineLength(model.cursor.Row)
	return switchMode(model, ModeInsert)
}

func insertAtStartOfLine(model *editorModel) tea.Cmd {
	model.cursor.Col = 0
	return switchMode(model, ModeInsert)
}

func openLineBelow(model *editorModel) tea.Cmd {
	model.buffer.saveUndoState(model.cursor)

	model.buffer.insertLine(model.cursor.Row+1, "")
	model.cursor.Row++
	model.cursor.Col = 0
	model.ensureCursorVisible()
	return switchMode(model, ModeInsert)
}

func openLineAbove(model *editorModel) tea.Cmd {
	model.buffer.saveUndoState(model.cursor)

	model.buffer.insertLine(model.cursor.Row, "")
	model.cursor.Col = 0
	model.ensureCursorVisible()
	return switchMode(model, ModeInsert)
}

func replaceCurrentCharacter(model *editorModel, char string) (tea.Model, tea.Cmd) {
	model.buffer.saveUndoState(model.cursor)
	// perform replacement
	deleteCharAtCursor(model)
	insertCharacter(model, char)

	// remove delete/insert undo states
	if len(model.buffer.undoStack) > 1 {
		model.buffer.undoStack = model.buffer.undoStack[:len(model.buffer.undoStack)-2]
	}
	// restore cursor to position of replacement
	// safe to not check len because insertCharacter increments cursor col
	model.cursor.Col--
	return model, nil
}

func insertCharacter(model *editorModel, char string) (tea.Model, tea.Cmd) {
	model.buffer.saveUndoState(model.cursor)

	if model.cursor.Col > model.buffer.lineLength(model.cursor.Row) {
		model.cursor.Col = model.buffer.lineLength(model.cursor.Row)
	}

	line := model.buffer.Line(model.cursor.Row)
	newLine := line[:model.cursor.Col] + char + line[model.cursor.Col:]
	model.buffer.setLine(model.cursor.Row, newLine)
	model.cursor.Col++

	return model, nil
}

func handleInsertBackspace(model *editorModel) tea.Cmd {
	model.buffer.saveUndoState(model.cursor)

	if model.cursor.Col > 0 {

		model.buffer.deleteAt(model.cursor.Row, model.cursor.Col-1, model.cursor.Row, model.cursor.Col-1)
		model.cursor.Col--
	} else if model.cursor.Row > 0 {

		prevLineLen := model.buffer.lineLength(model.cursor.Row - 1)

		model.buffer.deleteAt(model.cursor.Row-1, prevLineLen, model.cursor.Row, 0)

		model.cursor.Row--
		model.cursor.Col = prevLineLen
	}
	return nil
}

func handleInsertTab(model *editorModel) tea.Cmd {
	model.buffer.saveUndoState(model.cursor)

	line := model.buffer.Line(model.cursor.Row)
	newLine := line[:model.cursor.Col] + "\t" + line[model.cursor.Col:]
	model.buffer.setLine(model.cursor.Row, newLine)
	model.cursor.Col += 1
	return nil
}

func handleInsertEnterKey(m *editorModel) tea.Cmd {
	m.buffer.saveUndoState(m.cursor)

	currentLine := m.buffer.Line(m.cursor.Row)
	newLine := ""

	if m.cursor.Col < len(currentLine) {
		newLine = currentLine[m.cursor.Col:]
		m.buffer.setLine(m.cursor.Row, currentLine[:m.cursor.Col])
	}

	m.buffer.insertLine(m.cursor.Row+1, newLine)
	m.cursor.Row++
	m.cursor.Col = 0
	m.ensureCursorVisible()
	return nil
}

func moveCursorLeft(model *editorModel) tea.Cmd {
	withCountPrefix(model, func() {
		if model.cursor.Col > 0 {
			model.cursor.Col--
		}
	})
	model.desiredCol = model.cursor.Col
	return nil
}

func moveCursorDown(model *editorModel) tea.Cmd {
	withCountPrefix(model, func() {
		if model.cursor.Row < model.buffer.lineCount()-1 {
			model.cursor.Row++
			model.cursor.Col = min(model.desiredCol, model.buffer.lineLength(model.cursor.Row)-1)
		}
	})
	model.ensureCursorVisible()
	return nil
}

func moveCursorUp(model *editorModel) tea.Cmd {
	withCountPrefix(model, func() {
		if model.cursor.Row > 0 {
			model.cursor.Row--
			model.cursor.Col = min(model.desiredCol, model.buffer.lineLength(model.cursor.Row)-1)
		}
	})
	model.ensureCursorVisible()
	return nil
}

func moveCursorRight(model *editorModel) tea.Cmd {
	lineLen := model.buffer.lineLength(model.cursor.Row)

	withCountPrefix(model, func() {
		if lineLen > 0 && model.cursor.Col < lineLen-1 {
			model.cursor.Col++
		}
	})
	model.desiredCol = model.cursor.Col
	return nil
}

func moveCursorRightOrNextLine(model *editorModel) tea.Cmd {
	lineLen := model.buffer.lineLength(model.cursor.Row)
	if lineLen > 0 && model.cursor.Col < lineLen-1 {
		model.cursor.Col++
	} else if model.cursor.Row < model.buffer.lineCount()-1 {
		model.cursor.Row++
		model.cursor.Col = 0
	}
	model.desiredCol = model.cursor.Col
	model.ensureCursorVisible()
	return nil
}

func moveToStartOfLine(model *editorModel) tea.Cmd {
	model.cursor.Col = 0
	return nil
}

func moveToEndOfLine(model *editorModel) tea.Cmd {
	lineLen := model.buffer.lineLength(model.cursor.Row)
	if lineLen > 0 {
		model.cursor.Col = lineLen - 1
	} else {
		model.cursor.Col = 0
	}
	model.desiredCol = model.cursor.Col
	return nil
}

func moveToStartOfDocument(model *editorModel) tea.Cmd {
	model.cursor.Row = 0
	model.cursor.Col = min(model.desiredCol, model.buffer.lineLength(model.cursor.Row)-1)
	model.keySequence = []string{}
	model.ensureCursorVisible()
	return nil
}

func moveToEndOfDocument(model *editorModel) tea.Cmd {
	model.cursor.Row = model.buffer.lineCount() - 1
	model.cursor.Col = min(model.desiredCol, model.buffer.lineLength(model.cursor.Row)-1)
	model.keySequence = []string{}
	model.ensureCursorVisible()
	return nil
}

func handleArrowKeys(key string) func(*editorModel) tea.Cmd {
	return func(m *editorModel) tea.Cmd {
		switch key {
		case "up":
			return moveCursorUp(m)
		case "down":
			return moveCursorDown(m)
		case "left":
			return moveCursorLeft(m)
		case "right":
			return moveCursorRight(m)
		}
		return nil
	}
}

func executeCommand(model *editorModel) tea.Cmd {
	command := model.commandBuffer
	model.commandBuffer = ""
	return func() tea.Msg {
		return CommandMsg{command}
	}
}

func addCommandCharacter(model *editorModel, char string) (tea.Model, tea.Cmd) {
	model.commandBuffer += char
	return model, nil
}

func commandBackspace(model *editorModel) tea.Cmd {
	if len(model.commandBuffer) > 0 {
		model.commandBuffer = model.commandBuffer[:len(model.commandBuffer)-1]
	}
	return nil
}

func moveToNextWordStart(model *editorModel) tea.Cmd {
	currRow := model.cursor.Row
	if currRow >= model.buffer.lineCount() {
		return nil
	}

	line := model.buffer.Line(currRow)
	startPos := model.cursor.Col + 1

	if startPos >= len(line) {
		if currRow < model.buffer.lineCount()-1 {
			model.cursor.Row++
			model.cursor.Col = 0
			model.ensureCursorVisible()
		}
		model.desiredCol = model.cursor.Col
		return nil
	}

	for i := startPos; i < len(line); i++ {
		if (i == 0 || isWordSeparator(line[i-1])) && !isWordSeparator(line[i]) {
			model.cursor.Col = i
			model.desiredCol = model.cursor.Col
			return nil
		}
	}

	model.cursor.Col = max(0, len(line)-1)
	model.desiredCol = model.cursor.Col
	return nil
}

func moveToPrevWordStart(model *editorModel) tea.Cmd {
	currRow := model.cursor.Row
	if currRow >= model.buffer.lineCount() {
		return nil
	}

	line := model.buffer.Line(currRow)
	if model.cursor.Col <= 0 {
		if currRow > 0 {
			model.cursor.Row--
			prevLineLen := model.buffer.lineLength(model.cursor.Row)
			model.cursor.Col = max(0, prevLineLen-1)
			model.desiredCol = model.cursor.Col
			model.ensureCursorVisible()
		}
		return nil
	}

	for i := model.cursor.Col - 1; i >= 0; i-- {
		if (i == 0 || isWordSeparator(line[i-1])) && !isWordSeparator(line[i]) {
			model.cursor.Col = i
			model.desiredCol = model.cursor.Col
			return nil
		}
	}

	model.cursor.Col = 0
	model.desiredCol = model.cursor.Col
	return nil
}

func undo(model *editorModel) tea.Cmd {
	return model.buffer.undo(model.cursor)
}

func redo(model *editorModel) tea.Cmd {
	return model.buffer.redo(model.cursor)
}

func beginReplaceAtCursor(model *editorModel) tea.Cmd {
	deleteCharAtCursor(model)
	model.buffer.insertAt(model.cursor.Row, model.cursor.Col, " ")
  // mark waitReplace to be handled in next insert keymsg
	model.waitReplace = true
	return enterModeInsert(model)

}

func deleteCharAtCursor(model *editorModel) tea.Cmd {
	model.buffer.saveUndoState(model.cursor)

	lineLen := model.buffer.lineLength(model.cursor.Row)
	if lineLen > 0 && model.cursor.Col < lineLen {

		model.buffer.deleteAt(model.cursor.Row, model.cursor.Col, model.cursor.Row, model.cursor.Col)

		newLineLen := model.buffer.lineLength(model.cursor.Row)
		if model.cursor.Col >= newLineLen && newLineLen > 0 {
			model.cursor.Col = newLineLen - 1
		}
	}
	return nil
}

func setupYankHighlight(model *editorModel, start, end Cursor, text string, isLinewise bool) {
	model.yankBuffer = text
	clipboard.Write(clipboard.FmtText, []byte(model.yankBuffer))
	model.statusMessage = fmt.Sprintf("yanked %d characters", len(text))
	model.yankHighlight.Start = start
	model.yankHighlight.End = end
	model.yankHighlight.StartTime = time.Now()
	model.yankHighlight.IsLinewise = isLinewise
	model.yankHighlight.Active = true
}

func yankLine(model *editorModel) tea.Cmd {
	line := model.buffer.Line(model.cursor.Row)

	setupYankHighlight(
		model,
		Cursor{model.cursor.Row, 0},
		Cursor{model.cursor.Row, max(0, len(line)-1)},
		"\n"+line,
		true,
	)

	model.keySequence = []string{}
	return nil
}

func deleteLine(model *editorModel) tea.Cmd {
	model.buffer.saveUndoState(model.cursor)

	row := model.cursor.Row
	lineContent := model.buffer.Line(row)
	model.yankBuffer = "\n" + lineContent
	clipboard.Write(clipboard.FmtText, []byte(model.yankBuffer))

	model.buffer.deleteLine(row)

	if model.buffer.lineCount() == 0 {
		model.buffer.insertLine(0, "")
	}

	if model.cursor.Row >= model.buffer.lineCount() {
		model.cursor.Row = model.buffer.lineCount() - 1
	}
	if model.cursor.Col >= model.buffer.lineLength(model.cursor.Row) {
		model.cursor.Col = max(0, model.buffer.lineLength(model.cursor.Row)-1)
	}

	model.ensureCursorVisible()
	return nil
}

func pasteAfter(model *editorModel) tea.Cmd {
	data := clipboard.Read(clipboard.FmtText)
	model.yankBuffer = string(data)
	if model.yankBuffer == "" {
		return nil
	}

	model.buffer.saveUndoState(model.cursor)

	// Line-wise paste
	if strings.HasPrefix(model.yankBuffer, "\n") {
		return pasteLineAfter(model)
	}

	// Character-wise paste
	currLine := model.buffer.Line(model.cursor.Row)
	insertPos := model.cursor.Col

	// Check if the yanked text contains newlines (multi-line character-wise yank)
	if strings.Contains(model.yankBuffer, "\n") {
		// Split the yanked text by newlines
		lines := strings.Split(model.yankBuffer, "\n")

		// Handle the first line - insert at cursor position in current line
		firstLine := lines[0]
		remainderOfLine := ""
		if insertPos < len(currLine) {
			remainderOfLine = currLine[insertPos+1:]
		}

		// Set the first line with the content before cursor + first part of yanked text
		if insertPos >= len(currLine) {
			model.buffer.setLine(model.cursor.Row, currLine+firstLine)
		} else {
			model.buffer.setLine(model.cursor.Row,
				currLine[:insertPos+1]+firstLine)
		}

		// Insert middle lines as new lines
		row := model.cursor.Row
		for i := 1; i < len(lines)-1; i++ {
			model.buffer.insertLine(row+i, lines[i])
		}

		// Handle the last line separately
		if len(lines) > 1 {
			lastLine := lines[len(lines)-1]
			model.buffer.insertLine(row+len(lines)-1, lastLine+remainderOfLine)
		} else {
			// If only one line, append the remainder to the current line
			currLineContent := model.buffer.Line(model.cursor.Row)
			model.buffer.setLine(model.cursor.Row, currLineContent+remainderOfLine)
		}

		// Position cursor at the end of the last inserted line
		model.cursor.Row = row + len(lines) - 1
		if len(lines) > 1 {
			// For multi-line pastes, position at the end of the last line's content
			model.cursor.Col = len(lines[len(lines)-1])
		} else {
			// For single line pastes, position at the end of what was pasted
			model.cursor.Col = insertPos + len(firstLine) + 1
		}

		if model.mode != ModeInsert && model.cursor.Col > 0 {
			model.cursor.Col--
		}
	} else {
		// Single-line paste - original behavior
		if insertPos >= len(currLine) {
			model.buffer.setLine(model.cursor.Row, currLine+model.yankBuffer)
		} else {
			model.buffer.setLine(model.cursor.Row,
				currLine[:insertPos+1]+model.yankBuffer+currLine[insertPos+1:])
		}

		model.cursor.Col = insertPos + len(model.yankBuffer) + 1
		if model.mode != ModeInsert && model.cursor.Col > 0 {
			model.cursor.Col--
		}
	}

	model.ensureCursorVisible()
	return nil
}

func pasteBefore(model *editorModel) tea.Cmd {
	data := clipboard.Read(clipboard.FmtText)
	model.yankBuffer = string(data)
	if model.yankBuffer == "" {
		return nil
	}

	model.buffer.saveUndoState(model.cursor)

	// Line-wise paste
	if strings.HasPrefix(model.yankBuffer, "\n") {
		return pasteLineBefore(model)
	}

	// Character-wise paste
	currLine := model.buffer.Line(model.cursor.Row)
	insertPos := model.cursor.Col

	// Check if the yanked text contains newlines (multi-line character-wise yank)
	if strings.Contains(model.yankBuffer, "\n") {
		// Split the yanked text by newlines
		lines := strings.Split(model.yankBuffer, "\n")

		// Handle the first line - insert at cursor position in current line
		firstLine := lines[0]
		newFirstLine := currLine[:insertPos] + firstLine
		model.buffer.setLine(model.cursor.Row, newFirstLine)

		// If this is the last line, append the remainder of the original line
		if len(lines) == 1 {
			model.buffer.setLine(model.cursor.Row, newFirstLine+currLine[insertPos:])
		} else {
			// Handle the last line - combine with remainder of current line
			lastLineIndex := len(lines) - 1
			lastLine := lines[lastLineIndex] + currLine[insertPos:]

			// Insert middle and last lines as new lines
			row := model.cursor.Row
			for i := 1; i < lastLineIndex; i++ {
				model.buffer.insertLine(row+i, lines[i])
			}
			model.buffer.insertLine(row+lastLineIndex, lastLine)
		}

		// Position cursor appropriately depending on where the paste ended
		if len(lines) > 1 {
			// For multi-line pastes in pasteBefore, cursor stays at the insertion point
			model.cursor.Col = insertPos + len(firstLine)
		} else {
			// For single line pastes, position at the end of what was pasted
			model.cursor.Col = insertPos + len(firstLine)
		}

		if model.mode != ModeInsert && model.cursor.Col > 0 {
			model.cursor.Col--
		}
	} else {
		// Single-line paste - original behavior
		model.buffer.setLine(model.cursor.Row,
			currLine[:insertPos]+model.yankBuffer+currLine[insertPos:])

		model.cursor.Col = max(insertPos+len(model.yankBuffer)-1, 0)
	}

	model.ensureCursorVisible()
	return nil
}

func pasteLineAfter(model *editorModel) tea.Cmd {
	data := clipboard.Read(clipboard.FmtText)
	model.yankBuffer = string(data)
	lines := strings.Split(model.yankBuffer[1:], "\n")
	row := model.cursor.Row

	for i := range lines {
		model.buffer.insertLine(row+1+i, lines[i])
	}

	model.cursor.Row = row + 1
	model.cursor.Col = 0
	model.ensureCursorVisible()
	return nil
}

func pasteLineBefore(model *editorModel) tea.Cmd {
	data := clipboard.Read(clipboard.FmtText)
	model.yankBuffer = string(data)
	lines := strings.Split(model.yankBuffer[1:], "\n")
	row := model.cursor.Row

	for i := range lines {
		model.buffer.insertLine(row+i, lines[i])
	}

	model.cursor.Col = 0
	model.ensureCursorVisible()
	return nil
}

func yankVisualSelection(model *editorModel) tea.Cmd {
	start, end := model.GetSelectionBoundary()
	selectedText := model.buffer.getRange(start, end)

	if model.isVisualLine {
		selectedText = "\n" + selectedText
	}

	setupYankHighlight(model, start, end, selectedText, model.isVisualLine)
	return switchMode(model, ModeNormal)
}

func deleteVisualSelection(model *editorModel) tea.Cmd {
	model.buffer.saveUndoState(model.cursor)
	start, end := model.GetSelectionBoundary()

	selectedText := model.buffer.getRange(start, end)
	if model.isVisualLine {
		selectedText = "\n" + selectedText
	}
	model.yankBuffer = selectedText
	clipboard.Write(clipboard.FmtText, []byte(model.yankBuffer))

	model.buffer.deleteRange(start, end)

	model.cursor = start
	model.ensureCursorVisible()

	return switchMode(model, ModeNormal)
}

func replaceVisualSelectionWithYank(model *editorModel) tea.Cmd {
	model.buffer.saveUndoState(model.cursor)
	start, end := model.GetSelectionBoundary()
	oldSelection := model.buffer.deleteRange(start, end)
	model.yankBuffer = oldSelection
	clipboard.Write(clipboard.FmtText, []byte(model.yankBuffer))

	model.cursor = start

	if strings.Contains(model.yankBuffer, "\n") {
		pasteLineBefore(model)
	} else {
		currLine := model.buffer.Line(model.cursor.Row)
		insertPos := model.cursor.Col
		model.buffer.setLine(model.cursor.Row,
			currLine[:insertPos]+model.yankBuffer+currLine[insertPos:])
		model.cursor.Col = max(insertPos+len(model.yankBuffer)-1, 0)
	}

	model.ensureCursorVisible()
	return switchMode(model, ModeNormal)
}

func performWordOperation(model *editorModel, operation string) tea.Cmd {
	start, end := getWordBoundary(model)
	if start == end {
		return nil
	}

	word := model.buffer.Line(model.cursor.Row)[start:end]

	if operation == "delete" || operation == "change" {
		model.buffer.saveUndoState(model.cursor)
	}

	model.yankBuffer = word
	clipboard.Write(clipboard.FmtText, []byte(model.yankBuffer))

	switch operation {
	case "yank":
		model.statusMessage = fmt.Sprintf("yanked word: %s", model.yankBuffer)
		model.yankHighlight.Start = Cursor{model.cursor.Row, start}
		model.yankHighlight.End = Cursor{model.cursor.Row, end - 1}
		model.yankHighlight.StartTime = time.Now()
		model.yankHighlight.IsLinewise = false
		model.yankHighlight.Active = true
	case "delete", "change":

		line := model.buffer.Line(model.cursor.Row)
		newLine := line[:start] + line[end:]
		model.buffer.setLine(model.cursor.Row, newLine)
		model.cursor.Col = start

		if operation == "change" {
			return switchMode(model, ModeInsert)
		}
	}

	model.keySequence = []string{}
	return nil
}

func deleteInnerWord(model *editorModel) tea.Cmd {
	return performWordOperation(model, "delete")
}

func yankInnerWord(model *editorModel) tea.Cmd {
	return performWordOperation(model, "yank")
}

func changeInnerWord(model *editorModel) tea.Cmd {
	return performWordOperation(model, "change")
}

func getWordBoundary(model *editorModel) (int, int) {
	line := model.buffer.Line(model.cursor.Row)
	if len(line) == 0 {
		return 0, 0
	}

	col := model.cursor.Col
	if col >= len(line) {
		col = len(line) - 1
	}

	start := col

	if isWordSeparator(line[col]) {
		for start > 0 && isWordSeparator(line[start-1]) {
			start--
		}
	} else {
		for start > 0 && !isWordSeparator(line[start-1]) {
			start--
		}
	}

	end := col

	if isWordSeparator(line[col]) {
		for end < len(line)-1 && isWordSeparator(line[end+1]) {
			end++
		}
	} else {
		for end < len(line)-1 && !isWordSeparator(line[end+1]) {
			end++
		}
	}

	return start, end + 1
}

func isWordSeparator(ch byte) bool {
	return ch == ' ' || ch == '\t' || ch == '.' || ch == ',' ||
		ch == ';' || ch == ':' || ch == '!' || ch == '?' ||
		ch == '(' || ch == ')' || ch == '[' || ch == ']' ||
		ch == '{' || ch == '}' || ch == '<' || ch == '>' ||
		ch == '/' || ch == '\\' || ch == '+' || ch == '-' ||
		ch == '*' || ch == '&' || ch == '^' || ch == '%' ||
		ch == '$' || ch == '#' || ch == '@' || ch == '=' ||
		ch == '|' || ch == '`' || ch == '~' || ch == '"' ||
		ch == '\''
}

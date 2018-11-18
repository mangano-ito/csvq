package cmd

import (
	"bytes"
	"strings"

	"github.com/mithrandie/go-text/color"
)

const (
	DefaultLineWidth = 75
	DefaultPadding   = 1
)

type ObjectWriter struct {
	Palette *color.Palette

	MaxWidth    int
	Padding     int
	Indent      int
	IndentWidth int

	Title1       string
	Title1Effect string
	Title2       string
	Title2Effect string

	buf bytes.Buffer

	subBlock  int
	lineWidth int
	column    int
}

func NewObjectWriter() *ObjectWriter {
	maxWidth := DefaultLineWidth
	if Terminal != nil {
		if termw, _, err := Terminal.GetSize(); err == nil {
			maxWidth = termw
		}
	}

	palette, _ := GetPalette()

	return &ObjectWriter{
		MaxWidth:    maxWidth,
		Indent:      0,
		IndentWidth: 4,
		Padding:     DefaultPadding,
		Palette:     palette,
		lineWidth:   0,
		column:      0,
		subBlock:    0,
	}
}

func (w *ObjectWriter) Clear() {
	w.Title1 = ""
	w.Title1Effect = ""
	w.Title2 = ""
	w.Title2Effect = ""
	w.lineWidth = 0
	w.column = 0
	w.subBlock = 0
	w.buf.Reset()
}

func (w *ObjectWriter) WriteColorWithoutLineBreak(s string, effect string) {
	w.write(s, effect, true)
}

func (w *ObjectWriter) WriteColor(s string, effect string) {
	w.write(s, effect, false)
}

func (w *ObjectWriter) write(s string, effect string, withoutLineBreak bool) {
	startOfLine := w.column < 1

	if startOfLine {
		width := w.leadingSpacesWidth() + w.subBlock
		w.writeToBuf(strings.Repeat(" ", width))
		w.column = width
	}

	if !withoutLineBreak && !startOfLine && !w.FitInLine(s) {
		w.NewLine()
		w.write(s, effect, withoutLineBreak)
	} else {
		if w.Palette == nil {
			w.writeToBuf(s)
		} else {
			w.writeToBuf(w.Palette.Render(effect, s))
		}
		w.column = w.column + TextWidth(s)
	}
}

func (w *ObjectWriter) writeToBuf(s string) {
	w.buf.WriteString(s)
}

func (w *ObjectWriter) leadingSpacesWidth() int {
	return w.Padding + (w.Indent * w.IndentWidth)
}

func (w *ObjectWriter) FitInLine(s string) bool {
	if w.MaxWidth-w.Padding < w.column+TextWidth(s) {
		return false
	}
	return true
}

func (w *ObjectWriter) WriteWithoutLineBreak(s string) {
	w.WriteColorWithoutLineBreak(s, NoEffect)
}

func (w *ObjectWriter) Write(s string) {
	w.WriteColor(s, NoEffect)
}

func (w *ObjectWriter) WriteSpaces(l int) {
	w.Write(strings.Repeat(" ", l))
}

func (w *ObjectWriter) NewLine() {
	w.buf.WriteRune('\n')
	if w.lineWidth < w.column {
		w.lineWidth = w.column
	}
	w.column = 0
}

func (w *ObjectWriter) BeginBlock() {
	w.Indent++
}

func (w *ObjectWriter) EndBlock() {
	w.Indent--
}

func (w *ObjectWriter) BeginSubBlock() {
	w.subBlock = w.column - w.leadingSpacesWidth()
}

func (w *ObjectWriter) EndSubBlock() {
	w.subBlock = 0
}

func (w *ObjectWriter) ClearBlock() {
	w.Indent = 0
}

func (w *ObjectWriter) String() string {
	var header bytes.Buffer
	if 0 < len(w.Title1) || 0 < len(w.Title2) {
		tw := TextWidth(w.Title1) + TextWidth(w.Title2)
		if 0 < len(w.Title1) && 0 < len(w.Title2) {
			tw++
		}

		hlLen := tw + 2
		if hlLen < w.lineWidth+1 {
			hlLen = w.lineWidth + 1
		}
		if hlLen < w.column+1 {
			hlLen = w.column + 1
		}
		if w.MaxWidth < hlLen {
			hlLen = w.MaxWidth
		}

		if tw < hlLen {
			header.Write(bytes.Repeat([]byte(" "), (hlLen-tw)/2))
		}
		if 0 < len(w.Title1) {
			if w.Palette == nil {
				header.WriteString(w.Title1)
			} else {
				header.WriteString(w.Palette.Render(w.Title1Effect, w.Title1))
			}
		}
		if 0 < len(w.Title2) {
			header.WriteRune(' ')
			if w.Palette == nil {
				header.WriteString(w.Title2)
			} else {
				header.WriteString(w.Palette.Render(w.Title2Effect, w.Title2))
			}
		}
		header.WriteRune('\n')
		header.Write(bytes.Repeat([]byte("-"), hlLen))
		header.WriteRune('\n')
	}

	return header.String() + w.buf.String()
}
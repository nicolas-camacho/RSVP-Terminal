package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/spinner"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

type appState int

const (
	stateFilePicker appState = iota
	stateLoading
	stateNavigator
	stateReady
	stateReading
	stateDone
)

const progressFile = "books/.progress"

func orpIndex(word string) int {
	n := len([]rune(word))
	switch {
	case n <= 3:
		return 0
	case n <= 5:
		return 1
	case n <= 9:
		return 2
	case n <= 13:
		return 3
	default:
		return 4
	}
}

type tickMsg struct{}

type wordsLoadedMsg struct {
	words []string
	err   error
}

func loadWordsCmd(path string) tea.Cmd {
	return func() tea.Msg {
		if isCacheable(path) {
			if words, ok := loadFromCache(path); ok {
				return wordsLoadedMsg{words: words}
			}
		}
		words, err := loadWords(path)
		if err != nil {
			return wordsLoadedMsg{err: err}
		}
		if isCacheable(path) {
			saveToCache(path, words)
		}
		return wordsLoadedMsg{words: words}
	}
}

type model struct {
	bookFiles    []string
	bookCursor   int
	bookProgress map[string]int
	bookPath     string

	words     []string
	index     int
	navCursor int
	prevState appState

	spinner       spinner.Model
	wpm           int
	fontSize      int
	longWordBonus int // extra % delay for words with 9+ characters
	paused        bool
	state         appState
	width         int
	height        int
}

// wordDelay returns the display duration for a word, adding longWordBonus %
// extra time for words with 9 or more characters.
func wordDelay(wpm, bonusPct int, word string) time.Duration {
	base := time.Minute / time.Duration(wpm)
	if bonusPct > 0 && len([]rune(word)) >= 9 {
		return base + base*time.Duration(bonusPct)/100
	}
	return base
}

func nextTick(wpm int) tea.Cmd {
	return tea.Tick(time.Minute/time.Duration(wpm), func(time.Time) tea.Msg {
		return tickMsg{}
	})
}

func nextWordTick(wpm, bonusPct int, word string) tea.Cmd {
	return tea.Tick(wordDelay(wpm, bonusPct, word), func(time.Time) tea.Msg {
		return tickMsg{}
	})
}

func clamp(v, lo, hi int) int {
	if v < lo {
		return lo
	}
	if v > hi {
		return hi
	}
	return v
}

// wrapWords wraps word indices into lines of at most lineWidth visible chars.
// Returns lines (each = list of word indices) and wordToLine mapping.
func wrapWords(words []string, lineWidth int) (lines [][]int, wordToLine []int) {
	wordToLine = make([]int, len(words))
	lineNum := 0
	var lineWords []int
	lineLen := 0

	for i, w := range words {
		wLen := len([]rune(w))
		sep := 0
		if len(lineWords) > 0 {
			sep = 1
		}
		if len(lineWords) > 0 && lineLen+sep+wLen > lineWidth {
			lines = append(lines, lineWords)
			lineNum++
			lineWords = []int{i}
			lineLen = wLen
		} else {
			lineWords = append(lineWords, i)
			lineLen += sep + wLen
		}
		wordToLine[i] = lineNum
	}
	if len(lineWords) > 0 {
		lines = append(lines, lineWords)
	}
	return
}

// ── progress persistence ──────────────────────────────────────────────────────

func loadProgress() map[string]int {
	data, err := os.ReadFile(progressFile)
	if err != nil {
		return make(map[string]int)
	}
	var p map[string]int
	if err := json.Unmarshal(data, &p); err != nil {
		return make(map[string]int)
	}
	return p
}

func saveProgress(p map[string]int) {
	data, _ := json.Marshal(p)
	_ = os.WriteFile(progressFile, data, 0644)
}

// ── init / update ─────────────────────────────────────────────────────────────

func (m model) Init() tea.Cmd {
	return nil
}

func (m model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height

	case spinner.TickMsg:
		if m.state == stateLoading {
			var cmd tea.Cmd
			m.spinner, cmd = m.spinner.Update(msg)
			return m, cmd
		}

	case wordsLoadedMsg:
		if msg.err != nil {
			m.state = stateFilePicker
			return m, nil
		}
		m.words = msg.words
		m.index = m.bookProgress[m.bookPath]
		m.navCursor = m.index
		m.prevState = stateFilePicker
		m.state = stateNavigator

	case tickMsg:
		if m.state != stateReading {
			return m, nil
		}
		if m.paused {
			return m, nextTick(m.wpm)
		}
		m.index++
		if m.index >= len(m.words) {
			m.bookProgress[m.bookPath] = 0
			saveProgress(m.bookProgress)
			m.state = stateDone
			return m, nil
		}
		return m, nextWordTick(m.wpm, m.longWordBonus, m.words[m.index])

	case tea.KeyMsg:
		switch m.state {

		case stateFilePicker:
			switch msg.String() {
			case "ctrl+c", "q":
				return m, tea.Quit
			case "up", "k":
				if m.bookCursor > 0 {
					m.bookCursor--
				}
			case "down", "j":
				if m.bookCursor < len(m.bookFiles)-1 {
					m.bookCursor++
				}
			case "enter", " ":
				m.bookPath = m.bookFiles[m.bookCursor]
				m.spinner = spinner.New()
				m.spinner.Spinner = spinner.Points
				m.spinner.Style = lipgloss.NewStyle().Foreground(lipgloss.Color("#9B59F5"))
				m.state = stateLoading
				return m, tea.Batch(m.spinner.Tick, loadWordsCmd(m.bookPath))
			}

		case stateLoading:
			if msg.String() == "ctrl+c" || msg.String() == "q" {
				return m, tea.Quit
			}

		case stateNavigator:
			lineWidth := clamp(m.width-4, 10, m.width)
			navLines, wordToLine := wrapWords(m.words, lineWidth)

			switch msg.String() {
			case "ctrl+c", "q":
				saveProgress(m.bookProgress)
				return m, tea.Quit
			case "esc":
				if m.prevState == stateReading {
					m.state = stateReading
					return m, nextWordTick(m.wpm, m.longWordBonus, m.words[m.index])
				}
				m.state = m.prevState
			case "enter", "s", "S":
				m.index = m.navCursor
				if m.prevState == stateReading {
					m.state = stateReading
					return m, nextWordTick(m.wpm, m.longWordBonus, m.words[m.index])
				}
				m.state = stateReady
			case "left", "h":
				if m.navCursor > 0 {
					m.navCursor--
				}
			case "right", "l":
				if m.navCursor < len(m.words)-1 {
					m.navCursor++
				}
			case "up", "k":
				cl := wordToLine[m.navCursor]
				if cl > 0 {
					for pi, wi := range navLines[cl] {
						if wi == m.navCursor {
							prev := navLines[cl-1]
							m.navCursor = prev[clamp(pi, 0, len(prev)-1)]
							break
						}
					}
				}
			case "down", "j":
				cl := wordToLine[m.navCursor]
				if cl < len(navLines)-1 {
					for pi, wi := range navLines[cl] {
						if wi == m.navCursor {
							next := navLines[cl+1]
							m.navCursor = next[clamp(pi, 0, len(next)-1)]
							break
						}
					}
				}
			case "g":
				m.navCursor = 0
			case "G":
				m.navCursor = len(m.words) - 1
			}
			_ = navLines

		case stateReady:
			switch msg.String() {
			case "ctrl+c", "q":
				return m, tea.Quit
			case "esc":
				m.state = stateFilePicker
				return m, nil
			case "n":
				m.navCursor = m.index
				m.prevState = stateReady
				m.state = stateNavigator
			case "s", "S":
				m.state = stateReading
				return m, nextWordTick(m.wpm, m.longWordBonus, m.words[m.index])
			case "+", "=":
				m.wpm += 25
			case "-":
				if m.wpm > 50 {
					m.wpm -= 25
				}
			case "]":
				if m.fontSize < 5 {
					m.fontSize++
				}
			case "[":
				if m.fontSize > 1 {
					m.fontSize--
				}
			case ".":
				if m.longWordBonus < 50 {
					m.longWordBonus += 5
				}
			case ",":
				if m.longWordBonus > 0 {
					m.longWordBonus -= 5
				}
			}

		case stateReading:
			switch msg.String() {
			case "ctrl+c", "q":
				m.bookProgress[m.bookPath] = m.index
				saveProgress(m.bookProgress)
				return m, tea.Quit
			case " ":
				m.paused = !m.paused
			case "+", "=":
				m.wpm += 25
			case "-":
				if m.wpm > 50 {
					m.wpm -= 25
				}
			case "left":
				if m.index > 0 {
					m.index--
				}
			case "right":
				if m.index < len(m.words)-1 {
					m.index++
				}
			case ".":
				if m.longWordBonus < 50 {
					m.longWordBonus += 5
				}
			case ",":
				if m.longWordBonus > 0 {
					m.longWordBonus -= 5
				}
			case "n":
				m.bookProgress[m.bookPath] = m.index
				saveProgress(m.bookProgress)
				m.navCursor = m.index
				m.prevState = stateReading
				m.state = stateNavigator
				return m, nil
			case "r":
				m.index = 0
				m.state = stateReady
				return m, nil
			case "esc":
				m.bookProgress[m.bookPath] = m.index
				saveProgress(m.bookProgress)
				m.index = 0
				m.state = stateFilePicker
				return m, nil
			}

		case stateDone:
			switch msg.String() {
			case "ctrl+c", "q":
				return m, tea.Quit
			case "enter", " ":
				m.index = 0
				m.state = stateReady
			case "r":
				m.index = 0
				m.state = stateFilePicker
			}
		}
	}
	return m, nil
}

// ── styles ────────────────────────────────────────────────────────────────────

var (
	orpStyle          = lipgloss.NewStyle().Foreground(lipgloss.Color("#FF3333")).Bold(true)
	wordStyle         = lipgloss.NewStyle().Foreground(lipgloss.Color("#F0F0F0")).Bold(true)
	guideStyle        = lipgloss.NewStyle().Foreground(lipgloss.Color("#444466"))
	progressFill      = lipgloss.NewStyle().Foreground(lipgloss.Color("#7B2FBE"))
	progressEmpty     = lipgloss.NewStyle().Foreground(lipgloss.Color("#333333"))
	infoStyle         = lipgloss.NewStyle().Foreground(lipgloss.Color("#888888"))
	pauseStyle        = lipgloss.NewStyle().Foreground(lipgloss.Color("#FFB347")).Bold(true)
	doneStyle         = lipgloss.NewStyle().Foreground(lipgloss.Color("#44DD88")).Bold(true)
	titleStyle        = lipgloss.NewStyle().Foreground(lipgloss.Color("#9B59F5")).Bold(true)
	labelStyle        = lipgloss.NewStyle().Foreground(lipgloss.Color("#AAAAAA"))
	valueStyle        = lipgloss.NewStyle().Foreground(lipgloss.Color("#FFFFFF")).Bold(true)
	dimStyle          = lipgloss.NewStyle().Foreground(lipgloss.Color("#555555"))
	startStyle        = lipgloss.NewStyle().Foreground(lipgloss.Color("#44DD88")).Bold(true)
	selectedItemStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("#9B59F5")).Bold(true)
	normalItemStyle   = lipgloss.NewStyle().Foreground(lipgloss.Color("#AAAAAA"))
	navCursorStyle    = lipgloss.NewStyle().Foreground(lipgloss.Color("#FF3333")).Bold(true).Underline(true)
	navReadStyle      = lipgloss.NewStyle().Foreground(lipgloss.Color("#555577"))
	panelStyle        = lipgloss.NewStyle().
				Border(lipgloss.RoundedBorder()).
				BorderForeground(lipgloss.Color("#7B2FBE")).
				Padding(1, 4)
)

// ── helpers ───────────────────────────────────────────────────────────────────

func centerLine(s string, width int) string {
	w := lipgloss.Width(s)
	pad := (width - w) / 2
	if pad <= 0 {
		return s
	}
	return strings.Repeat(" ", pad) + s
}

func padRight(s string, w int) string {
	if len(s) >= w {
		return s
	}
	return s + strings.Repeat(" ", w-len(s))
}

func displayName(path string) string {
	base := filepath.Base(path)
	name := strings.TrimSuffix(base, ".txt")
	return strings.ReplaceAll(name, "_", " ")
}

func renderWordSpaced(word string, spacing, centerX int) string {
	runes := []rune(word)
	if len(runes) == 0 {
		return ""
	}
	orp := clamp(orpIndex(word), 0, len(runes)-1)
	sep := strings.Repeat(" ", spacing)
	orpCol := orp * (1 + spacing)
	leftPad := clamp(centerX-orpCol, 0, centerX)

	var sb strings.Builder
	sb.WriteString(strings.Repeat(" ", leftPad))
	for i, r := range runes {
		if i > 0 && spacing > 0 {
			sb.WriteString(sep)
		}
		if i == orp {
			sb.WriteString(orpStyle.Render(string(r)))
		} else {
			sb.WriteString(wordStyle.Render(string(r)))
		}
	}
	return sb.String()
}

// ── views ─────────────────────────────────────────────────────────────────────

func (m model) viewLoading() string {
	if m.width == 0 || m.height == 0 {
		return ""
	}

	name := displayName(m.bookPath)
	ext := strings.ToUpper(strings.TrimPrefix(filepath.Ext(m.bookPath), "."))

	const innerW = 42
	div := dimStyle.Render(strings.Repeat("─", innerW))

	content := strings.Join([]string{
		"",
		centerLine(titleStyle.Render("RSVP  Terminal"), innerW),
		"",
		div,
		"",
		centerLine(m.spinner.View()+"  "+valueStyle.Render(name), innerW),
		centerLine(dimStyle.Render("Procesando "+ext+"..."), innerW),
		"",
		div,
		"",
	}, "\n")

	box := panelStyle.Render(content)
	boxLines := strings.Split(box, "\n")
	topPad := clamp((m.height-len(boxLines))/2, 0, m.height)
	leftPad := clamp((m.width-lipgloss.Width(box))/2, 0, m.width)
	leftStr := strings.Repeat(" ", leftPad)

	var out []string
	for i := 0; i < topPad; i++ {
		out = append(out, "")
	}
	for _, l := range boxLines {
		out = append(out, leftStr+l)
	}
	return strings.Join(out, "\n")
}

func (m model) viewFilePicker() string {
	if m.width == 0 || m.height == 0 {
		return ""
	}
	const innerW = 42
	div := dimStyle.Render(strings.Repeat("─", innerW))

	var items []string
	for i, f := range m.bookFiles {
		name := displayName(f)
		suffix := ""
		if m.bookProgress[f] > 0 {
			suffix = dimStyle.Render("  ·  progreso guardado")
		}
		if i == m.bookCursor {
			items = append(items, "  "+selectedItemStyle.Render("▶  "+name)+suffix)
		} else {
			items = append(items, "     "+normalItemStyle.Render(name)+suffix)
		}
	}

	content := strings.Join([]string{
		"",
		centerLine(titleStyle.Render("RSVP  Terminal"), innerW),
		centerLine(dimStyle.Render("Selecciona un libro"), innerW),
		"",
		div,
		"",
		strings.Join(items, "\n"),
		"",
		div,
		"",
		centerLine(dimStyle.Render("↑↓: navegar   Enter: seleccionar   q: salir"), innerW),
		"",
	}, "\n")

	box := panelStyle.Render(content)
	boxLines := strings.Split(box, "\n")
	topPad := clamp((m.height-len(boxLines))/2, 0, m.height)
	leftPad := clamp((m.width-lipgloss.Width(box))/2, 0, m.width)
	leftStr := strings.Repeat(" ", leftPad)

	var out []string
	for i := 0; i < topPad; i++ {
		out = append(out, "")
	}
	for _, l := range boxLines {
		out = append(out, leftStr+l)
	}
	return strings.Join(out, "\n")
}

func (m model) viewNavigator() string {
	if m.width == 0 || m.height == 0 {
		return ""
	}

	const headerH = 2
	const footerH = 2
	const hPad = 2
	viewH := clamp(m.height-headerH-footerH, 1, m.height)
	lineWidth := clamp(m.width-hPad*2, 10, m.width)

	navLines, wordToLine := wrapWords(m.words, lineWidth)

	// Scroll: keep cursor centered in viewport
	cursorLine := wordToLine[m.navCursor]
	scroll := cursorLine - viewH/2
	scroll = clamp(scroll, 0, clamp(len(navLines)-viewH, 0, len(navLines)))

	// ── header ──
	name := titleStyle.Render(displayName(m.bookPath))
	pos := infoStyle.Render(fmt.Sprintf("palabra %d / %d", m.navCursor+1, len(m.words)))
	gap := clamp(m.width-lipgloss.Width(name)-lipgloss.Width(pos), 1, m.width)
	header := name + strings.Repeat(" ", gap) + pos
	separator := dimStyle.Render(strings.Repeat("─", m.width))

	var out []string
	out = append(out, header, separator)

	// ── text body ──
	pad := strings.Repeat(" ", hPad)
	endLine := clamp(scroll+viewH, 0, len(navLines))
	for li := scroll; li < endLine; li++ {
		var parts []string
		for _, wi := range navLines[li] {
			w := m.words[wi]
			switch {
			case wi == m.navCursor:
				parts = append(parts, navCursorStyle.Render(w))
			case wi < m.index:
				parts = append(parts, navReadStyle.Render(w))
			default:
				parts = append(parts, normalItemStyle.Render(w))
			}
		}
		out = append(out, pad+strings.Join(parts, " "))
	}

	// Pad remaining viewport rows
	for len(out) < headerH+viewH {
		out = append(out, "")
	}

	// ── footer ──
	var hint string
	if m.prevState == stateReading {
		hint = "←→:palabra  ↑↓:línea  g/G:inicio/fin  Enter:saltar aquí  Esc:reanudar"
	} else {
		hint = "←→:palabra  ↑↓:línea  g/G:inicio/fin  Enter:leer desde aquí  Esc:volver"
	}
	out = append(out, separator, infoStyle.Render(hint))

	return strings.Join(out, "\n")
}

func (m model) viewReady() string {
	if m.width == 0 || m.height == 0 {
		return ""
	}
	const innerW = 42
	div := dimStyle.Render(strings.Repeat("─", innerW))

	wpmRow := "  " + labelStyle.Render(padRight("Velocidad:", 16)) +
		"  " + valueStyle.Render(padRight(fmt.Sprintf("%d WPM", m.wpm), 9)) +
		dimStyle.Render("  [ − ]  [ + ]")

	sizeRow := "  " + labelStyle.Render(padRight("Tamaño fuente:", 16)) +
		"  " + valueStyle.Render(padRight(fmt.Sprintf("%d / 5", m.fontSize), 9)) +
		dimStyle.Render("  [ [ ]  [ ] ]")

	bonusRow := "  " + labelStyle.Render(padRight("Pausa en largas:", 16)) +
		"  " + valueStyle.Render(padRight(fmt.Sprintf("%d %%", m.longWordBonus), 9)) +
		dimStyle.Render("  [ , ]  [ . ]")

	const previewWord = "ejemplo"
	spacing := m.fontSize - 1
	runes := []rune(previewWord)
	orp := clamp(orpIndex(previewWord), 0, len(runes)-1)
	sep := strings.Repeat(" ", spacing)
	var parts []string
	for i, r := range runes {
		if i == orp {
			parts = append(parts, orpStyle.Render(string(r)))
		} else {
			parts = append(parts, wordStyle.Render(string(r)))
		}
	}
	preview := strings.Join(parts, sep)

	content := strings.Join([]string{
		"",
		centerLine(titleStyle.Render("RSVP  Terminal"), innerW),
		centerLine(dimStyle.Render(displayName(m.bookPath)), innerW),
		"",
		div,
		"",
		wpmRow,
		"",
		sizeRow,
		"",
		bonusRow,
		"",
		div,
		"",
		centerLine(dimStyle.Render("vista previa"), innerW),
		"",
		centerLine(preview, innerW),
		"",
		div,
		"",
		centerLine(startStyle.Render("[ S ]  Comenzar lectura"), innerW),
		"",
		centerLine(dimStyle.Render("n: navegar texto   esc: cambiar libro   q: salir"), innerW),
		"",
	}, "\n")

	box := panelStyle.Render(content)
	boxLines := strings.Split(box, "\n")
	topPad := clamp((m.height-len(boxLines))/2, 0, m.height)
	leftPad := clamp((m.width-lipgloss.Width(box))/2, 0, m.width)
	leftStr := strings.Repeat(" ", leftPad)

	var out []string
	for i := 0; i < topPad; i++ {
		out = append(out, "")
	}
	for _, l := range boxLines {
		out = append(out, leftStr+l)
	}
	return strings.Join(out, "\n")
}

func (m model) viewReading() string {
	if m.width == 0 || m.height == 0 {
		return ""
	}
	centerX := m.width / 2
	centerY := m.height / 2
	spacing := m.fontSize - 1

	lines := make([]string, m.height)

	barWidth := clamp(m.width-2, 1, m.width)
	filled := clamp(int(float64(barWidth)*float64(m.index+1)/float64(len(m.words))), 0, barWidth)
	lines[0] = " " + progressFill.Render(strings.Repeat("█", filled)) +
		progressEmpty.Render(strings.Repeat("░", barWidth-filled))

	lines[centerY] = renderWordSpaced(m.words[m.index], spacing, centerX)

	const half = 12
	guidePad := strings.Repeat(" ", clamp(centerX-half, 0, m.width))
	if centerY-1 > 0 {
		lines[centerY-1] = guidePad + guideStyle.Render(strings.Repeat("─", half)+"┬"+strings.Repeat("─", half))
	}
	if centerY+1 < m.height-2 {
		lines[centerY+1] = guidePad + guideStyle.Render(strings.Repeat("─", half)+"┴"+strings.Repeat("─", half))
	}

	var left string
	if m.paused {
		left = pauseStyle.Render("⏸ PAUSA") + "  " +
			infoStyle.Render(fmt.Sprintf("%d WPM  %d/%d", m.wpm, m.index+1, len(m.words)))
	} else {
		left = infoStyle.Render(fmt.Sprintf("▶ %d WPM  %d/%d", m.wpm, m.index+1, len(m.words)))
	}
	right := infoStyle.Render("space:pausa  ±:vel  ,/.:largas  ←→:nav  n:texto  r:config  q:salir")
	gap := clamp(m.width-lipgloss.Width(left)-lipgloss.Width(right), 1, m.width)
	lines[m.height-1] = left + strings.Repeat(" ", gap) + right

	return strings.Join(lines, "\n")
}

func (m model) viewDone() string {
	if m.width == 0 || m.height == 0 {
		return ""
	}
	lines := make([]string, m.height)
	cy := m.height / 2
	lines[cy-1] = centerLine(doneStyle.Render("✓  Lectura completada"), m.width)
	lines[cy+1] = centerLine(dimStyle.Render("Enter: releer   r: elegir libro   q: salir"), m.width)
	return strings.Join(lines, "\n")
}

func (m model) View() string {
	switch m.state {
	case stateFilePicker:
		return m.viewFilePicker()
	case stateLoading:
		return m.viewLoading()
	case stateNavigator:
		return m.viewNavigator()
	case stateReady:
		return m.viewReady()
	case stateReading:
		return m.viewReading()
	case stateDone:
		return m.viewDone()
	}
	return ""
}

// ── main ──────────────────────────────────────────────────────────────────────

func loadWords(filePath string) ([]string, error) {
	switch strings.ToLower(filepath.Ext(filePath)) {
	case ".pdf":
		return parsePDF(filePath)
	case ".epub":
		return parseEPUB(filePath)
	default:
		data, err := os.ReadFile(filePath)
		if err != nil {
			return nil, err
		}
		words := strings.Fields(string(data))
		if len(words) == 0 {
			return nil, fmt.Errorf("archivo vacío")
		}
		return words, nil
	}
}

func scanBooks(dir string) ([]string, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}
	var files []string
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		switch strings.ToLower(filepath.Ext(e.Name())) {
		case ".txt", ".pdf", ".epub":
			files = append(files, filepath.Join(dir, e.Name()))
		}
	}
	return files, nil
}

func main() {
	books, err := scanBooks("books")
	if err != nil || len(books) == 0 {
		fmt.Fprintln(os.Stderr, "No se encontraron archivos .txt en la carpeta 'books/'")
		os.Exit(1)
	}

	p := tea.NewProgram(
		model{
			bookFiles:     books,
			bookProgress:  loadProgress(),
			wpm:           300,
			fontSize:      1,
			longWordBonus: 5,
			state:         stateFilePicker,
		},
		tea.WithAltScreen(),
	)
	if _, err := p.Run(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

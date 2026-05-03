package main

import (
	"archive/zip"
	"bytes"
	"encoding/xml"
	"fmt"
	"io"
	"math"
	"os"
	"path"
	"sort"
	"strings"

	"github.com/ledongthuc/pdf"
	"golang.org/x/net/html"
)

// Pause multipliers applied to the last word before each boundary.
const (
	factorClause    = 1.5 // after , ; :
	factorSentence  = 2.5 // after . ! ? …
	factorParagraph = 3.5 // after paragraph / section break
)

// ── pause factors ─────────────────────────────────────────────────────────────

// computePauseFactors converts a raw word slice (where "" marks a paragraph
// break) into clean words and a per-word pause multiplier.
// The multiplier for word i controls how long it stays on screen.
func computePauseFactors(raw []string) (words []string, factors []float64) {
	for _, w := range raw {
		if w == "" {
			// Upgrade the previous word's factor to paragraph level.
			if len(factors) > 0 && factors[len(factors)-1] < factorParagraph {
				factors[len(factors)-1] = factorParagraph
			}
			continue
		}
		words = append(words, w)
		factors = append(factors, punctuationFactor(w))
	}
	return
}

func countWords(raw []string) int {
	n := 0
	for _, w := range raw {
		if w != "" {
			n++
		}
	}
	return n
}

// punctuationFactor returns the pause multiplier implied by a word's trailing
// punctuation. Closing quotes/brackets are skipped to reach the real mark.
func punctuationFactor(word string) float64 {
	runes := []rune(word)
	for i := len(runes) - 1; i >= 0; i-- {
		switch runes[i] {
		case '.', '!', '?', '…':
			return factorSentence
		case ',', ';', ':':
			return factorClause
		case '"', '\'', ')', ']', '”', '’', '»':
			continue // skip closing punctuation and look further left
		default:
			return 1.0
		}
	}
	return 1.0
}

// ── TXT ───────────────────────────────────────────────────────────────────────

func loadTxt(filePath string) ([]string, error) {
	data, err := os.ReadFile(filePath)
	if err != nil {
		return nil, err
	}
	text := strings.ReplaceAll(string(data), "\r\n", "\n")
	text = strings.ReplaceAll(text, "\r", "\n")
	return paragraphsToWords(text), nil
}

// paragraphsToWords splits text on double (or more) newlines and inserts ""
// paragraph sentinels between non-empty paragraphs.
func paragraphsToWords(text string) []string {
	// Collapse 3+ newlines → 2.
	for strings.Contains(text, "\n\n\n") {
		text = strings.ReplaceAll(text, "\n\n\n", "\n\n")
	}
	parts := strings.Split(text, "\n\n")

	var raw []string
	first := true
	for _, p := range parts {
		ws := strings.Fields(p)
		if len(ws) == 0 {
			continue
		}
		if !first {
			raw = append(raw, "") // paragraph sentinel
		}
		raw = append(raw, ws...)
		first = false
	}
	return raw
}

// ── PDF ───────────────────────────────────────────────────────────────────────

func parsePDF(filePath string) ([]string, error) {
	f, r, err := pdf.Open(filePath)
	if err != nil {
		return nil, fmt.Errorf("no se pudo abrir el PDF: %w", err)
	}
	defer f.Close()

	var raw []string

	for pageNum := 1; pageNum <= r.NumPage(); pageNum++ {
		page := r.Page(pageNum)
		if page.V.IsNull() {
			continue
		}

		texts := page.Content().Text
		if len(texts) == 0 {
			continue
		}

		// Sort top→bottom (Y desc), then left→right (X asc).
		sort.Slice(texts, func(i, j int) bool {
			if math.Abs(texts[i].Y-texts[j].Y) > 2 {
				return texts[i].Y > texts[j].Y
			}
			return texts[i].X < texts[j].X
		})

		var sb strings.Builder
		prevY := texts[0].Y
		prevRight := 0.0

		for _, t := range texts {
			if t.S == "" {
				continue
			}

			yDiff := prevY - t.Y // positive = moved down
			newLine := yDiff > t.FontSize*0.5
			paragraphBreak := yDiff > t.FontSize*2.5

			if paragraphBreak {
				sb.WriteString("\n\n")
				prevRight = 0
				prevY = t.Y
			} else if newLine {
				sb.WriteByte('\n')
				prevRight = 0
				prevY = t.Y
			}

			if prevRight > 0 && t.X-prevRight > t.FontSize*0.15 {
				sb.WriteByte(' ')
			}

			sb.WriteString(t.S)
			prevRight = t.X + t.W
			prevY = t.Y
		}

		raw = append(raw, paragraphsToWords(sb.String())...)
		raw = append(raw, "") // page boundary = paragraph break
	}

	if countWords(raw) == 0 {
		return nil, fmt.Errorf("no se encontró texto en el PDF")
	}
	return raw, nil
}

// ── EPUB ──────────────────────────────────────────────────────────────────────

func parseEPUB(filePath string) ([]string, error) {
	zr, err := zip.OpenReader(filePath)
	if err != nil {
		return nil, fmt.Errorf("no se pudo abrir el EPUB: %w", err)
	}
	defer zr.Close()

	index := make(map[string]*zip.File, len(zr.File))
	for _, f := range zr.File {
		index[f.Name] = f
	}

	readZipFile := func(name string) ([]byte, error) {
		f, ok := index[name]
		if !ok {
			return nil, fmt.Errorf("archivo no encontrado en EPUB: %s", name)
		}
		rc, err := f.Open()
		if err != nil {
			return nil, err
		}
		defer rc.Close()
		return io.ReadAll(rc)
	}

	containerData, err := readZipFile("META-INF/container.xml")
	if err != nil {
		return nil, fmt.Errorf("EPUB inválido: %w", err)
	}

	var container struct {
		Rootfiles []struct {
			FullPath string `xml:"full-path,attr"`
		} `xml:"rootfiles>rootfile"`
	}
	if err := xml.Unmarshal(containerData, &container); err != nil {
		return nil, fmt.Errorf("error leyendo container.xml: %w", err)
	}
	if len(container.Rootfiles) == 0 {
		return nil, fmt.Errorf("EPUB inválido: sin rootfile")
	}

	opfPath := container.Rootfiles[0].FullPath
	opfDir := path.Dir(opfPath)
	if opfDir == "." {
		opfDir = ""
	}

	opfData, err := readZipFile(opfPath)
	if err != nil {
		return nil, fmt.Errorf("OPF no encontrado (%s): %w", opfPath, err)
	}

	var opf struct {
		Manifest struct {
			Items []struct {
				ID        string `xml:"id,attr"`
				Href      string `xml:"href,attr"`
				MediaType string `xml:"media-type,attr"`
			} `xml:"item"`
		} `xml:"manifest"`
		Spine struct {
			Itemrefs []struct {
				IDRef string `xml:"idref,attr"`
			} `xml:"itemref"`
		} `xml:"spine"`
	}
	if err := xml.Unmarshal(opfData, &opf); err != nil {
		return nil, fmt.Errorf("error leyendo OPF: %w", err)
	}

	manifest := make(map[string]string)
	for _, item := range opf.Manifest.Items {
		mt := strings.ToLower(item.MediaType)
		if strings.Contains(mt, "html") {
			var p string
			if opfDir != "" {
				p = path.Clean(opfDir + "/" + item.Href)
			} else {
				p = path.Clean(item.Href)
			}
			manifest[item.ID] = p
		}
	}

	var raw []string
	for _, ref := range opf.Spine.Itemrefs {
		zipPath, ok := manifest[ref.IDRef]
		if !ok {
			continue
		}
		data, err := readZipFile(zipPath)
		if err != nil {
			continue
		}
		text := extractHTMLText(data)
		chunk := paragraphsToWords(text)
		if len(chunk) > 0 {
			if len(raw) > 0 {
				raw = append(raw, "") // chapter boundary
			}
			raw = append(raw, chunk...)
		}
	}

	if countWords(raw) == 0 {
		return nil, fmt.Errorf("no se encontró texto en el EPUB")
	}
	return raw, nil
}

// extractHTMLText walks the HTML tree collecting visible text.
// Block-level elements emit "\n\n" to mark paragraph boundaries.
func extractHTMLText(data []byte) string {
	doc, err := html.Parse(bytes.NewReader(data))
	if err != nil {
		return string(data)
	}

	var sb strings.Builder
	var walk func(*html.Node)
	walk = func(n *html.Node) {
		if n.Type == html.ElementNode {
			switch n.Data {
			case "script", "style", "head":
				return
			case "p", "div", "h1", "h2", "h3", "h4", "h5", "h6",
				"li", "tr", "blockquote", "br":
				sb.WriteString("\n\n")
			}
		}
		if n.Type == html.TextNode {
			sb.WriteString(n.Data)
			sb.WriteByte(' ')
		}
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			walk(c)
		}
	}
	walk(doc)
	return sb.String()
}

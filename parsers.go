package main

import (
	"archive/zip"
	"bytes"
	"encoding/xml"
	"fmt"
	"io"
	"math"
	"path"
	"sort"
	"strings"

	"github.com/ledongthuc/pdf"
	"golang.org/x/net/html"
)

// ── PDF ───────────────────────────────────────────────────────────────────────

// parsePDF extracts words from a PDF by reconstructing spaces from the
// positional data of each text item on the page. GetPlainText() omits gaps
// between items so words get concatenated; this approach inserts a space
// whenever the horizontal gap between two consecutive items on the same line
// is wider than ~15% of the font size.
func parsePDF(filePath string) ([]string, error) {
	f, r, err := pdf.Open(filePath)
	if err != nil {
		return nil, fmt.Errorf("no se pudo abrir el PDF: %w", err)
	}
	defer f.Close()

	var sb strings.Builder

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
		// Tolerance of 2pt to treat items at the same Y as one line.
		sort.Slice(texts, func(i, j int) bool {
			if math.Abs(texts[i].Y-texts[j].Y) > 2 {
				return texts[i].Y > texts[j].Y
			}
			return texts[i].X < texts[j].X
		})

		prevY := texts[0].Y
		prevRight := 0.0 // X + W of the last written item

		for _, t := range texts {
			if t.S == "" {
				continue
			}

			newLine := math.Abs(t.Y-prevY) > t.FontSize*0.5
			if newLine {
				sb.WriteByte('\n')
				prevRight = 0
				prevY = t.Y
			}

			// Insert space when the gap between items is visibly non-zero.
			if !newLine && prevRight > 0 && t.X-prevRight > t.FontSize*0.15 {
				sb.WriteByte(' ')
			}

			sb.WriteString(t.S)
			prevRight = t.X + t.W
			prevY = t.Y
		}

		sb.WriteByte('\n')
	}

	words := strings.Fields(sb.String())
	if len(words) == 0 {
		return nil, fmt.Errorf("no se encontró texto en el PDF")
	}
	return words, nil
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

	// 1. container.xml → OPF path
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

	// 2. OPF → manifest + spine
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

	// id → zip path
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

	// 3. Spine order → extract text
	var words []string
	for _, ref := range opf.Spine.Itemrefs {
		zipPath, ok := manifest[ref.IDRef]
		if !ok {
			continue
		}
		data, err := readZipFile(zipPath)
		if err != nil {
			continue
		}
		words = append(words, strings.Fields(extractHTMLText(data))...)
	}

	if len(words) == 0 {
		return nil, fmt.Errorf("no se encontró texto en el EPUB")
	}
	return words, nil
}

// extractHTMLText walks an HTML parse tree and collects visible text.
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

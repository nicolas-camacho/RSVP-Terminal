# RSVP Reader — Project Specification

Full design document for cross-platform reimplementation. Covers every feature, algorithm, data flow, and design decision made in the original terminal version.

---

## 1. What is RSVP reading?

**Rapid Serial Visual Presentation (RSVP)** is a reading technique that eliminates the primary bottleneck of traditional reading: eye movement. Instead of the eye scanning left-to-right across a line, words are presented one at a time at a fixed position on screen. The reader's eye never moves — words come to it.

Practical effect: most people can read 20–50% faster with good comprehension once accustomed to the method (typical training period: 1–2 hours of use).

### Optimal Recognition Point (ORP)

The most important concept in RSVP. The eye does not read a word by scanning it left-to-right — it fixates on a single letter and the brain reconstructs the whole word from there. This fixation point is the **ORP**: the letter that, when fixated, allows fastest recognition of the whole word.

Research shows the ORP is slightly left of center for most words. The exact position by word length:

| Word length | ORP index (0-based) | Example (ORP in brackets) |
|-------------|--------------------|-----------------------------|
| 1–3 chars   | 0 (1st letter)     | `[i]t`, `[t]he`             |
| 4–5 chars   | 1 (2nd letter)     | `w[o]rd`, `r[e]ads`         |
| 6–9 chars   | 2 (3rd letter)     | `re[a]ding`, `qu[i]ckly`    |
| 10–13 chars | 3 (4th letter)     | `imp[o]rtantly`             |
| 14+ chars   | 4 (5th letter)     | `comp[r]ehension`           |

**Implementation rule:** all words are horizontally aligned so their ORP character sits at the same fixed column on screen (the horizontal center). The ORP letter is displayed in a distinct color (red in the original). This eliminates micro eye-movements between words.

---

## 2. Application states

The app is a finite state machine with 6 states:

```
┌─────────────┐
│ FilePicker  │ ← entry point, lists books
└──────┬──────┘
       │ Enter (select book)
       ▼
┌─────────────┐
│   Loading   │ ← async parse/cache load, shows spinner
└──────┬──────┘
       │ wordsLoaded
       ▼
┌─────────────┐     Esc        ┌─────────────┐
│  Navigator  │ ──────────────►│ FilePicker  │
└──────┬──────┘                └─────────────┘
       │ Enter
       ▼
┌─────────────┐     Esc        ┌─────────────┐
│    Ready    │ ──────────────►│ FilePicker  │
│  (config)   │     n          │             │
└──────┬──────┘ ──────────────►│  Navigator  │
       │ S                     └─────────────┘
       ▼
┌─────────────┐     r          ┌─────────────┐
│   Reading   │ ──────────────►│    Ready    │
│             │     n          │             │
│             │ ──────────────►│  Navigator  │
│             │     Esc        │             │
│             │ ──────────────►│ FilePicker  │
└──────┬──────┘                └─────────────┘
       │ (last word)
       ▼
┌─────────────┐     r          ┌─────────────┐
│    Done     │ ──────────────►│ FilePicker  │
│             │     Enter      │             │
│             │ ──────────────►│    Ready    │
└─────────────┘                └─────────────┘
```

### State descriptions

**FilePicker** — lists all readable files in the `books/` directory. Shows a "saved progress" indicator next to books that have a stored position. Navigation: ↑↓ to move, Enter to select.

**Loading** — triggered immediately after selection. The file is parsed in a background thread/goroutine while a spinner animates. On completion, transitions to Navigator.

**Navigator** — shows the full text of the selected book, wrapped to screen width, scrollable. Three word visual states: already-read (dim), unread (normal), cursor (highlighted). The cursor starts at the last saved position for this book.

**Ready (Config)** — pre-reading configuration screen. Shows current WPM, font size, and long-word bonus. Preview word demonstrates current font size. Press S to begin reading.

**Reading** — the main reading view. One word at a time, centered, ORP aligned to screen center. Progress bar at top. Status bar at bottom.

**Done** — shown when the last word is displayed. Offers to re-read (Enter) or return to book selection (R).

---

## 3. File formats

The app reads from a `books/` directory. Three formats are supported:

### TXT
Plain text files. Paragraph detection: two or more consecutive newlines (`\n\n`) mark a paragraph boundary. Single newlines within a paragraph are treated as spaces.

### PDF
Parsed by extracting individual text items with their X/Y coordinates and font sizes. Reconstruction algorithm:
1. Sort items top→bottom (Y descending), then left→right (X ascending) within each line
2. Two items are on the **same line** if `|ΔY| ≤ 0.5 × fontSize`
3. A **word space** is inserted between consecutive items on the same line when the horizontal gap between them exceeds `0.15 × fontSize`
4. A **line break** is inserted when `ΔY > 0.5 × fontSize`
5. A **paragraph break** is inserted when `ΔY > 2.5 × fontSize`

This reconstruction is necessary because PDFs store text as positioned glyphs without explicit spaces — naive extraction concatenates adjacent words.

### EPUB
EPUBs are ZIP archives containing XHTML content files. Parse sequence:
1. Read `META-INF/container.xml` → find the OPF (package) file path
2. Parse the OPF manifest and spine to get content files in reading order
3. For each spine item, parse its XHTML and extract text
4. Block-level HTML elements (`<p>`, `<div>`, `<h1>`–`<h6>`, `<li>`, `<br>`, `<blockquote>`, `<tr>`) emit a paragraph break signal
5. Chapter boundaries (transitions between spine items) are treated as paragraph breaks

---

## 4. Word processing pipeline

After raw text is extracted from any format, it goes through this pipeline:

```
Raw format bytes
      │
      ▼
Format-specific parser
(returns []string with "" paragraph sentinels)
      │
      ▼
computePauseFactors()
(strips sentinels, produces clean words + per-word float64 multipliers)
      │
      ▼
model.words []string        model.pauseFactors []float64
```

### Paragraph sentinels

Parsers communicate paragraph breaks by inserting empty strings (`""`) in the word list. Example:

```
["The", "fox", "jumped.", "", "A", "new", "paragraph."]
```

`computePauseFactors` consumes these sentinels and upgrades the pause multiplier of the word immediately before each sentinel to paragraph level.

### Pause factor computation

Every word gets a `float64` pause multiplier applied to its display duration:

```
punctuationFactor(word):
  scan last characters right-to-left, skip closing punctuation (", ', ), ]):
    . ! ? … → 2.5   (sentence end)
    , ; :   → 1.5   (clause break)
    default → 1.0

if "" sentinel follows word:
    factor = max(current factor, 3.5)   (paragraph break)
```

Default multiplier values:
| Trigger | Factor |
|---------|--------|
| Normal | 1.0 |
| Clause (`, ; :`) | 1.5 |
| Sentence (`. ! ?`) | 2.5 |
| Paragraph break | 3.5 |

These are named constants (`factorClause`, `factorSentence`, `factorParagraph`) and can be made user-configurable.

---

## 5. Display duration formula

For each word at position `i`:

```
base = 60 seconds / WPM

if longWordBonus > 0 AND len(word) >= 9:
    base = base + base * longWordBonus / 100

duration = base * pauseFactors[i]
```

**Long word bonus**: compensates for the extra cognitive load of long words. Default 5%, range 0–50%, step 5%. Threshold: 9+ characters (aligns with the ORP table boundary at 6–9 chars).

**Example at 300 WPM (base = 200ms):**
- Normal short word: 200 ms
- Normal long word (9+ chars, 5% bonus): 210 ms
- Word ending in comma: 300 ms
- Word ending in period: 500 ms
- Last word before paragraph break: 700 ms

---

## 6. Reading screen layout

```
████████████████████████░░░░░░░░░░░░    ← progress bar (row 0)

(empty rows)

        ────────────┬────────────       ← guide line above (marks ORP column)
                 wo[r]d                 ← word with ORP letter highlighted
        ────────────┴────────────       ← guide line below

(empty rows)

▶ 300 WPM  42/847    space:pausa  ±:vel  ←→:nav  n:texto  r:config  q:salir
```

The word is rendered at the vertical center of the screen. The ORP character is always placed at the horizontal center column. Guide lines (box-drawing characters) mark this column visually.

### ORP rendering algorithm

```
word = "reading"   ORP index = 2   centerX = screen_width / 2

left  = "re"       (characters before ORP)
orp   = "a"        (the ORP character, rendered in red)
right = "ding"     (characters after ORP)

leftPad = centerX - len(left)   // so ORP lands at centerX
output  = spaces(leftPad) + styled(left) + red(orp) + styled(right)
```

### Font size simulation

Terminals cannot change font size programmatically. "Font size" is simulated by adding spacing between letters:

- Size 1: `reading` (no spacing)
- Size 2: `r e a d i n g` (1 space between letters)
- Size 3: `r  e  a  d  i  n  g` (2 spaces)
- ...up to size 5

The ORP column calculation must account for spacing:
```
orpVisualOffset = orpIndex * (1 + spacing)
leftPad = centerX - orpVisualOffset
```

---

## 7. Text navigator

The navigator renders the full word list as wrapped text. Key behaviors:

- **Word wrapping**: words are greedily packed into lines of `screenWidth - 4` chars (2-char padding each side). A word that would overflow starts a new line.
- **Scroll**: viewport is centered on the cursor word's line. Stateless — recomputed each frame.
- **Visual states**:
  - `index < cursor`: already read (dim color, e.g. dark blue-grey)
  - `index == navCursor`: current cursor (red, underlined)
  - `index > cursor`: unread (normal grey)
- **Navigation**: ←→ word by word; ↑↓ jumps to same column position on prev/next line (falls back to last word on shorter lines); G/g jump to end/start.

### Entry and exit

| Entry point | navCursor starts at | Esc returns to | Enter goes to |
|-------------|--------------------|--------------------|---------------|
| After book selection | saved progress position | FilePicker | Ready (config) |
| `n` from Ready | `model.index` | Ready | Ready (new index) |
| `n` from Reading | `model.index` (saves progress) | Reading (restarts tick) | Reading (new index, restarts tick) |

---

## 8. Caching system

PDF and EPUB files are expensive to parse (seconds for large books). After the first parse, results are cached.

**Cache location**: `books/.cache/<filename>.gob`

**Cache structure**:
```
{
  ModTime:      int64      // Unix timestamp of source file at cache time
  Words:        []string   // clean word list (no sentinels)
  PauseFactors: []float64  // per-word multipliers
}
```

**Invalidation**: on load, compare source file's current mod time to `cache.ModTime`. Mismatch → delete cache entry, re-parse, re-cache.

**Format**: Go's `encoding/gob` binary format. Fast to encode/decode, compact for large word lists.

TXT files are not cached (fast to read and parse).

---

## 9. Progress persistence

**Location**: `books/.progress` (JSON)

**Format**:
```json
{
  "books/the_raven.txt": 42,
  "books/some_book.pdf": 1337
}
```

Key = relative file path. Value = word index (0-based) of last reading position.

**Save triggers**:
- Opening the text navigator from Reading state
- Pressing Esc from Reading (goes to FilePicker)
- Pressing Q to quit from Reading
- Completing the book (value reset to 0)

**Load**: on app startup. The loaded map is carried in the model for the session.

**Behavior**: when a book is selected, `model.index` and `model.navCursor` are both initialized to `savedProgress[bookPath]` (or 0 if not found). The navigator opens showing the text at that position.

---

## 10. Configuration options

All configuration persists for the session only (not saved to disk between runs).

| Setting | Default | Range | Step | Keys |
|---------|---------|-------|------|------|
| WPM | 300 | 50–∞ | 25 | `+` / `-` |
| Font size | 1 | 1–5 | 1 | `]` / `[` |
| Long word bonus | 5% | 0–50% | 5% | `.` / `,` |

WPM and long word bonus are adjustable both on the config screen and during live reading.

---

## 11. Key bindings summary

### FilePicker
| Key | Action |
|-----|--------|
| `↑` `↓` / `k` `j` | navigate list |
| `Enter` / `Space` | select book |
| `q` / `Ctrl+C` | quit |

### Navigator
| Key | Action |
|-----|--------|
| `←` `→` / `h` `l` | word by word |
| `↑` `↓` / `k` `j` | line by line |
| `g` / `G` | go to start / end |
| `Enter` / `s` | start reading from cursor |
| `Esc` | go back |
| `q` / `Ctrl+C` | quit |

### Ready (Config)
| Key | Action |
|-----|--------|
| `+` / `-` | ±25 WPM |
| `]` / `[` | font size ±1 |
| `.` / `,` | long word bonus ±5% |
| `s` | start reading |
| `n` | open navigator |
| `Esc` | back to FilePicker |
| `q` / `Ctrl+C` | quit |

### Reading
| Key | Action |
|-----|--------|
| `Space` | pause / resume |
| `+` / `-` | ±25 WPM |
| `.` / `,` | long word bonus ±5% |
| `←` `→` | step one word back / forward |
| `n` | open navigator (saves progress) |
| `r` | back to config |
| `Esc` | back to FilePicker (saves progress) |
| `q` / `Ctrl+C` | quit (saves progress) |

### Done
| Key | Action |
|-----|--------|
| `Enter` / `Space` | re-read (go to Ready) |
| `r` | back to FilePicker |
| `q` / `Ctrl+C` | quit |

---

## 12. Implementation notes for other platforms

### Core data model
Any reimplementation needs:
- `words []string` — clean word list
- `pauseFactors []float64` — one per word
- `index int` — current reading position
- `navCursor int` — navigator cursor
- `wpm int`, `longWordBonus int`, `fontSize int`
- `bookProgress map[string]int` — persisted

### Word display duration
```
base = 60_000ms / wpm
if len(word) >= 9: base *= (1 + longWordBonus/100)
duration = base * pauseFactors[index]
```

### ORP alignment
The key visual invariant: the ORP character of every word is always displayed at the same horizontal position. On a fixed-grid display (terminal, PSP screen with tile renderer), this means computing left padding as:
```
leftPad = centerColumn - orpIndex(word)
```
For proportional fonts, use pixel widths of the characters before the ORP.

### Paragraph break detection
- **TXT**: split on `\n\n` (or `\n\n\n` collapsed to `\n\n`)
- **PDF**: Y-axis gap between text runs > 2.5 × fontSize
- **EPUB**: block-level HTML elements between text nodes
- **Other formats**: any structural boundary in the source (chapter headers, section breaks, etc.)

### What to adapt per platform
| Concern | Terminal (original) | PSP / embedded |
|---------|--------------------|-----------------------|
| Rendering | ANSI escape codes via Lipgloss | Direct framebuffer / GPU / tile map |
| Input | Keyboard events | D-pad, face buttons, analog stick |
| File I/O | OS filesystem | Memory stick, UMD, or compiled-in resources |
| Concurrency | Goroutines (Go) | Platform threads or cooperative multitasking |
| Font size | Letter spacing simulation | Actual bitmap font scaling |
| Progress file | JSON on disk | Save file / EEPROM / memory card |
| Cache | Gob files on disk | Pre-processed binary blobs or in-memory only |

### PSP-specific suggestions
- Use the PSP SDK's `oslReadFile` or equivalent for file I/O
- Fonts: OSLib or intraFont for bitmap font rendering — true font scaling is available
- The ORP alignment concept works the same way: compute pixel offset of the ORP character using the font's character width table
- Input mapping suggestion: `Cross` = select/confirm, `Circle` = back, `L/R` = adjust WPM, `D-pad` = navigate
- The pause factor system requires a timer — use `sceKernelGetSystemTimeLow()` or equivalent
- Memory constraint: for large books, consider streaming word-by-word from the file rather than loading all words into RAM

---

## 13. Algorithm reference

### `orpIndex(word) → int`
```
n = character count of word
n ≤ 3  → 0
n ≤ 5  → 1
n ≤ 9  → 2
n ≤ 13 → 3
else   → 4
```

### `punctuationFactor(word) → float`
```
scan word right-to-left, skip: " ' ) ] » " '
first non-skip character:
  . ! ? … → 2.5
  , ; :   → 1.5
  other   → 1.0
```

### `computePauseFactors(raw) → (words, factors)`
```
for each token in raw:
  if token == "":                          // paragraph sentinel
    if factors not empty:
      factors[last] = max(factors[last], 3.5)
    continue
  words.append(token)
  factors.append(punctuationFactor(token))
```

### `wrapWords(words, lineWidth) → (lines, wordToLine)`
```
lineWords = []
lineLen = 0
lineNum = 0

for i, word in words:
  wLen = len(word)
  sep = 1 if lineWords not empty else 0
  if lineWords not empty AND lineLen + sep + wLen > lineWidth:
    lines.append(lineWords)
    lineNum++
    lineWords = [i]
    lineLen = wLen
  else:
    lineWords.append(i)
    lineLen += sep + wLen
  wordToLine[i] = lineNum

if lineWords not empty:
  lines.append(lineWords)
```

### PDF text reconstruction (per page)
```
items = page.textItems()                  // each has x, y, w, s, fontSize
sort items by (y DESC, x ASC)            // top-to-bottom, left-to-right

prevY = items[0].y
prevRight = 0

for item in items:
  yDiff = prevY - item.y                 // positive = moved down

  if yDiff > item.fontSize * 2.5:
    emit "\n\n"                          // paragraph break
    prevRight = 0
  elif yDiff > item.fontSize * 0.5:
    emit "\n"                            // line break
    prevRight = 0

  if prevRight > 0 AND item.x - prevRight > item.fontSize * 0.15:
    emit " "                             // word space

  emit item.s
  prevRight = item.x + item.w
  prevY = item.y
```

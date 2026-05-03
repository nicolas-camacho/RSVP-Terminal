# rsvp-terminal

A terminal-based RSVP (Rapid Serial Visual Presentation) reader built with the [Charm](https://charm.sh) suite. Displays one word at a time in the center of the terminal at a configurable speed, using the Optimal Recognition Point technique to reduce eye movement and increase reading speed.

## What is RSVP?

RSVP is a reading method that presents words sequentially at a fixed position. Instead of your eyes scanning across a line, each word is brought to you. The **Optimal Recognition Point (ORP)** is the specific letter in each word where the eye fixates for fastest recognition — highlighted in red in this app. Words are aligned so the ORP is always at the same horizontal position on screen.

ORP position by word length:
| Length | ORP position |
|--------|-------------|
| 1–3    | 1st letter  |
| 4–5    | 2nd letter  |
| 6–9    | 3rd letter  |
| 10–13  | 4th letter  |
| 14+    | 5th letter  |

## Requirements

- Go 1.21+
- A terminal with color support (Windows Terminal, iTerm2, etc.)

## Running

```bash
go run .
```

Or build first:

```bash
go build -o rsvp-terminal
./rsvp-terminal
```

## Project structure

```
rsvp-terminal/
├── main.go          # TUI logic, state machine, views
├── parsers.go       # PDF, EPUB, and TXT text extraction
├── cache.go         # word cache for PDF/EPUB (gob, keyed by mod time)
├── books/
│   ├── file.txt         # sample text (committed)
│   ├── .progress        # saved reading positions per book (gitignored)
│   └── .cache/          # parsed word cache for PDF/EPUB files (gitignored)
│       └── <file>.gob
├── go.mod
├── go.sum
└── README.md
```

Add any `.txt`, `.pdf`, or `.epub` file to the `books/` folder and it will appear in the book selector automatically.

## Features

### Book selector
- Lists all `.txt`, `.pdf`, and `.epub` files in the `books/` directory
- Books with saved progress show a "progreso guardado" indicator
- Navigate with `↑` / `↓`, confirm with `Enter`

### Loading screen
PDF and EPUB files are parsed on first open and cached for instant subsequent loads. A spinner is shown while processing.

- **First open:** parses the file, saves a word cache to `books/.cache/<filename>.gob`
- **Subsequent opens:** loads from cache instantly
- **File changed:** mod time mismatch triggers a re-parse and cache update

### Text navigator
- Opens after selecting a book, or press `n` during reading
- Shows the full text wrapped to terminal width
- Three visual states: **read** (dark), **unread** (grey), **cursor** (red underline)
- Cursor starts at the last saved position for that book

| Key | Action |
|-----|--------|
| `←` `→` | move word by word |
| `↑` `↓` | move line by line |
| `g` / `G` | jump to start / end |
| `Enter` | start reading from cursor position |
| `Esc` | go back (resumes reading if opened mid-read) |

### Config screen
Shown before starting. Adjustable while in this screen:

| Key | Action |
|-----|--------|
| `+` / `-` | ±25 WPM |
| `]` / `[` | font size 1–5 (letter spacing) |
| `s` | start reading |
| `n` | open text navigator |
| `Esc` | back to book selector |

### Reading screen

| Key | Action |
|-----|--------|
| `Space` | pause / resume |
| `+` / `-` | adjust WPM on the fly |
| `←` / `→` | step back / forward one word |
| `n` | open text navigator (saves progress) |
| `r` | back to config (same book) |
| `Esc` | back to book selector (saves progress) |
| `q` | quit (saves progress) |

### Progress persistence
Reading position is saved automatically to `books/.progress` when:
- Opening the text navigator from reading
- Pressing `Esc` or `q` during reading
- Finishing a book (resets to 0)

The next time you select the same book, the navigator cursor starts at the saved position.

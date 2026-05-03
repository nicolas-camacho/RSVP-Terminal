package main

import (
	"encoding/gob"
	"os"
	"path/filepath"
	"strings"
)

const cacheDir = "books/.cache"

type wordCache struct {
	ModTime int64
	Words   []string
}

func cachePathFor(bookPath string) string {
	return filepath.Join(cacheDir, filepath.Base(bookPath)+".gob")
}

func loadFromCache(bookPath string) ([]string, bool) {
	info, err := os.Stat(bookPath)
	if err != nil {
		return nil, false
	}

	f, err := os.Open(cachePathFor(bookPath))
	if err != nil {
		return nil, false
	}
	defer f.Close()

	var c wordCache
	if err := gob.NewDecoder(f).Decode(&c); err != nil {
		return nil, false
	}

	if c.ModTime != info.ModTime().Unix() {
		return nil, false
	}
	return c.Words, true
}

func saveToCache(bookPath string, words []string) {
	info, err := os.Stat(bookPath)
	if err != nil {
		return
	}
	if err := os.MkdirAll(cacheDir, 0755); err != nil {
		return
	}
	f, err := os.Create(cachePathFor(bookPath))
	if err != nil {
		return
	}
	defer f.Close()

	_ = gob.NewEncoder(f).Encode(wordCache{
		ModTime: info.ModTime().Unix(),
		Words:   words,
	})
}

func isCacheable(bookPath string) bool {
	ext := strings.ToLower(filepath.Ext(bookPath))
	return ext == ".pdf" || ext == ".epub"
}

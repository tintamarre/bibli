package main

import (
	"log"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestLogFileRotates(t *testing.T) {
	path := filepath.Join(t.TempDir(), "logs", "bibli.log")
	lf, err := openLogFile(path, 100)
	if err != nil {
		t.Fatal(err)
	}
	l := log.New(lf, "", 0)
	for i := 0; i < 10; i++ {
		l.Print(strings.Repeat("x", 30))
	}
	cur, _ := os.ReadFile(path)
	old, err := os.ReadFile(path + ".1")
	if err != nil {
		t.Fatalf("no rotated file: %v", err)
	}
	if len(cur) > 100 || len(old) > 100 {
		t.Errorf("a file passed the limit: current %d, rotated %d bytes", len(cur), len(old))
	}
	if len(cur) == 0 {
		t.Error("current file is empty after rotation")
	}
}

func TestLogFileAppends(t *testing.T) {
	path := filepath.Join(t.TempDir(), "bibli.log")
	for _, line := range []string{"first\n", "second\n"} {
		lf, err := openLogFile(path, maxLogSize)
		if err != nil {
			t.Fatal(err)
		}
		lf.Write([]byte(line))
		lf.f.Close()
	}
	if got, _ := os.ReadFile(path); string(got) != "first\nsecond\n" {
		t.Errorf("a reopened log lost its content: %q", got)
	}
}

func TestReadPasswordFile(t *testing.T) {
	for raw, want := range map[string]string{
		"phrase de passe longue":         "phrase de passe longue",
		"phrase de passe longue\r\n":     "phrase de passe longue",
		"\ufeffphrase de passe longue\n": "phrase de passe longue",
		"  espaces gardés  \n":           "  espaces gardés  ",
	} {
		path := filepath.Join(t.TempDir(), "password")
		os.WriteFile(path, []byte(raw), 0o600)
		got, err := readPasswordFile(path)
		if err != nil || got != want {
			t.Errorf("readPasswordFile(%q) = %q, %v; want %q", raw, got, err, want)
		}
	}
}

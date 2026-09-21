package config

import (
	"bytes"
	"errors"
	"io/fs"
	"os"

	"github.com/joho/godotenv"
)

// utf8BOM is what Windows editors and PowerShell's `Set-Content -Encoding utf8`
// prepend to a file. godotenv does not strip it, so the leading bytes get glued
// onto the first key name and the file can fail to parse entirely.
var utf8BOM = []byte{0xEF, 0xBB, 0xBF}

// LoadEnvFiles reads .env files and applies them to the process environment.
//
// It matches godotenv.Load semantics — a variable already present in the
// environment always wins — but tolerates a UTF-8 BOM and CRLF line endings,
// which is the normal state of a file created on Windows.
//
// A missing file is not an error: .env is a local-development convenience, and
// in Docker the variables are supplied directly.
func LoadEnvFiles(paths ...string) error {
	if len(paths) == 0 {
		paths = []string{".env"}
	}

	for _, path := range paths {
		raw, err := os.ReadFile(path)
		if err != nil {
			if errors.Is(err, fs.ErrNotExist) {
				continue
			}
			return err
		}

		parsed, err := godotenv.Unmarshal(string(bytes.TrimPrefix(raw, utf8BOM)))
		if err != nil {
			return err
		}

		for key, value := range parsed {
			if _, exists := os.LookupEnv(key); !exists {
				if err := os.Setenv(key, value); err != nil {
					return err
				}
			}
		}
	}
	return nil
}

package configtest

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"quiz/internal/config"
)

// configEnvKeys lists every environment variable Config reads. cleanenv's
// parseENV writes file keys into OS env unconditionally, so each test clears
// them first to stay deterministic regardless of ambient env or earlier tests.
var configEnvKeys = []string{
	"PORT", "GURU_USER", "GURU_PASS", "DB_USER", "DB_PASS",
	"DB_NAME", "DB_PORT", "DB_HOST", "TEST_DB_NAME",
}

func clearConfigEnv(t *testing.T) {
	t.Helper()
	for _, k := range configEnvKeys {
		orig, had := os.LookupEnv(k)
		os.Unsetenv(k)
		k := k
		t.Cleanup(func() {
			if had {
				os.Setenv(k, orig)
			} else {
				os.Unsetenv(k)
			}
		})
	}
}

func writeEnvFile(t *testing.T, content string) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), ".env")
	if err := os.WriteFile(p, []byte(content), 0o600); err != nil {
		t.Fatalf("write env file: %v", err)
	}
	return p
}

func TestLoad(t *testing.T) {
	fullFile := "GURU_USER=guru\nGURU_PASS=secret\nDB_USER=quiz\n" +
		"DB_PASS=quizpass\nDB_NAME=quiz\nDB_PORT=3306\nPORT=8090\n"
	noPassFile := "GURU_USER=guru\nDB_USER=quiz\nDB_PASS=quizpass\nDB_NAME=quiz\n"

	tests := []struct {
		name     string
		file     string            // "" → use a nonexistent path
		setEnv   map[string]string // OS env set before Load
		wantHost string            // expected DBHost
		wantGuru string            // expected GuruUser
		wantErr  string            // substring of error; "" → expect success
	}{
		{
			name:     "DB_HOST defaults when absent from file and OS env",
			file:     fullFile,
			wantHost: "127.0.0.1",
			wantGuru: "guru",
		},
		{
			name:     "compose OS env overrides when key absent from file",
			file:     fullFile,
			setEnv:   map[string]string{"DB_HOST": "db"},
			wantHost: "db",
			wantGuru: "guru",
		},
		{
			// The image carries no .env: inside a container every value
			// arrives as OS env injected by compose env_file, so Load must
			// resolve fully without a file.
			name: "missing file resolves entirely from OS env",
			file: "",
			setEnv: map[string]string{
				"GURU_USER": "osguru", "GURU_PASS": "ospass",
				"DB_USER": "osdb", "DB_PASS": "osdbpass", "DB_NAME": "osname",
				"DB_HOST": "db",
			},
			wantHost: "db",
			wantGuru: "osguru",
		},
		{
			name:     "file wins over OS env for keys present in file",
			file:     fullFile,
			setEnv:   map[string]string{"GURU_USER": "osuser", "DB_HOST": "oshost"},
			wantHost: "oshost", // DB_HOST absent from file → OS env applies
			wantGuru: "guru",   // present in file → file wins
		},
		{
			name:    "missing required key errors",
			file:    noPassFile,
			wantErr: "read config",
		},
		{
			name:    "nonexistent path errors",
			file:    "", // default: path under a fresh temp dir that was never written
			wantErr: "read config",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			clearConfigEnv(t)
			for k, v := range tt.setEnv {
				t.Setenv(k, v)
			}
			path := tt.file
			if path == "" {
				path = filepath.Join(t.TempDir(), ".env")
			} else {
				path = writeEnvFile(t, tt.file)
			}

			cfg, err := config.Load(path)

			if tt.wantErr != "" {
				if err == nil {
					t.Fatalf("Load(%q) = %+v, nil; want error containing %q", path, cfg, tt.wantErr)
				}
				if !strings.Contains(err.Error(), tt.wantErr) {
					t.Errorf("Load(%q) error = %q; want substring %q", path, err, tt.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("Load(%q): %v", path, err)
			}
			if cfg.DBHost != tt.wantHost {
				t.Errorf("DBHost = %q, want %q", cfg.DBHost, tt.wantHost)
			}
			if cfg.GuruUser != tt.wantGuru {
				t.Errorf("GuruUser = %q, want %q", cfg.GuruUser, tt.wantGuru)
			}
		})
	}
}

func TestDSN(t *testing.T) {
	c := &config.Config{DBUser: "quiz", DBPass: "p", DBHost: "db", DBPort: 3306}
	got := c.DSN("quiz")
	want := "quiz:p@tcp(db:3306)/quiz?parseTime=true&charset=utf8mb4&loc=UTC"
	if got != want {
		t.Errorf("DSN = %q, want %q", got, want)
	}
}

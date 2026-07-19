package alert

import (
	"archive/zip"
	"bytes"
	"context"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

const defaultGotifyDownloadURL = "https://github.com/gotify/server/releases/download/v3.0.0/gotify-windows-amd64.exe.zip"

// EnsureGotifyServerRunning checks if a Gotify server is reachable.
// If it's a localhost target and not running, it automatically downloads and starts the native Windows binary.
func EnsureGotifyServerRunning(ctx context.Context, gotifyURL string, logger func(string, ...interface{})) error {
	if logger == nil {
		logger = func(string, ...interface{}) {}
	}

	if gotifyURL == "" {
		gotifyURL = "http://localhost:8383"
	}

	cleanURL := strings.TrimRight(gotifyURL, "/")

	// 1. Check if server is already running
	if isGotifyReachable(cleanURL) {
		logger("Gotify Server is already active at %s", cleanURL)
		return nil
	}

	u, err := url.Parse(cleanURL)
	if err != nil {
		return fmt.Errorf("invalid gotify URL: %w", err)
	}

	host := u.Hostname()
	if host != "localhost" && host != "127.0.0.1" {
		logger("Gotify URL (%s) is remote, skipping local process autostart", cleanURL)
		return nil
	}

	port := u.Port()
	if port == "" {
		port = "8383"
	}

	logger("Gotify Server not detected at %s. Initializing native Windows process on port %s...", cleanURL, port)

	// 2. Ensure bin directory and binary exist
	binDir := "bin"
	_ = os.MkdirAll(binDir, 0755)

	exePath := filepath.Join(binDir, "gotify-server.exe")
	if _, err := os.Stat(exePath); os.IsNotExist(err) {
		logger("Downloading Gotify Server native binary (~15MB)...")
		if err := downloadAndExtractGotify(exePath, defaultGotifyDownloadURL); err != nil {
			return fmt.Errorf("failed to download Gotify server: %w", err)
		}
		logger("Gotify Server downloaded successfully to %s", exePath)
	}

	// 3. Ensure config.yml exists with correct port
	_ = os.MkdirAll(filepath.Join(binDir, "data"), 0755)
	_ = ensureGotifyConfigFile(binDir, port)

	// 4. Launch Gotify Server process using absolute paths
	absExePath, err := filepath.Abs(exePath)
	if err != nil {
		absExePath = exePath
	}
	absBinDir, err := filepath.Abs(binDir)
	if err != nil {
		absBinDir = binDir
	}

	cmd := exec.Command(absExePath)
	cmd.Dir = absBinDir
	cmd.Env = append(os.Environ(), "GOTIFY_SERVER_PORT="+port)

	err = cmd.Start()
	if err != nil {
		return fmt.Errorf("failed to start gotify-server.exe: %w", err)
	}

	logger("Gotify Server process launched (PID: %d). Waiting for HTTP readiness...", cmd.Process.Pid)

	// 4. Poll readiness
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}
		if isGotifyReachable(cleanURL) {
			logger("Gotify Server is READY at %s!", cleanURL)
			return nil
		}
		time.Sleep(500 * time.Millisecond)
	}

	return fmt.Errorf("gotify server launched but failed to respond on %s within 10 seconds", cleanURL)
}

func isGotifyReachable(targetURL string) bool {
	client := &http.Client{Timeout: 2 * time.Second}
	resp, err := client.Get(targetURL + "/health")
	if err == nil {
		_ = resp.Body.Close()
		return true
	}

	// Fallback check root /
	respRoot, errRoot := client.Get(targetURL + "/")
	if errRoot == nil {
		_ = respRoot.Body.Close()
		return true
	}

	return false
}

func downloadAndExtractGotify(targetPath, downloadURL string) error {
	resp, err := http.Get(downloadURL)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("HTTP %d downloading %s", resp.StatusCode, downloadURL)
	}

	zipBytes, err := io.ReadAll(resp.Body)
	if err != nil {
		return err
	}

	zipReader, err := zip.NewReader(bytes.NewReader(zipBytes), int64(len(zipBytes)))
	if err != nil {
		return err
	}

	var exeFile *zip.File
	for _, f := range zipReader.File {
		if strings.HasSuffix(f.Name, ".exe") {
			exeFile = f
			break
		}
	}

	if exeFile == nil {
		return fmt.Errorf("no .exe found inside Gotify zip download")
	}

	rc, err := exeFile.Open()
	if err != nil {
		return err
	}
	defer rc.Close()

	outFile, err := os.Create(targetPath)
	if err != nil {
		return err
	}
	defer outFile.Close()

	_, err = io.Copy(outFile, rc)
	return err
}

func ensureGotifyConfigFile(binDir, portStr string) error {
	configPath := filepath.Join(binDir, "config.yml")
	content := fmt.Sprintf(`server:
  port: %s
  ssl:
    enabled: false
database:
  dialect: sqlite3
  connection: data/gotify.db
defaultuser:
  name: admin
  pass: admin
passstrength: 10
uploadedimagesdir: data/images
pluginsdir: data/plugins
`, portStr)
	return os.WriteFile(configPath, []byte(content), 0644)
}

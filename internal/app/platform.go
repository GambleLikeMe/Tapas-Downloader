package app

import (
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"
)

func applicationRoot(executable, cwd string) string {
	dir := filepath.Dir(executable)
	if strings.HasPrefix(filepath.Base(filepath.Dir(filepath.Dir(dir))), "go-build") {
		return cwd
	}
	if filepath.Base(dir) == "MacOS" && filepath.Base(filepath.Dir(dir)) == "Contents" && strings.HasSuffix(filepath.Base(filepath.Dir(filepath.Dir(dir))), ".app") {
		return filepath.Dir(filepath.Dir(filepath.Dir(dir)))
	}
	return dir
}

func portableRoot() (string, error) {
	executable, err := os.Executable()
	if err != nil {
		return "", err
	}
	if resolved, err := filepath.EvalSymlinks(executable); err == nil {
		executable = resolved
	}
	cwd, err := os.Getwd()
	if err != nil {
		return "", err
	}
	return applicationRoot(executable, cwd), nil
}

func legacyDataDir(cwd string) string {
	if runtime.GOOS != "windows" {
		for _, name := range []string{"accounts", "queue.json", "settings.json"} {
			if _, err := os.Stat(filepath.Join(cwd, name)); err == nil {
				return cwd
			}
		}
	}
	base, err := os.UserConfigDir()
	if err != nil {
		return ""
	}
	return filepath.Join(base, "Tapas Downloader")
}

func copyPortableFile(source, target string) error {
	input, err := os.Open(source)
	if err != nil {
		return err
	}
	defer input.Close()
	output, err := os.OpenFile(target, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if err != nil {
		return err
	}
	_, copyErr := io.Copy(output, input)
	closeErr := output.Close()
	if copyErr != nil {
		return copyErr
	}
	return closeErr
}

func initializePortableData(target, legacy string) error {
	if _, err := os.Stat(target); err == nil {
		return nil
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	parent := filepath.Dir(target)
	staging, err := os.MkdirTemp(parent, ".tapas-data-*")
	if err != nil {
		return err
	}
	defer os.RemoveAll(staging)
	if legacy != "" {
		if info, err := os.Stat(legacy); err == nil && info.IsDir() {
			for _, name := range []string{"cache.json", "history.json", "queue.json", "settings.json", "urls.txt"} {
				source := filepath.Join(legacy, name)
				if info, err := os.Lstat(source); err == nil && info.Mode().IsRegular() {
					if err := copyPortableFile(source, filepath.Join(staging, name)); err != nil {
						return err
					}
				}
			}
			accountSource := filepath.Join(legacy, "accounts")
			if info, err := os.Lstat(accountSource); err == nil && info.IsDir() {
				if err := os.Mkdir(filepath.Join(staging, "accounts"), 0700); err != nil {
					return err
				}
				entries, err := os.ReadDir(accountSource)
				if err != nil {
					return err
				}
				for _, entry := range entries {
					if !entry.Type().IsRegular() {
						continue
					}
					if err := copyPortableFile(filepath.Join(accountSource, entry.Name()), filepath.Join(staging, "accounts", entry.Name())); err != nil {
						return err
					}
				}
			}
		}
	}
	return os.Rename(staging, target)
}

func prepareDataDir() (string, error) {
	root, err := portableRoot()
	if err != nil {
		return "", err
	}
	dir := os.Getenv("TAPAS_DATA_DIR")
	if dir != "" {
		if !filepath.IsAbs(dir) {
			return "", errors.New("TAPAS_DATA_DIR must be an absolute path")
		}
		if err := os.MkdirAll(dir, 0700); err != nil {
			return "", err
		}
	} else {
		dir = filepath.Join(root, "data")
		cwd, err := os.Getwd()
		if err != nil {
			return "", err
		}
		if err := initializePortableData(dir, legacyDataDir(cwd)); err != nil {
			return "", fmt.Errorf("cannot create data beside the executable: %w; move the app to a writable folder or set TAPAS_DATA_DIR", err)
		}
	}
	if err := os.Chdir(dir); err != nil {
		return "", err
	}
	return root, nil
}

func defaultDownloadRoot(root string) (string, error) {
	if dir := os.Getenv("TAPAS_DOWNLOAD_DIR"); dir != "" {
		if !filepath.IsAbs(dir) {
			return "", errors.New("TAPAS_DOWNLOAD_DIR must be an absolute path")
		}
		return filepath.Clean(dir), nil
	}
	return filepath.Join(root, "downloads"), nil
}

func startCommand(cmd *exec.Cmd) error {
	if err := cmd.Start(); err != nil {
		return err
	}
	return cmd.Process.Release()
}

func shouldOpenBrowser() bool {
	switch strings.ToLower(strings.TrimSpace(os.Getenv("OPEN_BROWSER"))) {
	case "false", "0", "no":
		return false
	case "true", "1", "yes":
		return true
	}
	if runtime.GOOS != "linux" {
		return true
	}
	if os.Getenv("WSL_DISTRO_NAME") != "" {
		return false
	}
	release, err := os.ReadFile("/proc/sys/kernel/osrelease")
	return err != nil || !strings.Contains(strings.ToLower(string(release)), "microsoft")
}

func openBrowser(url string) error {
	switch runtime.GOOS {
	case "windows":
		return startCommand(exec.Command("rundll32.exe", "url.dll,FileProtocolHandler", url))
	case "darwin":
		return startCommand(exec.Command("open", url))
	default:
		if _, err := exec.LookPath("explorer.exe"); err == nil && os.Getenv("WSL_DISTRO_NAME") != "" {
			return startCommand(exec.Command("explorer.exe", url))
		}
		return startCommand(exec.Command("xdg-open", url))
	}
}

func launchLocal(target string) error {
	switch runtime.GOOS {
	case "windows":
		return startCommand(exec.Command("explorer.exe", target))
	case "darwin":
		return startCommand(exec.Command("open", target))
	default:
		if _, err := exec.LookPath("explorer.exe"); err == nil && os.Getenv("WSL_DISTRO_NAME") != "" {
			path, err := exec.Command("wslpath", "-w", target).Output()
			if err != nil {
				return err
			}
			return startCommand(exec.Command("explorer.exe", strings.TrimSpace(string(path))))
		}
		return startCommand(exec.Command("xdg-open", target))
	}
}

func openWhenReady(url string, open func(string) error) {
	client := &http.Client{Timeout: 300 * time.Millisecond}
	for attempt := 0; attempt < 30; attempt++ {
		response, err := client.Get(url + "/api/health")
		if err == nil {
			response.Body.Close()
			if response.StatusCode == http.StatusNoContent {
				if err := open(url); err != nil {
					log.Printf("Could not open browser: %v; open %s manually", err, url)
				}
				return
			}
		}
		time.Sleep(100 * time.Millisecond)
	}
	log.Printf("Server did not become ready at %s", url)
}

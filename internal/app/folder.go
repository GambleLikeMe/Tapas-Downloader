package app

import (
	"errors"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
)

func validateDownloadDir(path string) (string, error) {
	path = strings.TrimSpace(path)
	if path == "" || !filepath.IsAbs(path) {
		return "", errors.New("choose an absolute download folder")
	}
	path = filepath.Clean(path)
	info, err := os.Stat(path)
	if err != nil {
		return "", fmt.Errorf("download folder %q is unavailable: %w", path, err)
	}
	if !info.IsDir() {
		return "", fmt.Errorf("download folder %q is a file", path)
	}
	probe, err := os.CreateTemp(path, ".tapas-write-*")
	if err != nil {
		return "", fmt.Errorf("download folder %q is not writable: %w", path, err)
	}
	name := probe.Name()
	if err := probe.Close(); err != nil {
		os.Remove(name)
		return "", err
	}
	if err := os.Remove(name); err != nil {
		return "", err
	}
	return path, nil
}

func pickFolder(initial string) (string, error) {
	var cmd *exec.Cmd
	switch runtime.GOOS {
	case "windows":
		script := `Add-Type -AssemblyName System.Windows.Forms; $d = New-Object System.Windows.Forms.FolderBrowserDialog; $d.Description = 'Choose download folder'; if (Test-Path -LiteralPath $env:TAPAS_PICK_INITIAL) { $d.SelectedPath = $env:TAPAS_PICK_INITIAL }; if ($d.ShowDialog() -eq 'OK') { [Console]::Out.Write($d.SelectedPath) }`
		cmd = exec.Command("powershell.exe", "-NoProfile", "-STA", "-Command", script)
		cmd.Env = append(os.Environ(), "TAPAS_PICK_INITIAL="+initial)
	case "darwin":
		cmd = exec.Command("osascript", "-e", `POSIX path of (choose folder with prompt "Choose download folder")`)
	default:
		if os.Getenv("WSL_DISTRO_NAME") != "" {
			if _, err := exec.LookPath("powershell.exe"); err == nil {
				script := `Add-Type -AssemblyName System.Windows.Forms; $d = New-Object System.Windows.Forms.FolderBrowserDialog; $d.Description = 'Choose download folder'; if ($d.ShowDialog() -eq 'OK') { [Console]::Out.Write($d.SelectedPath) }`
				output, err := exec.Command("powershell.exe", "-NoProfile", "-STA", "-Command", script).Output()
				if err != nil {
					return "", err
				}
				selected := strings.TrimSpace(string(output))
				if selected == "" {
					return "", nil
				}
				linux, err := exec.Command("wslpath", "-u", selected).Output()
				return strings.TrimSpace(string(linux)), err
			}
		}
		if _, err := exec.LookPath("zenity"); err == nil {
			cmd = exec.Command("zenity", "--file-selection", "--directory", "--title=Choose download folder")
		} else if _, err := exec.LookPath("kdialog"); err == nil {
			cmd = exec.Command("kdialog", "--getexistingdirectory", initial)
		} else {
			return "", errors.New("no native folder picker is installed; enter the folder path")
		}
	}
	output, err := cmd.Output()
	if err != nil {
		var exit *exec.ExitError
		if errors.As(err, &exit) && exit.ExitCode() == 1 {
			return "", nil
		}
		return "", err
	}
	return strings.TrimSpace(string(output)), nil
}

func (a *app) pickDownloadFolder(w http.ResponseWriter, r *http.Request) {
	a.mu.Lock()
	initial := a.config.DownloadDir
	a.mu.Unlock()
	selected, err := pickFolder(initial)
	if err != nil {
		fail(w, 501, err)
		return
	}
	if selected == "" {
		send(w, map[string]any{"canceled": true})
		return
	}
	selected, err = validateDownloadDir(selected)
	if err != nil {
		fail(w, 400, err)
		return
	}
	send(w, map[string]string{"path": selected})
}

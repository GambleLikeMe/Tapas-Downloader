package main

import (
	"archive/tar"
	"archive/zip"
	"compress/gzip"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
)

type packageFile struct {
	source, name string
	mode         os.FileMode
}

func main() {
	if err := packageApp(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func packageApp() error {
	version := os.Getenv("APP_VERSION")
	if version == "" {
		version = "dev"
	}
	if !regexp.MustCompile(`^(dev|v[0-9]+\.[0-9]+\.[0-9]+([-+][0-9A-Za-z.-]+)?)$`).MatchString(version) {
		return fmt.Errorf("invalid version %q", version)
	}
	temp, err := os.MkdirTemp("", "tapas-build-*")
	if err != nil {
		return err
	}
	defer os.RemoveAll(temp)
	binary := filepath.Join(temp, "TapasDownloader")
	platform := map[string]string{"windows": "Windows", "linux": "Linux", "darwin": "macOS"}[runtime.GOOS]
	if platform == "" {
		return fmt.Errorf("unsupported platform %s", runtime.GOOS)
	}
	if runtime.GOOS == "windows" {
		binary += ".exe"
	}
	build := exec.Command("go", "build", "-ldflags", "-X main.version="+version, "-o", binary, ".")
	build.Stdout, build.Stderr = os.Stdout, os.Stderr
	if err := build.Run(); err != nil {
		return err
	}
	smoke := exec.Command(binary, "--smoke-test")
	smoke.Stdout, smoke.Stderr = os.Stdout, os.Stderr
	if err := smoke.Run(); err != nil {
		return fmt.Errorf("package smoke test: %w", err)
	}
	arch := map[string]string{"amd64": "x64", "arm64": "arm64"}[runtime.GOARCH]
	if arch == "" {
		return fmt.Errorf("unsupported architecture %s", runtime.GOARCH)
	}
	name := fmt.Sprintf("TapasDownloader-%s-%s-%s", version, platform, arch)
	if err := os.MkdirAll(filepath.Join("dist", "release"), 0755); err != nil {
		return err
	}
	files := []packageFile{{binary, filepath.Base(binary), 0755}}
	if runtime.GOOS == "darwin" {
		app := "Tapas Downloader.app/Contents/"
		files[0].name = app + "MacOS/TapasDownloader"
		plist := filepath.Join(temp, "Info.plist")
		bundleVersion := strings.TrimPrefix(version, "v")
		if version == "dev" {
			bundleVersion = "0.0.0"
		}
		content := fmt.Sprintf(`<?xml version="1.0" encoding="UTF-8"?><!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd"><plist version="1.0"><dict><key>CFBundleName</key><string>Tapas Downloader</string><key>CFBundleIdentifier</key><string>local.tapas.downloader</string><key>CFBundleExecutable</key><string>TapasDownloader</string><key>CFBundlePackageType</key><string>APPL</string><key>CFBundleShortVersionString</key><string>%s</string><key>CFBundleVersion</key><string>%s</string></dict></plist>`, bundleVersion, bundleVersion)
		if err := os.WriteFile(plist, []byte(content), 0644); err != nil {
			return err
		}
		files = append(files, packageFile{plist, app + "Info.plist", 0644})
	}
	if runtime.GOOS == "linux" {
		return writeTarGz(filepath.Join("dist", "release", name+".tar.gz"), files)
	}
	return writeZip(filepath.Join("dist", "release", name+".zip"), files)
}

func writeZip(path string, files []packageFile) (err error) {
	output, err := os.Create(path)
	if err != nil {
		return err
	}
	defer output.Close()
	archive := zip.NewWriter(output)
	defer func() {
		if closeErr := archive.Close(); err == nil {
			err = closeErr
		}
	}()
	for _, file := range files {
		header := &zip.FileHeader{Name: file.name, Method: zip.Deflate}
		header.SetMode(file.mode)
		writer, err := archive.CreateHeader(header)
		if err != nil {
			return err
		}
		source, err := os.Open(file.source)
		if err != nil {
			return err
		}
		_, err = io.Copy(writer, source)
		source.Close()
		if err != nil {
			return err
		}
	}
	return nil
}

func writeTarGz(path string, files []packageFile) (err error) {
	output, err := os.Create(path)
	if err != nil {
		return err
	}
	defer output.Close()
	compressed := gzip.NewWriter(output)
	archive := tar.NewWriter(compressed)
	defer func() {
		if closeErr := archive.Close(); err == nil {
			err = closeErr
		}
		if closeErr := compressed.Close(); err == nil {
			err = closeErr
		}
	}()
	for _, file := range files {
		source, err := os.Open(file.source)
		if err != nil {
			return err
		}
		info, err := source.Stat()
		if err != nil {
			source.Close()
			return err
		}
		header := &tar.Header{Name: file.name, Mode: int64(file.mode), Size: info.Size(), ModTime: info.ModTime()}
		if err := archive.WriteHeader(header); err != nil {
			source.Close()
			return err
		}
		_, err = io.Copy(archive, source)
		source.Close()
		if err != nil {
			return err
		}
	}
	return nil
}

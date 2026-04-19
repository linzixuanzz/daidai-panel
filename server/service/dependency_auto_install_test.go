package service

import (
	"os"
	"path/filepath"
	"testing"
)

func TestDetectAutoInstallCandidate(t *testing.T) {
	t.Run("python alias", func(t *testing.T) {
		candidate := DetectAutoInstallCandidate(".py", "ModuleNotFoundError: No module named 'Crypto.Hash'", t.TempDir())
		if candidate == nil {
			t.Fatal("expected python candidate")
		}
		if candidate.Manager != "python" || candidate.PackageName != "pycryptodome" {
			t.Fatalf("unexpected python candidate: %+v", candidate)
		}
	})

	t.Run("python Cryptodome alias maps to pycryptodomex", func(t *testing.T) {
		// 回归：from Cryptodome.PublicKey import RSA 失败时，必须 pip install pycryptodomex，
		// 不能原样 install Cryptodome（PyPI 没这个名字，会直接 404）。
		candidate := DetectAutoInstallCandidate(".py", "ModuleNotFoundError: No module named 'Cryptodome'", t.TempDir())
		if candidate == nil {
			t.Fatal("expected python candidate")
		}
		if candidate.Manager != "python" || candidate.PackageName != "pycryptodomex" {
			t.Fatalf("unexpected python candidate: %+v", candidate)
		}
	})

	t.Run("node package", func(t *testing.T) {
		candidate := DetectAutoInstallCandidate(".js", "Error: Cannot find module 'axios'", t.TempDir())
		if candidate == nil {
			t.Fatal("expected node candidate")
		}
		if candidate.Manager != "nodejs" || candidate.PackageName != "axios" {
			t.Fatalf("unexpected node candidate: %+v", candidate)
		}
	})

	t.Run("node relative module ignored", func(t *testing.T) {
		candidate := DetectAutoInstallCandidate(".js", "Error: Cannot find module './local-helper'", t.TempDir())
		if candidate != nil {
			t.Fatalf("expected nil candidate, got %+v", candidate)
		}
	})

	t.Run("go module requires go.mod", func(t *testing.T) {
		workDir := t.TempDir()
		if err := os.WriteFile(filepath.Join(workDir, "go.mod"), []byte("module example.com/demo\n\ngo 1.25\n"), 0644); err != nil {
			t.Fatalf("write go.mod: %v", err)
		}
		candidate := DetectAutoInstallCandidate(".go", "main.go:5:2: no required module provides package github.com/gin-gonic/gin; to add it:\n\tgo get github.com/gin-gonic/gin", workDir)
		if candidate == nil {
			t.Fatal("expected go candidate")
		}
		if candidate.Manager != "go" || candidate.PackageName != "github.com/gin-gonic/gin" {
			t.Fatalf("unexpected go candidate: %+v", candidate)
		}
	})

	t.Run("go without module manifest is ignored", func(t *testing.T) {
		candidate := DetectAutoInstallCandidate(".go", "main.go:5:2: no required module provides package github.com/gin-gonic/gin", t.TempDir())
		if candidate != nil {
			t.Fatalf("expected nil candidate, got %+v", candidate)
		}
	})

	t.Run("node hint npm install", func(t *testing.T) {
		candidate := DetectAutoInstallCandidate(".js", `缺少有效 https-proxy-agent 模块！请运行: npm install https-proxy-agent`, t.TempDir())
		if candidate == nil {
			t.Fatal("expected node candidate from npm install hint")
		}
		if candidate.Manager != "nodejs" || candidate.PackageName != "https-proxy-agent" {
			t.Fatalf("unexpected candidate: %+v", candidate)
		}
	})

	t.Run("python hint pip install", func(t *testing.T) {
		candidate := DetectAutoInstallCandidate(".py", `请先安装依赖: pip install beautifulsoup4`, t.TempDir())
		if candidate == nil {
			t.Fatal("expected python candidate from pip install hint")
		}
		if candidate.Manager != "python" || candidate.PackageName != "beautifulsoup4" {
			t.Fatalf("unexpected candidate: %+v", candidate)
		}
	})

	t.Run("node native error takes priority over hint", func(t *testing.T) {
		output := "Error: Cannot find module 'axios'\nnpm install axios to fix"
		candidate := DetectAutoInstallCandidate(".js", output, t.TempDir())
		if candidate == nil {
			t.Fatal("expected candidate")
		}
		if candidate.PackageName != "axios" {
			t.Fatalf("expected axios, got %s", candidate.PackageName)
		}
	})

	t.Run("python local so not installed", func(t *testing.T) {
		workDir := t.TempDir()
		os.WriteFile(filepath.Join(workDir, "loader_v2.so"), []byte{}, 0644)
		candidate := DetectAutoInstallCandidate(".py", "ModuleNotFoundError: No module named 'loader_v2'", workDir)
		if candidate != nil {
			t.Fatalf("expected nil for local .so file, got %+v", candidate)
		}
	})
}

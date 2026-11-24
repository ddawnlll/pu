package main

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	osexec "os/exec"
	"runtime"

	"github.com/ddawnlll/pu/internal/catalog"
	puexec "github.com/ddawnlll/pu/internal/exec"
)

type doctorDeps struct {
	modelsPath string
	getenv     func(string) string
	lookPath   func(string) (string, error)
}

func runDoctor(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("doctor", flag.ContinueOnError)
	fs.SetOutput(stderr)
	modelsPath := fs.String("models", "", "path to models.toml (default ~/.pu/models.toml)")
	if err := fs.Parse(args); err != nil {
		return 2
	}

	path := *modelsPath
	if path == "" {
		if envPath := os.Getenv("PU_MODELS_FILE"); envPath != "" {
			path = envPath
		} else if p, err := catalog.DefaultPath(); err == nil {
			path = p
		}
	}

	return execDoctor(doctorDeps{
		modelsPath: path,
		getenv:     os.Getenv,
		lookPath:   osexec.LookPath,
	}, stdout)
}

func execDoctor(deps doctorDeps, stdout io.Writer) int {
	if deps.getenv == nil {
		deps.getenv = os.Getenv
	}
	if deps.lookPath == nil {
		deps.lookPath = osexec.LookPath
	}

	failed := false

	// 1. Go version
	fmt.Fprintf(stdout, "[ok]   go: %s (%s/%s)\n", runtime.Version(), runtime.GOOS, runtime.GOARCH)

	// 2. models.toml validity + 3. API key presence (masked)
	if deps.modelsPath == "" {
		fmt.Fprintf(stdout, "[warn] models.toml: could not resolve default path\n")
	} else {
		cat, err := catalog.Load(deps.modelsPath)
		if err != nil {
			if errors.Is(err, os.ErrNotExist) {
				fmt.Fprintf(stdout, "[warn] models.toml: not found at %s\n", deps.modelsPath)
				// Still report OPENCODE_API_KEY status (masked) for convenience.
				if k := deps.getenv("OPENCODE_API_KEY"); k != "" {
					fmt.Fprintf(stdout, "[ok]   key OPENCODE_API_KEY: set (%s)\n", catalog.MaskSecret(k))
				} else {
					fmt.Fprintf(stdout, "[warn] key OPENCODE_API_KEY: not set\n")
				}
			} else {
				fmt.Fprintf(stdout, "[fail] models.toml (%s): %v\n", deps.modelsPath, err)
				failed = true
			}
		} else {
			fmt.Fprintf(stdout, "[ok]   models.toml: valid (%s, %d model(s))\n", deps.modelsPath, len(cat.Models))
			for _, name := range cat.Names() {
				m := cat.Models[name]
				key, envVar, required, kErr := m.ResolveAPIKey(deps.getenv)
				if kErr != nil {
					fmt.Fprintf(stdout, "[fail] key [%s]: %v\n", name, kErr)
					failed = true
					continue
				}
				if !required {
					fmt.Fprintf(stdout, "[ok]   key [%s]: none required\n", name)
					continue
				}
				if key == "" {
					fmt.Fprintf(stdout, "[warn] key [%s] (%s): not set\n", name, envVar)
				} else {
					fmt.Fprintf(stdout, "[ok]   key [%s] (%s): set (%s)\n", name, envVar, catalog.MaskSecret(key))
				}
			}
		}
	}

	// 4. rg presence
	if rgPath, err := deps.lookPath("rg"); err == nil {
		fmt.Fprintf(stdout, "[ok]   rg: %s\n", rgPath)
	} else {
		fmt.Fprintf(stdout, "[warn] rg: not found in PATH (pure-Go fallback will be used)\n")
	}

	// 5. env-scrub self-test
	if err := puexec.SelfTestEnvScrub(); err != nil {
		fmt.Fprintf(stdout, "[fail] env-scrub: %v\n", err)
		failed = true
	} else {
		fmt.Fprintf(stdout, "[ok]   env-scrub: self-test passed\n")
	}

	if failed {
		return 1
	}
	return 0
}

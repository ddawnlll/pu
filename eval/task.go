package eval

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/BurntSushi/toml"
)

// Task describes one evaluation task loaded from <task-dir>/task.toml.
type Task struct {
	ID         string `toml:"id"`
	Repo       string `toml:"repo"`
	Commit     string `toml:"commit"`
	Prompt     string `toml:"prompt"`
	Acceptance string `toml:"acceptance"`
	Category   string `toml:"category"`
	Dir        string `toml:"-"`
}

// LoadTasks scans tasksDir for subdirectories containing task.toml, sorted by name.
func LoadTasks(tasksDir string) ([]Task, error) {
	entries, err := os.ReadDir(tasksDir)
	if err != nil {
		return nil, fmt.Errorf("read tasks dir %s: %w", tasksDir, err)
	}

	var tasks []Task
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		dir := filepath.Join(tasksDir, e.Name())
		tomlPath := filepath.Join(dir, "task.toml")
		data, err := os.ReadFile(tomlPath)
		if err != nil {
			if os.IsNotExist(err) {
				continue
			}
			return nil, fmt.Errorf("read %s: %w", tomlPath, err)
		}

		var t Task
		md, err := toml.Decode(string(data), &t)
		if err != nil {
			return nil, fmt.Errorf("decode %s: %w", tomlPath, err)
		}
		if undecoded := md.Undecoded(); len(undecoded) > 0 {
			keys := make([]string, len(undecoded))
			for i, k := range undecoded {
				keys[i] = k.String()
			}
			return nil, fmt.Errorf("%s has unknown keys: %s", tomlPath, strings.Join(keys, ", "))
		}

		if t.ID == "" {
			t.ID = e.Name()
		}
		t.Dir = dir
		if t.Repo == "" || t.Commit == "" || t.Prompt == "" || t.Acceptance == "" {
			return nil, fmt.Errorf("%s: repo, commit, prompt, and acceptance are all required", tomlPath)
		}
		if !filepath.IsAbs(t.Repo) {
			t.Repo = filepath.Clean(filepath.Join(dir, t.Repo))
		}
		tasks = append(tasks, t)
	}

	sort.Slice(tasks, func(i, j int) bool { return tasks[i].ID < tasks[j].ID })
	if len(tasks) == 0 {
		return nil, fmt.Errorf("no task.toml files found under %s", tasksDir)
	}
	return tasks, nil
}

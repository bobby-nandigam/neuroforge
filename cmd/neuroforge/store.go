package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"time"
)

func runsDir() string { return filepath.Join(".neuroforge", "runs") }

func newExecID() string {
	return fmt.Sprintf("run-%s-%04d", time.Now().Format("20060102-150405"), os.Getpid()%10000)
}

func saveRun(rec RunRecord) error {
	if err := os.MkdirAll(runsDir(), 0o755); err != nil {
		return err
	}
	data, err := json.MarshalIndent(rec, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(runsDir(), rec.ID+".json"), data, 0o644)
}

func loadRun(id string) (RunRecord, error) {
	var rec RunRecord
	data, err := os.ReadFile(filepath.Join(runsDir(), id+".json"))
	if err != nil {
		return rec, fmt.Errorf("no such execution %q (looked in %s)", id, runsDir())
	}
	return rec, json.Unmarshal(data, &rec)
}

func listRuns() error {
	entries, err := os.ReadDir(runsDir())
	if err != nil {
		fmt.Println("no runs recorded yet")
		return nil
	}
	var ids []string
	for _, e := range entries {
		if filepath.Ext(e.Name()) == ".json" {
			ids = append(ids, e.Name()[:len(e.Name())-5])
		}
	}
	sort.Sort(sort.Reverse(sort.StringSlice(ids)))
	fmt.Printf("recorded executions (%d):\n", len(ids))
	for _, id := range ids {
		fmt.Printf("  %s\n", id)
	}
	fmt.Println("\nview one: neuroforge profile <exec-id>")
	return nil
}

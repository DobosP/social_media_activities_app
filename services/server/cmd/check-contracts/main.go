package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"path/filepath"

	"github.com/DobosP/social_media_activities_app/services/server/internal/contracts"
)

func main() {
	root := flag.String("root", ".", "repository root")
	freeze := flag.Bool("freeze", false, "emit immutable legacy test declaration inventory; runs no reference code")
	head := flag.String("source-head", "", "exact baseline commit for inventory provenance")
	inventoryPath := flag.String("inventory", "services/server/internal/contracts/testdata/legacy-test-inventory.json", "frozen source case inventory")
	summary := flag.Bool("summary", false, "emit counts only; unresolved cases still fail the gate")
	flag.Parse()
	encoder := json.NewEncoder(os.Stdout)
	encoder.SetIndent("", "  ")
	if *freeze {
		if *head == "" {
			fmt.Fprintln(os.Stderr, "source-head is required")
			os.Exit(2)
		}
		inventory, err := contracts.ScanInventory(*root, *head)
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		if encoder.Encode(inventory) != nil {
			os.Exit(1)
		}
		return
	}
	var inventory contracts.Inventory
	if err := contracts.ReadJSON(filepath.Join(*root, *inventoryPath), &inventory); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	files, err := filepath.Glob(filepath.Join(*root, "services/server/internal/contracts/testdata/*-coverage.json"))
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	manifests := []contracts.Manifest{}
	for _, file := range files {
		var manifest contracts.Manifest
		if err := contracts.ReadJSON(file, &manifest); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		manifests = append(manifests, manifest)
	}
	report := contracts.Check(*root, inventory, manifests)
	var output any = report
	if *summary {
		output = map[string]int{"total_source_cases": report.Total, "verified_source_cases": report.Verified, "unresolved_cases": len(report.Unresolved), "invalid_evidence": len(report.Invalid)}
	}
	if encoder.Encode(output) != nil || len(report.Unresolved) > 0 || len(report.Invalid) > 0 {
		os.Exit(1)
	}
}

// Command social-gui-normalize is a tooling-only bridge to the released public
// SDK. Build it in a verified private copy of the released Go module, never in
// the Social app module. No caller-selected oracle or asset masks are accepted.
package main

import (
	"encoding/json"
	"fmt"
	"io"
	"os"

	"github.com/DobosP/roedu-ui/web-kit/golden"
)

const policy = "core-v1.6/golden.Normalize/strict"

type result struct {
	Schema     int      `json:"schema"`
	Policy     string   `json:"policy"`
	Normalized []byte   `json:"normalized"`
	Hard       []string `json:"hard"`
}

func normalize(input io.Reader, output io.Writer) error {
	raw, err := io.ReadAll(io.LimitReader(input, (512<<10)+1))
	if err != nil || len(raw) == 0 || len(raw) > 512<<10 {
		return fmt.Errorf("one nonempty bounded raw HTML input required")
	}
	canonical, hard := golden.Normalize(raw, golden.Options{})
	if len(canonical) == 0 || len(canonical) > 4<<20 {
		return fmt.Errorf("released normalizer output exceeded the bridge bound")
	}
	if err := json.NewEncoder(output).Encode(result{1, policy, canonical, hard}); err != nil {
		return err
	}
	if len(hard) != 0 {
		return fmt.Errorf("released normalizer returned hard findings")
	}
	return nil
}

func main() {
	if len(os.Args) != 1 {
		fmt.Fprintln(os.Stderr, "raw stdin/stdout only; no alternate policy arguments")
		os.Exit(2)
	}
	if err := normalize(os.Stdin, os.Stdout); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

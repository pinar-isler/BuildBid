package main

import (
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"

	matchingbench "github.com/pinar-isler/BuildBid/validation/benchmark/matching"
)

func main() {
	os.Exit(run(os.Args[1:], os.Stdout, os.Stderr))
}

func run(args []string, stdout, stderr io.Writer) int {
	flags := flag.NewFlagSet("evaluate-matching", flag.ContinueOnError)
	flags.SetOutput(stderr)
	split := flags.String("split", "both", "corpus split: development, held-out (regression), or both")
	corpus := flags.String("corpus", "", "directory containing the matching corpus manifest")
	if err := flags.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return 0
		}
		return 2
	}
	if flags.NArg() != 0 {
		fmt.Fprintln(stderr, "evaluate-matching takes no positional arguments")
		return 2
	}
	switch *split {
	case "both", matchingbench.Development, matchingbench.HeldOut:
	default:
		fmt.Fprintln(stderr, "evaluate-matching: split must be development, held-out, or both")
		return 2
	}

	explicitCorpus := false
	flags.Visit(func(f *flag.Flag) {
		if f.Name == "corpus" {
			explicitCorpus = true
		}
	})
	corpusPath := *corpus
	if !explicitCorpus {
		var err error
		corpusPath, err = discoverCorpus()
		if err != nil {
			fmt.Fprintf(stderr, "evaluate-matching: %v\n", err)
			return 1
		}
	}
	if err := writeReport(os.DirFS(corpusPath), *split, stdout); err != nil {
		fmt.Fprintf(stderr, "evaluate-matching: %v\n", err)
		return 1
	}
	return 0
}

func discoverCorpus() (string, error) {
	for _, dir := range []string{"corpus/v1", "validation/corpus/v1"} {
		info, err := os.Stat(filepath.Join(dir, "manifest.json"))
		if err == nil && info.Mode().IsRegular() {
			return dir, nil
		}
	}
	return "", errors.New("matching corpus not found, run from the BuildBid root or validation/ or supply --corpus")
}

func writeReport(corpus fs.FS, split string, stdout io.Writer) error {
	var result any
	if split == "both" {
		reports := make([]matchingbench.Report, 0, 2)
		for _, name := range []string{matchingbench.Development, matchingbench.HeldOut} {
			report, err := matchingbench.Evaluate(corpus, name)
			if err != nil {
				return err
			}
			reports = append(reports, report)
		}
		result = reports
	} else {
		report, err := matchingbench.Evaluate(corpus, split)
		if err != nil {
			return err
		}
		result = report
	}
	encoder := json.NewEncoder(stdout)
	encoder.SetEscapeHTML(false)
	encoder.SetIndent("", "  ")
	return encoder.Encode(result)
}

package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/pinar-isler/BuildBid/validation/benchmark/extraction"
)

func main() {
	os.Exit(run(context.Background(), os.Args[1:], os.Stdout, os.Stderr))
}

func run(ctx context.Context, args []string, stdout, stderr io.Writer) int {
	flags := flag.NewFlagSet("evaluate-extraction", flag.ContinueOnError)
	flags.SetOutput(stderr)
	mock := flags.Bool("mock", false, "run fixture replay with quality metrics suppressed")
	predictions := flags.String("predictions", "", "path to a recorded prediction bundle")
	split := flags.String("split", extractionbench.Both, "development, held-out, or both")
	format := flags.String("format", "console", "console or json")
	root := flags.String("repo-root", "", "BuildBid checkout directory")
	if err := flags.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return 0
		}
		return 2
	}
	fail := func(err error) int {
		fmt.Fprintf(stderr, "evaluate-extraction: %v\n", err)
		return 2
	}
	hasPredictions := false
	flags.Visit(func(f *flag.Flag) { hasPredictions = hasPredictions || f.Name == "predictions" })
	if flags.NArg() != 0 || *mock == hasPredictions {
		return fail(errors.New("Supply exactly one of --mock or --predictions PATH."))
	}
	if *format != "console" && *format != "json" {
		return fail(errors.New("Choose console or json for the report format."))
	}
	switch *split {
	case extractionbench.Development, extractionbench.HeldOut, extractionbench.Both:
	default:
		return fail(errors.New("Choose development, held-out, or both."))
	}
	var err error
	if *root == "" {
		*root, err = findRepoRoot()
		if err != nil {
			return fail(err)
		}
	}
	options := extractionbench.Options{
		CorpusDir: filepath.Join(*root, "validation", "corpus", "v1"),
		Schemas:   os.DirFS(filepath.Join(*root, "Schemas")),
		Split:     *split,
	}
	if hasPredictions {
		options.Predictions, err = os.ReadFile(*predictions)
		if err != nil {
			return fail(errors.New("The local prediction bundle could not be read."))
		}
	}
	report, err := extractionbench.Evaluate(ctx, options)
	if err != nil {
		return fail(err)
	}
	if *format == "json" {
		var encoded []byte
		encoded, err = extractionbench.MarshalReport(report)
		if err == nil {
			_, err = fmt.Fprintln(stdout, string(encoded))
		}
	} else {
		_, err = fmt.Fprintln(stdout, extractionbench.ConsoleReport(report))
	}
	if err != nil {
		return fail(errors.New("The benchmark report could not be written."))
	}
	return 0
}

func findRepoRoot() (string, error) {
	dir, err := os.Getwd()
	if err != nil {
		return "", err
	}
	for {
		info, err := os.Stat(filepath.Join(dir, "Schemas", "Vendor_Quotation_Schema"))
		if err == nil && info.Mode().IsRegular() {
			return dir, nil
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return "", errors.New("Run from the BuildBid checkout or supply --repo-root.")
		}
		dir = parent
	}
}

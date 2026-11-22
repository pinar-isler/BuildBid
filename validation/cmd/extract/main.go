package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/pinar-isler/BuildBid/validation/extraction"
)

const usage = "Usage: extract DOCUMENT --kind quotation|title_deed [--mock-response PATH] [--repo-root PATH]"

type options struct {
	input        string
	kind         string
	response     string
	repoRoot     string
	explicit     bool
	inputPresent bool
}

func main() {
	os.Exit(run(context.Background(), os.Args[1:], os.Stdout, os.Stderr))
}

func run(ctx context.Context, args []string, stdout, stderr io.Writer) int {
	opt, help, err := parseArgs(args)
	if err != nil {
		fmt.Fprintln(stderr, usage)
		fmt.Fprintln(stderr, err)
		return 2
	}
	if help {
		fmt.Fprintln(stdout, usage)
		return 0
	}
	root := opt.repoRoot
	if root == "" {
		root, err = findRepoRoot()
		if err != nil {
			fmt.Fprintln(stderr, "Run from the BuildBid checkout or supply --repo-root.")
			return 2
		}
	}
	validators, err := extraction.LoadValidators(os.DirFS(filepath.Join(root, "Schemas")))
	if err != nil {
		fmt.Fprintln(stderr, "The local canonical extraction schemas could not be loaded.")
		return 2
	}
	var provider extraction.Provider
	if opt.explicit {
		provider = extraction.MockResponseProvider{Path: opt.response}
	} else {
		provider, err = extraction.NewMockProvider(os.DirFS(filepath.Join(root, "validation", "corpus", "v1")))
		if err != nil {
			fmt.Fprintln(stderr, "The local extraction replay corpus could not be loaded.")
			return 2
		}
	}
	pipeline, err := extraction.NewPipeline(provider, validators, extraction.DefaultMaxBytes)
	if err != nil {
		fmt.Fprintln(stderr, "The local extraction pipeline could not be initialized.")
		return 2
	}
	result := pipeline.Run(ctx, opt.input, extraction.Kind(opt.kind))
	encoded, err := result.MarshalJSON()
	if err != nil {
		fmt.Fprintln(stderr, "The extraction result could not be encoded.")
		return 2
	}
	if _, err := fmt.Fprintln(stdout, string(encoded)); err != nil {
		fmt.Fprintln(stderr, "The extraction result could not be written.")
		return 2
	}
	switch result.Status {
	case extraction.Complete:
		return 0
	case extraction.NeedsReview:
		return 1
	default:
		return 2
	}
}

func parseArgs(args []string) (options, bool, error) {
	var opt options
	flags := map[string]*string{"--kind": &opt.kind, "--mock-response": &opt.response, "--repo-root": &opt.repoRoot}
	positionalOnly := false
	for i := 0; i < len(args); i++ {
		arg := args[i]
		if !positionalOnly {
			switch arg {
			case "-h", "--help":
				return opt, true, nil
			case "--":
				positionalOnly = true
				continue
			}
			if arg != "-" && strings.HasPrefix(arg, "-") {
				flag, value, inline := strings.Cut(arg, "=")
				target, ok := flags[flag]
				if !ok {
					return opt, false, errors.New("unknown option")
				}
				if !inline {
					if i+1 >= len(args) || (args[i+1] != "-" && strings.HasPrefix(args[i+1], "-")) {
						return opt, false, fmt.Errorf("%s requires a value", flag)
					}
					i++
					value = args[i]
				}
				*target = value
				opt.explicit = opt.explicit || flag == "--mock-response"
				continue
			}
		}
		if opt.inputPresent {
			return opt, false, errors.New("expected one document path")
		}
		opt.input, opt.inputPresent = arg, true
	}
	if !opt.inputPresent || !extraction.Kind(opt.kind).Supported() {
		return opt, false, errors.New("a document path and --kind quotation or title_deed are required")
	}
	return opt, false, nil
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
			return "", errors.New("could not find the BuildBid checkout")
		}
		dir = parent
	}
}

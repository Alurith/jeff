package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"os"

	"jeff/evals/internal/harness"
)

func main() {
	os.Exit(run(os.Args[1:], os.Stdout, os.Stderr))
}

func run(args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 || args[0] == "--help" || args[0] == "-h" {
		usage(stdout)
		return 0
	}
	switch args[0] {
	case "validate":
		return runValidate(args[1:], stdout, stderr)
	case "run":
		return runEvaluation(args[1:], stdout, stderr)
	default:
		fmt.Fprintf(stderr, "error: unknown command %q\n", args[0])
		usage(stderr)
		return 2
	}
}

func runValidate(args []string, stdout, stderr io.Writer) int {
	flags := flag.NewFlagSet("jeff-eval validate", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	var datasets pathList
	flags.Var(&datasets, "dataset", "dataset manifest path; may be repeated")
	if err := flags.Parse(args); err != nil {
		fmt.Fprintln(stderr, err)
		return 2
	}
	if len(datasets) == 0 {
		fmt.Fprintln(stderr, "error: at least one --dataset is required")
		return 2
	}
	suite, err := harness.LoadDatasetSuite(datasets...)
	if err != nil {
		fmt.Fprintf(stderr, "error: %s\n", err)
		return 2
	}
	fmt.Fprintf(stdout, "validated %d dataset(s), suite hash %s\n", len(suite.Datasets), suite.Hash)
	return 0
}

func runEvaluation(args []string, stdout, stderr io.Writer) int {
	flags := flag.NewFlagSet("jeff-eval run", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	var datasetPath string
	var configPath string
	var jeffBinary string
	var profile string
	var provider string
	var outputDir string
	var baselinePath string
	var repetitions int
	flags.StringVar(&datasetPath, "dataset", "", "dataset manifest path")
	flags.StringVar(&configPath, "config", "evals/config.yaml", "harness config path")
	flags.StringVar(&jeffBinary, "jeff-bin", "", "Jeff binary; omitted builds ./cmd/jeff")
	flags.StringVar(&profile, "profile", "all", "selection profile: all or smoke")
	flags.StringVar(&provider, "provider", "synthetic", "provider mode: synthetic or real")
	flags.StringVar(&outputDir, "out", "evals/out", "empty output directory")
	flags.StringVar(&baselinePath, "baseline", "", "approved baseline results.json")
	flags.IntVar(&repetitions, "repetitions", 0, "override configured repetitions")
	if err := flags.Parse(args); err != nil {
		fmt.Fprintln(stderr, err)
		return 2
	}
	if datasetPath == "" {
		fmt.Fprintln(stderr, "error: --dataset is required")
		return 2
	}
	dataset, err := harness.LoadDataset(datasetPath)
	if err != nil {
		fmt.Fprintf(stderr, "error: %s\n", err)
		return 2
	}
	config, err := harness.LoadConfig(configPath)
	if err != nil {
		fmt.Fprintf(stderr, "error: %s\n", err)
		return 2
	}
	if repetitions != 0 {
		config.Repetitions = repetitions
		if err := config.Validate(); err != nil {
			fmt.Fprintf(stderr, "error: %s\n", err)
			return 2
		}
	}
	results, err := harness.Run(context.Background(), harness.RunOptions{
		Dataset: dataset, Config: config, JeffBinary: jeffBinary,
		Profile: profile, Provider: provider, OutputDir: outputDir,
	})
	if err != nil {
		fmt.Fprintf(stderr, "error: %s\n", err)
		return 2
	}
	exitCode := results.ExitCode()
	if baselinePath != "" {
		baseline, loadErr := harness.LoadBaseline(baselinePath)
		if loadErr != nil {
			fmt.Fprintf(stderr, "error: %s\n", loadErr)
			return 2
		}
		if compareErr := harness.AttachBaseline(&results, baseline, config.Gates); compareErr != nil {
			fmt.Fprintf(stderr, "error: %s\n", compareErr)
			return 2
		}
		if err := harness.WriteArtifacts(outputDir, results); err != nil {
			fmt.Fprintf(stderr, "error: %s\n", err)
			return 2
		}
		if !results.Baseline.Passed && exitCode == 0 {
			exitCode = 1
		}
	}
	fmt.Fprintf(stdout, "wrote %d case result(s) to %s/results.json\n", len(results.Cases), outputDir)
	return exitCode
}

func usage(w io.Writer) {
	fmt.Fprintln(w, "Usage:")
	fmt.Fprintln(w, "  jeff-eval validate --dataset PATH [--dataset PATH ...]")
	fmt.Fprintln(w, "  jeff-eval run --dataset PATH [--config PATH] [--profile all|smoke]")
	fmt.Fprintln(w, "      [--provider synthetic|real] [--repetitions N] [--jeff-bin PATH] [--out DIR] [--baseline PATH]")
}

type pathList []string

func (p *pathList) String() string {
	return fmt.Sprint([]string(*p))
}

func (p *pathList) Set(value string) error {
	if value == "" {
		return fmt.Errorf("path must not be empty")
	}
	*p = append(*p, value)
	return nil
}

package main

import (
	"bytes"
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"strings"

	"jeff/internal/check"
	"jeff/internal/config"
	"jeff/internal/credentials"
	"jeff/internal/files"

	"golang.org/x/term"
)

func main() {
	os.Exit(run(os.Args[1:], os.Stdin, os.Stdout, os.Stderr))
}

func run(args []string, stdin io.Reader, stdout, stderr io.Writer) int {
	if len(args) == 0 || args[0] == "--help" || args[0] == "-h" {
		usage(stdout)
		return 0
	}
	if args[0] == "auth" {
		return runAuth(args[1:], stdin, stdout, stderr)
	}
	if args[0] != "check" {
		err := fmt.Errorf("unknown command %q", args[0])
		if requestedOutputFormat(args[1:]) == "json" {
			return writeJSONError(stdout, stderr, check.ErrorKindUsage, err)
		}
		fmt.Fprintf(stderr, "error: %s\n", err)
		usage(stderr)
		return 2
	}

	flagArgs, paths, requestedFormat, formatSet := partitionCheckArgs(args[1:])
	flags := flag.NewFlagSet("jeff check", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	format := flags.String("output-format", "", "output format: text or json")
	configPath := flags.String("config", "", "path to a jeff TOML configuration file")
	noCache := flags.Bool("no-cache", false, "do not read or write the answer cache")
	flags.Usage = func() {}
	if err := flags.Parse(flagArgs); err != nil {
		if err == flag.ErrHelp {
			writeCheckUsage(stderr, flags)
			return 0
		}
		if requestedFormat == "json" {
			return writeJSONError(stdout, stderr, check.ErrorKindUsage, err)
		}
		fmt.Fprintln(stderr, err)
		writeCheckUsage(stderr, flags)
		return 2
	}
	root, err := files.ResolveRoot("")
	if err != nil {
		return writeResult(requestedFormat, stdout, stderr, check.NewErrorResult(check.ErrorKindInput, "", err))
	}
	settings, err := config.Load(root, *configPath)
	if err != nil {
		if !formatSet && settings.OutputFormat != "" {
			requestedFormat = settings.OutputFormat
		}
		return writeResult(requestedFormat, stdout, stderr, check.NewErrorResult(check.ErrorKindConfig, "", err))
	}
	if !formatSet {
		*format = settings.OutputFormat
	}
	if *format == "" {
		*format = "text"
	}
	if *format != "text" && *format != "json" {
		fmt.Fprintf(stderr, "error: unsupported output format %q\n", *format)
		return 2
	}

	if len(paths) == 0 && len(settings.Src) > 0 {
		paths = settings.Src
	}
	apiKey, credentialErr := credentials.LoadAPIKey()
	var result check.Result
	if credentialErr != nil {
		result = check.NewErrorResult(check.ErrorKindConfig, "", credentialErr)
	} else {
		result = check.Run(context.Background(), check.Options{
			Root:       root,
			Paths:      paths,
			Exclude:    settings.Exclude,
			Include:    settings.Include,
			RuleFiles:  settings.RuleFiles,
			JevVersion: settings.JevVersion,
			CacheDir:   settings.CacheDir,
			BaseURL:    os.Getenv("TYPESAFE_BASE_URL"),
			APIKey:     apiKey,
			NoCache:    *noCache,
		})
	}
	return writeResult(*format, stdout, stderr, result)
}

func runAuth(args []string, stdin io.Reader, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		fmt.Fprintln(stderr, "error: missing auth command")
		authUsage(stderr)
		return 2
	}
	if (args[0] == "--help" || args[0] == "-h") && len(args) == 1 {
		authUsage(stdout)
		return 0
	}
	if len(args) != 1 {
		fmt.Fprintf(stderr, "error: jeff auth %s accepts no arguments\n", args[0])
		authUsage(stderr)
		return 2
	}

	switch args[0] {
	case "login":
		value, err := readAPIKey(stdin, stderr)
		if err != nil {
			fmt.Fprintf(stderr, "error: %s\n", err)
			return 2
		}
		defer credentials.Clear(value)
		if err := credentials.SaveAPIKey(value); err != nil {
			fmt.Fprintf(stderr, "error: %s\n", err)
			return 2
		}
		fmt.Fprintln(stdout, "API key stored in system keyring.")
		return 0
	case "logout":
		if err := credentials.DeleteAPIKey(); err != nil {
			fmt.Fprintf(stderr, "error: %s\n", err)
			return 2
		}
		fmt.Fprintln(stdout, "API key removed from system keyring.")
		return 0
	default:
		fmt.Fprintf(stderr, "error: unknown auth command %q\n", args[0])
		authUsage(stderr)
		return 2
	}
}

func readAPIKey(input io.Reader, output io.Writer) ([]byte, error) {
	if file, ok := input.(*os.File); ok && term.IsTerminal(int(file.Fd())) {
		if _, err := fmt.Fprint(output, "TypeSafe API key: "); err != nil {
			return nil, err
		}
		value, err := readTTYAPIKey(file)
		_, _ = fmt.Fprintln(output)
		if err != nil {
			credentials.Clear(value)
			return nil, fmt.Errorf("read API key: %w", err)
		}
		if err := credentials.ValidateAPIKey(value); err != nil {
			credentials.Clear(value)
			return nil, err
		}
		return value, nil
	}

	raw, err := io.ReadAll(io.LimitReader(input, int64(credentials.MaxAPIKeyBytes+3)))
	if err != nil {
		credentials.Clear(raw)
		return nil, fmt.Errorf("read API key: %w", err)
	}
	value := raw
	if bytes.HasSuffix(value, []byte{'\n'}) {
		value = value[:len(value)-1]
		if bytes.HasSuffix(value, []byte{'\r'}) {
			value = value[:len(value)-1]
		}
	}
	value = append([]byte(nil), value...)
	credentials.Clear(raw)
	if err := credentials.ValidateAPIKey(value); err != nil {
		credentials.Clear(value)
		return nil, err
	}
	return value, nil
}

func readTTYAPIKey(input *os.File) (value []byte, err error) {
	fd := int(input.Fd())
	state, err := term.MakeRaw(fd)
	if err != nil {
		return nil, err
	}
	defer func() {
		if restoreErr := term.Restore(fd, state); restoreErr != nil {
			credentials.Clear(value)
			value = nil
			err = errors.Join(err, fmt.Errorf("restore terminal: %w", restoreErr))
		}
	}()
	return readBoundedTTYLine(input)
}

func readBoundedTTYLine(input io.Reader) ([]byte, error) {
	value := make([]byte, 0, credentials.MaxAPIKeyBytes)
	size := 0
	var next [1]byte
	for {
		n, err := input.Read(next[:])
		if n > 0 {
			switch next[0] {
			case '\r', '\n':
				if size > credentials.MaxAPIKeyBytes {
					return value, fmt.Errorf("API key exceeds %d bytes", credentials.MaxAPIKeyBytes)
				}
				return value, nil
			case '\b', 0x7f:
				if size > 0 {
					size--
					if size < len(value) {
						value = value[:size]
					}
				}
			case 0x03:
				return value, fmt.Errorf("API key input interrupted")
			case 0x04:
				if size == 0 {
					return value, io.EOF
				}
				if size > credentials.MaxAPIKeyBytes {
					return value, fmt.Errorf("API key exceeds %d bytes", credentials.MaxAPIKeyBytes)
				}
				return value, nil
			default:
				if size < credentials.MaxAPIKeyBytes {
					value = append(value, next[0])
				}
				size++
			}
		}
		if err != nil {
			if errors.Is(err, io.EOF) && size > 0 {
				if size > credentials.MaxAPIKeyBytes {
					return value, fmt.Errorf("API key exceeds %d bytes", credentials.MaxAPIKeyBytes)
				}
				return value, nil
			}
			return value, err
		}
	}
}

func requestedOutputFormat(args []string) string {
	_, _, format, _ := partitionCheckArgs(args)
	return format
}

func partitionCheckArgs(args []string) (flagArgs, paths []string, format string, formatSet bool) {
	format = "text"
	for index := 0; index < len(args); index++ {
		argument := args[index]
		if argument == "--" {
			paths = append(paths, args[index+1:]...)
			break
		}
		switch {
		case argument == "--output-format" || argument == "-output-format":
			flagArgs = append(flagArgs, argument)
			if index+1 < len(args) {
				index++
				flagArgs = append(flagArgs, args[index])
				format = args[index]
				formatSet = true
			}
		case strings.HasPrefix(argument, "--output-format=") || strings.HasPrefix(argument, "-output-format="):
			flagArgs = append(flagArgs, argument)
			format = strings.TrimPrefix(strings.TrimPrefix(argument, "--output-format="), "-output-format=")
			formatSet = true
		case argument == "--config" || argument == "-config":
			flagArgs = append(flagArgs, argument)
			if index+1 < len(args) {
				index++
				flagArgs = append(flagArgs, args[index])
			}
		case strings.HasPrefix(argument, "--config=") || strings.HasPrefix(argument, "-config=") || argument == "--no-cache" || argument == "-no-cache":
			flagArgs = append(flagArgs, argument)
		case argument == "-":
			paths = append(paths, argument)
		case strings.HasPrefix(argument, "-"):
			flagArgs = append(flagArgs, argument)
		default:
			paths = append(paths, argument)
		}
	}
	return flagArgs, paths, format, formatSet
}

func writeResult(format string, stdout, stderr io.Writer, result check.Result) int {
	if format == "json" {
		if err := check.WriteJSON(stdout, result); err != nil {
			fmt.Fprintf(stderr, "error: write JSON output: %s\n", err)
			return 2
		}
		return result.ExitCode()
	}
	if err := check.WriteWarnings(stderr, result); err != nil {
		fmt.Fprintf(stderr, "error: write warning output: %s\n", err)
		return 2
	}
	if err := check.WriteErrors(stderr, result); err != nil {
		fmt.Fprintf(stderr, "error: write error output: %s\n", err)
		return 2
	}
	if err := check.WriteText(stdout, result); err != nil {
		fmt.Fprintf(stderr, "error: write text output: %s\n", err)
		return 2
	}
	return result.ExitCode()
}

func writeJSONError(stdout, stderr io.Writer, kind check.ErrorKind, err error) int {
	if writeErr := check.WriteJSON(stdout, check.NewErrorResult(kind, "", err)); writeErr != nil {
		fmt.Fprintf(stderr, "error: write JSON output: %s\n", writeErr)
	}
	return 2
}

func writeCheckUsage(w io.Writer, flags *flag.FlagSet) {
	fmt.Fprintln(w, "Usage: jeff check [--config PATH] [--output-format text|json] [--no-cache] [PATH...]")
	flags.SetOutput(w)
	flags.PrintDefaults()
	flags.SetOutput(io.Discard)
}

func authUsage(w io.Writer) {
	fmt.Fprintln(w, "Usage:")
	fmt.Fprintln(w, "  jeff auth login")
	fmt.Fprintln(w, "  jeff auth logout")
}

func usage(w io.Writer) {
	fmt.Fprintln(w, "Usage:")
	fmt.Fprintln(w, "  jeff check [--config PATH] [--output-format text|json] [--no-cache] [PATH...]")
	fmt.Fprintln(w, "  jeff auth login")
	fmt.Fprintln(w, "  jeff auth logout")
}

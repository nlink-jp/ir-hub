// Package command parses the free text following the /ir-hub slash
// command. It is intentionally dependency-free and forgiving about
// flag position, but strict about unknown subcommands and flags so
// users get immediate feedback instead of silent misbehavior.
package command

import (
	"fmt"
	"slices"
	"strings"
)

// Severity levels accepted by --severity.
var Severities = []string{"low", "medium", "high", "critical"}

// DefaultSeverity applies when --severity is omitted.
const DefaultSeverity = "medium"

// Visibility is the channel visibility requested on the command line.
type Visibility int

const (
	// VisibilityDefault means no flag was given: inherit the
	// configured default.
	VisibilityDefault Visibility = iota
	VisibilityPublic
	VisibilityPrivate
)

// NewArgs carries the parsed arguments of the "new" subcommand.
type NewArgs struct {
	Title      string
	Severity   string
	Visibility Visibility
}

// Parsed is the result of parsing slash-command text.
type Parsed struct {
	// Sub is one of "new", "close", "status". Empty when the
	// command text was empty (modal mode).
	Sub string
	// New is set when Sub == "new".
	New *NewArgs
}

// Parse parses the text following /ir-hub.
func Parse(text string) (Parsed, error) {
	fields := strings.Fields(text)
	if len(fields) == 0 {
		return Parsed{}, nil
	}
	sub := fields[0]
	rest := fields[1:]
	switch sub {
	case "new":
		args, err := parseNew(rest)
		if err != nil {
			return Parsed{}, err
		}
		return Parsed{Sub: "new", New: args}, nil
	case "close", "status":
		if len(rest) > 0 {
			return Parsed{}, fmt.Errorf("%q takes no arguments", sub)
		}
		return Parsed{Sub: sub}, nil
	default:
		return Parsed{}, fmt.Errorf("unknown subcommand %q (expected: new, close, status)", sub)
	}
}

func parseNew(fields []string) (*NewArgs, error) {
	args := &NewArgs{Severity: DefaultSeverity}
	var title []string
	for i := 0; i < len(fields); i++ {
		f := fields[i]
		switch {
		case f == "--severity":
			if i+1 >= len(fields) {
				return nil, fmt.Errorf("--severity requires a value (%s)", strings.Join(Severities, "|"))
			}
			i++
			if !validSeverity(fields[i]) {
				return nil, fmt.Errorf("invalid severity %q (expected: %s)", fields[i], strings.Join(Severities, "|"))
			}
			args.Severity = fields[i]
		case f == "--private":
			if args.Visibility == VisibilityPublic {
				return nil, fmt.Errorf("--private and --public are mutually exclusive")
			}
			args.Visibility = VisibilityPrivate
		case f == "--public":
			if args.Visibility == VisibilityPrivate {
				return nil, fmt.Errorf("--private and --public are mutually exclusive")
			}
			args.Visibility = VisibilityPublic
		case strings.HasPrefix(f, "--"):
			return nil, fmt.Errorf("unknown flag %q", f)
		default:
			title = append(title, f)
		}
	}
	args.Title = strings.Join(title, " ")
	if args.Title == "" {
		return nil, fmt.Errorf("new requires a title: /ir-hub new <title> [--severity <lv>] [--private|--public]")
	}
	return args, nil
}

func validSeverity(s string) bool {
	return slices.Contains(Severities, s)
}

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

// ErrorKind identifies a parse failure so callers can render it in
// the user's language; Error() stays English for logs.
type ErrorKind int

const (
	ErrKindUnknownSubcommand ErrorKind = iota + 1
	ErrKindTakesNoArgs
	ErrKindSeverityNeedsValue
	ErrKindInvalidSeverity
	ErrKindTitleRequired
	ErrKindVisibilityConflict
	ErrKindUnknownFlag
)

// ParseError is the only error type Parse returns.
type ParseError struct {
	Kind ErrorKind
	// Arg carries the offending token where applicable (subcommand,
	// severity value, or flag).
	Arg string
}

func (e *ParseError) Error() string {
	switch e.Kind {
	case ErrKindUnknownSubcommand:
		return fmt.Sprintf("unknown subcommand %q (expected: new, close, status)", e.Arg)
	case ErrKindTakesNoArgs:
		return fmt.Sprintf("%q takes no arguments", e.Arg)
	case ErrKindSeverityNeedsValue:
		return fmt.Sprintf("--severity requires a value (%s)", strings.Join(Severities, "|"))
	case ErrKindInvalidSeverity:
		return fmt.Sprintf("invalid severity %q (expected: %s)", e.Arg, strings.Join(Severities, "|"))
	case ErrKindTitleRequired:
		return "new requires a title: /ir-hub new <title> [--severity <lv>] [--private|--public]"
	case ErrKindVisibilityConflict:
		return "--private and --public are mutually exclusive"
	case ErrKindUnknownFlag:
		return fmt.Sprintf("unknown flag %q", e.Arg)
	default:
		return "invalid command"
	}
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
			return Parsed{}, &ParseError{Kind: ErrKindTakesNoArgs, Arg: sub}
		}
		return Parsed{Sub: sub}, nil
	default:
		return Parsed{}, &ParseError{Kind: ErrKindUnknownSubcommand, Arg: sub}
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
				return nil, &ParseError{Kind: ErrKindSeverityNeedsValue}
			}
			i++
			if !validSeverity(fields[i]) {
				return nil, &ParseError{Kind: ErrKindInvalidSeverity, Arg: fields[i]}
			}
			args.Severity = fields[i]
		case f == "--private":
			if args.Visibility == VisibilityPublic {
				return nil, &ParseError{Kind: ErrKindVisibilityConflict}
			}
			args.Visibility = VisibilityPrivate
		case f == "--public":
			if args.Visibility == VisibilityPrivate {
				return nil, &ParseError{Kind: ErrKindVisibilityConflict}
			}
			args.Visibility = VisibilityPublic
		case strings.HasPrefix(f, "--"):
			return nil, &ParseError{Kind: ErrKindUnknownFlag, Arg: f}
		default:
			title = append(title, f)
		}
	}
	args.Title = strings.Join(title, " ")
	if args.Title == "" {
		return nil, &ParseError{Kind: ErrKindTitleRequired}
	}
	return args, nil
}

func validSeverity(s string) bool {
	return slices.Contains(Severities, s)
}

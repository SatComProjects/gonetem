package console

import (
	"fmt"
	"regexp"
	"sort"
)

// NetemCommand describes one interactive command.
type NetemCommand struct {
	Desc  string
	Usage string
	Args  []string
	Run   func(p *NetemPrompt, cmdArgs []string)
}

// CommandRegistry maps command names to definitions and validates arguments.
// It deliberately contains no terminal or client logic, so dispatch can be
// unit tested without a TTY or server.
type CommandRegistry map[string]*NetemCommand

// commandError carries user-facing usage text. It is kept as a type rather
// than fmt.Errorf because the console intentionally capitalizes these messages.
type commandError struct{ message string }

func (e commandError) Error() string { return e.message }

func NewCommandRegistry() CommandRegistry {
	return make(CommandRegistry)
}

// Register adds or replaces a command.
func (r CommandRegistry) Register(name string, cmd *NetemCommand) {
	r[name] = cmd
}

// Lookup returns a command by name.
func (r CommandRegistry) Lookup(name string) (*NetemCommand, bool) {
	cmd, found := r[name]
	return cmd, found
}

// Names returns the command names in deterministic order.
func (r CommandRegistry) Names() []string {
	names := make([]string, 0, len(r))
	for name := range r {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

// Parse validates the command name, arity and argument patterns and returns
// the command plus the arguments after the command name.
func (r CommandRegistry) Parse(args []string) (*NetemCommand, []string, error) {
	if len(args) == 0 {
		return nil, nil, commandError{"empty command"}
	}

	cmd, found := r[args[0]]
	if !found {
		return nil, nil, commandError{"Unknown command, enter help for details"}
	}

	if len(args) != len(cmd.Args)+1 {
		return nil, nil, commandError{fmt.Sprintf("Wrong number of arguments for '%s'\n\tusage: %s", args[0], cmd.Usage)}
	}
	for idx, pattern := range cmd.Args {
		re, err := regexp.Compile(pattern)
		if err != nil {
			return nil, nil, commandError{fmt.Sprintf("invalid argument pattern for '%s': %v", args[0], err)}
		}
		if !re.MatchString(args[idx+1]) {
			return nil, nil, commandError{fmt.Sprintf("Wrong format for argument %d\n\tusage: %s", idx+1, cmd.Usage)}
		}
	}

	return cmd, args[1:], nil
}

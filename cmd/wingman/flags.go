package main

import (
	"fmt"
	"io"
	"os"
	"slices"
	"strconv"
	"strings"
)

type flagDef struct {
	names []string
	arg   string
	usage string
	set   func(string) error
}

type flagSet struct {
	name string
	defs []*flagDef
}

func newFlags(name string) *flagSet {
	return &flagSet{name: name}
}

func (f *flagSet) Bool(p *bool, spec, usage string) {
	f.add(spec, usage, func(string) error {
		*p = true
		return nil
	})
}

func (f *flagSet) String(p *string, spec, usage string) {
	f.add(spec, usage, func(v string) error {
		*p = v
		return nil
	})
}

// Strings appends each occurrence of a repeatable flag.
func (f *flagSet) Strings(p *[]string, spec, usage string) {
	f.add(spec, usage, func(v string) error {
		*p = append(*p, v)
		return nil
	})
}

func (f *flagSet) Int(p *int, spec, usage string) {
	f.add(spec, usage, func(v string) error {
		n, err := strconv.Atoi(v)

		if err != nil {
			return fmt.Errorf("expected a number, got %q", v)
		}

		*p = n
		return nil
	})
}

func (f *flagSet) add(spec, usage string, set func(string) error) {
	def := &flagDef{usage: usage, set: set}

	for _, token := range strings.FieldsFunc(spec, func(r rune) bool { return r == ' ' || r == ',' }) {
		if strings.HasPrefix(token, "-") {
			def.names = append(def.names, token)
		} else {
			def.arg = token
		}
	}

	f.defs = append(f.defs, def)
}

func (f *flagSet) Parse(args []string) error {
	positional, err := f.ParseArgs(args)
	if err != nil {
		return err
	}
	if len(positional) > 0 {
		return fmt.Errorf("unexpected argument %q (run '%s --help' for usage)", positional[0], f.name)
	}
	return nil
}

// ParseArgs parses flags and returns positional arguments. A lone dash is a
// positional argument, and a double dash ends flag parsing.
func (f *flagSet) ParseArgs(args []string) ([]string, error) {
	var positional []string
	flagsDone := false

	for i := 0; i < len(args); i++ {
		if flagsDone {
			positional = append(positional, args[i])
			continue
		}
		if args[i] == "--" {
			flagsDone = true
			continue
		}
		if args[i] == "-" || !strings.HasPrefix(args[i], "-") {
			positional = append(positional, args[i])
			continue
		}

		name, value, hasValue := strings.Cut(args[i], "=")

		if name == "--help" || name == "-h" {
			f.printUsage(os.Stdout)
			os.Exit(0)
		}

		def := f.lookup(name)

		if def == nil {
			return nil, fmt.Errorf("unknown flag %s (run '%s --help' for usage)", name, f.name)
		}

		if def.arg == "" {
			if hasValue {
				return nil, fmt.Errorf("flag %s does not take a value", name)
			}

			_ = def.set("")
			continue
		}

		if !hasValue {
			if i+1 >= len(args) {
				return nil, fmt.Errorf("flag %s requires a value", name)
			}

			i++
			value = args[i]
		}

		if err := def.set(value); err != nil {
			return nil, fmt.Errorf("invalid value for %s: %v", name, err)
		}
	}

	return positional, nil
}

func (f *flagSet) lookup(name string) *flagDef {
	long := name

	if !strings.HasPrefix(name, "--") {
		long = "-" + name
	}

	for _, def := range f.defs {
		if slices.Contains(def.names, name) || slices.Contains(def.names, long) {
			return def
		}
	}

	return nil
}

func (f *flagSet) printUsage(w io.Writer) {
	fmt.Fprintf(w, "Usage: %s [flags]\n\nFlags:\n", f.name)

	rows := make([][2]string, 0, len(f.defs)+1)

	for _, def := range f.defs {
		label := strings.Join(def.names, ", ")

		if def.arg != "" {
			label += " " + def.arg
		}

		rows = append(rows, [2]string{label, def.usage})
	}

	rows = append(rows, [2]string{"--help, -h", "show this help"})

	width := 0

	for _, row := range rows {
		width = max(width, len(row[0]))
	}

	for _, row := range rows {
		fmt.Fprintf(w, "  %-*s  %s\n", width, row[0], row[1])
	}
}

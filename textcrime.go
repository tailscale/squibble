// Copyright (c) Tailscale Inc & AUTHORS
// SPDX-License-Identifier: BSD-3-Clause

package squibble

import (
	"fmt"
	"io"
	"strings"

	"github.com/creachadair/mds/mdiff"
	"github.com/creachadair/mds/slice"
)

// diffSchema computes a human-readable summary of the changes to the schema
// from ar to br, using the normalized form from the SQLite sqlite_schema
// table.
func diffSchema(ar, br []schemaRow) string {
	key := func(r schemaRow) string { return r.Type + "\t" + r.Name }
	lhs := make(map[string]schemaRow)
	for _, r := range ar {
		lhs[key(r)] = r
	}
	rhs := make(map[string]schemaRow)
	for _, r := range br {
		rhs[key(r)] = r
	}

	var sb strings.Builder
	for _, r := range ar {
		o, ok := rhs[key(r)]
		if !ok {
			fmt.Fprintf(&sb, "\n>> Remove %s %q\n", r.Type, r.Name)
		} else if len(r.Columns) == 0 && len(o.Columns) == 0 {
			sd := mdiff.New(cleanLines(r.SQL), cleanLines(o.SQL)).AddContext(2).Unify()
			if len(sd.Edits) != 0 {
				fmt.Fprintf(&sb, "\n>> Modify %s %q\n", r.Type, r.Name)
				mdiff.FormatUnified(&sb, sd, mdiff.NoHeader)
			}
		} else if dc := slice.EditScript(r.Columns, o.Columns); len(dc) != 0 {
			fmt.Fprintf(&sb, "\n>> Modify %s %q\n", r.Type, r.Name)
			diffColumns(&sb, dc, r.Columns, o.Columns)
		}
	}
	for _, r := range br {
		if _, ok := lhs[key(r)]; !ok && r.SQL != "" {
			fmt.Fprintf(&sb, "\n>> Add %s %q\n", r.Type, r.Name)
			indentLines(&sb, "+", r.SQL)
		}
	}
	return sb.String()
}

func indentLines(w io.Writer, indent, text string) {
	if text == "" {
		return
	}
	for _, line := range strings.Split(text, "\n") {
		fmt.Fprintln(w, indent, line)
	}
}

func diffColumns(w io.Writer, dc []slice.Edit[schemaCol], lhs, rhs []schemaCol) {
	for _, e := range dc {
		switch e.Op {
		case slice.OpCopy:
			for _, col := range e.Y {
				fmt.Fprintf(w, " + add column %v\n", col)
			}
		case slice.OpReplace:
			for i, old := range e.X {
				fmt.Fprintf(w, " ! replace column %v\n   with %v\n", old, e.Y[i])
			}
		case slice.OpDrop:
			for _, col := range e.X {
				fmt.Fprintf(w, " - remove column %v\n", col)
			}
		}
	}
}

func cleanLines(s string) []string {
	lines := mdiff.Lines(s)
	for i, s := range lines {
		lines[i] = strings.TrimSpace(s)
	}
	return lines
}

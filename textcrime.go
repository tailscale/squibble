// Copyright (c) Tailscale Inc & AUTHORS
// SPDX-License-Identifier: BSD-3-Clause

package squibble

import (
	"fmt"
	"io"
	"strings"

	"github.com/creachadair/mds/mdiff"
	"github.com/creachadair/mds/mstr"
	"github.com/creachadair/mds/slice"
)

// diffSchema computes a human-readable summary of the changes to the schema
// from ar to br, using the normalized form from the SQLite sqlite_schema
// table.
func diffSchema(ar, br []schemaRow) string {
	lhs := make(map[mapKey]schemaRow)
	for _, r := range ar {
		lhs[r.mapKey()] = r
	}
	rhs := make(map[mapKey]schemaRow)
	for _, r := range br {
		rhs[r.mapKey()] = r
	}

	var sb strings.Builder
	for _, r := range ar {
		o, ok := rhs[r.mapKey()]
		if !ok {
			fmt.Fprintf(&sb, "\n>> Remove %s %q\n", r.Type, r.Name)
			continue
		}

		// Indices and views do not have columns, so diff those using their
		// normalized SQL representation.
		if len(r.Columns) == 0 && len(o.Columns) == 0 {
			if cleanSQL(r.SQL) == cleanSQL(o.SQL) {
				continue
			}
			sd := mdiff.New(cleanLines(r.SQL), cleanLines(o.SQL)).AddContext(2).Unify()
			if len(sd.Edits) != 0 {
				fmt.Fprintf(&sb, "\n>> Modify %s %q\n", r.Type, r.Name)
				mdiff.FormatUnified(&sb, sd, nil)
			}
			continue
		}

		// For tables, diff the columns (this requires that they are already sorted).
		dc := slice.EditScript(r.Columns, o.Columns)
		if len(dc) == 0 {
			continue // no difference
		}
		fmt.Fprintf(&sb, "\n>> Modify %s %q\n", r.Type, r.Name)
		diffColumns(&sb, dc, r.Columns, o.Columns)
	}
	for _, r := range br {
		if _, ok := lhs[r.mapKey()]; ok {
			continue // we already dealt with this above
		}
		fmt.Fprintf(&sb, "\n>> Add %s %q\n", r.Type, r.Name)
		if r.SQL != "" {
			indentLines(&sb, "+", r.SQL)
		}
	}
	return sb.String()
}

// indentLines writes the lines of text to w, each indented with indent.
func indentLines(w io.Writer, indent, text string) {
	for _, line := range mstr.Lines(text) {
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

// cleanLines returns the lines of s "cleaned" by removing leading and trailing
// whitespace from them.
func cleanLines(s string) []string {
	lines := mstr.Lines(s)
	for i, s := range lines {
		lines[i] = strings.TrimSpace(s)
	}
	return lines
}

// cleanSQL returns a "clean" copy of s, in which leading and trailing
// whitespace on each line has been removed.
func cleanSQL(s string) string { return strings.Join(cleanLines(s), " ") }

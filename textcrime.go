// Copyright (c) Tailscale Inc & AUTHORS
// SPDX-License-Identifier: BSD-3-Clause

package squibble

import (
	"fmt"
	"io"
	"strings"

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
		} else if dc := slice.EditScript(r.Columns, o.Columns); len(dc) != 0 {
			fmt.Fprintf(&sb, "\n>> Modify %s %q\n", r.Type, r.Name)
			diffColumns(&sb, dc, r.Columns, o.Columns)
		} else if r.SQL != o.SQL {
			fmt.Fprintf(&sb, "\n>> Modify %s %q\n", r.Type, r.Name)
			diffSQL(&sb, r.SQL, o.SQL)
		}
	}
	for _, r := range br {
		if _, ok := lhs[key(r)]; !ok && r.SQL != "" {
			fmt.Fprintf(&sb, "\n>> Add %s %q\n", r.Type, r.Name)
			diffSQL(&sb, "", r.SQL)
		}
	}
	return sb.String()
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

func lines(s string) []string {
	if s == "" {
		return nil
	}
	return strings.Split(s, "\n")
}

func diffSQL(w io.Writer, a, b string) {
	for _, e := range slice.EditScript(lines(a), lines(b)) {
		switch e.Op {
		case slice.OpCopy:
			for _, s := range e.Y {
				fmt.Fprintf(w, " + %s\n", s)
			}
		case slice.OpReplace:
			for i, s := range e.X {
				fmt.Fprintf(w, " ! %s\n + %s\n", s, e.Y[i])
			}
		case slice.OpDrop:
			for _, s := range e.X {
				fmt.Fprintf(w, " - %s\n", s)
			}
		}
	}
}

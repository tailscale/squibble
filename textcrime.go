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

func diffColumns(w io.Writer, dc []slice.Edit, lhs, rhs []schemaCol) {
	i := 0
	for _, e := range dc {
		switch e.Op {
		case slice.OpCopy:
			for j := e.X; j < e.X+e.N; j++ {
				fmt.Fprintf(w, " + add column %v\n", rhs[j])
			}
		case slice.OpReplace:
			for j := e.X; j < e.X+e.N; j++ {
				fmt.Fprintf(w, " ! replace column %v\n   with %v\n", lhs[i], rhs[j])
				i++
			}
		case slice.OpDrop:
			for j := i; j < i+e.N; j++ {
				fmt.Fprintf(w, " - remove column %v\n", lhs[j])
			}
			i += e.N
		case slice.OpEmit:
			i += e.N
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
	lhs, rhs := lines(a), lines(b)
	i := 0
	for _, e := range slice.EditScript(lhs, rhs) {
		switch e.Op {
		case slice.OpCopy:
			for j := e.X; j < e.X+e.N; j++ {
				fmt.Fprintf(w, " + %s\n", rhs[j])
			}
		case slice.OpReplace:
			for j := e.X; j < e.X+e.N; j++ {
				fmt.Fprintf(w, " ! %s\n + %s\n", lhs[i], rhs[j])
				i++
			}
		case slice.OpDrop:
			for j := i; j < i+e.N; j++ {
				fmt.Fprintf(w, " - %s\n", lhs[j])
			}
			i += e.N
		case slice.OpEmit:
			i += e.N
		}
	}
}
